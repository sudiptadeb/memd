// Minimal, safe Markdown renderer for the directory file viewer.
//
// Ported verbatim (behaviour-for-behaviour) from the Alpine build's
// `renderMarkdown` family in server/internal/ui/assets/script.js. The whole
// source is entity-escaped before any tags are emitted and only a fixed tag set
// is produced, so Markdown stored in memory files can never inject markup that
// runs on the UI origin. Relative links become `data-rel` anchors the viewer
// intercepts; the raw bytes stay one click away via "Open in new tab".

export interface MarkdownOpts {
  // Resolve a relative link href to a directory-relative path (for data-rel).
  linkPath: (href: string) => string;
  // Resolve a relative image src to a fetchable raw URL.
  imageURL: (src: string) => string;
}

// path helpers -------------------------------------------------------------

export function parentPath(path: string): string {
  const index = path.lastIndexOf("/");
  return index === -1 ? "" : path.slice(0, index);
}

export function baseName(path: string): string {
  const index = path.lastIndexOf("/");
  return index === -1 ? path : path.slice(index + 1);
}

// Resolve a relative markdown link against the linking file's folder,
// clamping ".." at the directory root.
export function resolveRelPath(baseDir: string, rel: string): string {
  const stack = rel.charAt(0) === "/" ? [] : baseDir.split("/").filter(Boolean);
  rel.split("/").forEach((part) => {
    if (!part || part === ".") return;
    if (part === "..") {
      stack.pop();
    } else {
      stack.push(part);
    }
  });
  return stack.join("/");
}

// rendering ----------------------------------------------------------------

function escapeHTML(text: string): string {
  return String(text)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

// Inline markdown -> HTML. Source text is entity-escaped before any tags are
// added, so stored memory content can never inject markup of its own.
function renderInline(text: string, opts: MarkdownOpts): string {
  const tokens: string[] = [];
  function stash(html: string): string {
    tokens.push(html);
    return "\u0000" + (tokens.length - 1) + "\u0000";
  }
  let out = String(text).replace(/\u0000/g, "");
  out = out.replace(/`([^`]+)`/g, (_, code: string) => stash("<code>" + escapeHTML(code) + "</code>"));
  out = out.replace(/!\[([^\]]*)\]\(([^()\s]+)\)/g, (_, alt: string, src: string) => {
    if (/^[a-z][a-z0-9+.-]*:/i.test(src)) {
      // Never auto-load remote images from stored memory content.
      return "[" + alt + "](" + src + ")";
    }
    return stash(
      '<img src="' + escapeHTML(opts.imageURL(src)) + '" alt="' + escapeHTML(alt) + '" loading="lazy" />',
    );
  });
  out = out.replace(/\[([^\]]+)\]\(([^()\s]+)\)/g, (_, label: string, href: string) => {
    const labelHTML = escapeHTML(label);
    if (/^https?:\/\//i.test(href)) {
      return stash(
        '<a href="' + escapeHTML(href) + '" target="_blank" rel="noopener noreferrer">' + labelHTML + "</a>",
      );
    }
    if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.charAt(0) === "#") {
      return stash(labelHTML);
    }
    return stash('<a href="#" data-rel="' + escapeHTML(opts.linkPath(href)) + '">' + labelHTML + "</a>");
  });
  out = escapeHTML(out);
  out = out.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  out = out.replace(/__([^_]+)__/g, "<strong>$1</strong>");
  out = out.replace(/\*([^*]+)\*/g, "<em>$1</em>");
  out = out.replace(/~~([^~]+)~~/g, "<del>$1</del>");
  return out.replace(/\u0000(\d+)\u0000/g, (_, index: string) => tokens[Number(index)]);
}

interface ListItem {
  indent: number;
  ordered: boolean;
  text: string;
}

function renderListItems(items: ListItem[], opts: MarkdownOpts): string {
  const tag = items[0].ordered ? "ol" : "ul";
  let html = "<" + tag + ">";
  let index = 0;
  while (index < items.length) {
    const item = items[index];
    index += 1;
    const children: ListItem[] = [];
    while (index < items.length && items[index].indent > item.indent) {
      children.push(items[index]);
      index += 1;
    }
    html += "<li>" + renderInline(item.text, opts) + (children.length ? renderListItems(children, opts) : "") + "</li>";
  }
  return html + "</" + tag + ">";
}

function splitTableRow(line: string): string[] {
  return line
    .trim()
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split("|")
    .map((cell) => cell.trim());
}

// Minimal markdown renderer for the file viewer. The whole source is escaped and
// only a fixed tag set is emitted, so markup stored in memory files cannot run
// on the UI origin. Fidelity beyond the common constructs (headings, lists,
// code, tables, quotes, links, front matter) is not a goal.
function renderMarkdown(source: string, opts: MarkdownOpts): string {
  const lines = String(source || "").replace(/\r\n?/g, "\n").split("\n");
  const blocks: string[] = [];
  let paragraph: string[] = [];
  let i = 0;

  function flushParagraph(): void {
    if (paragraph.length) {
      blocks.push("<p>" + renderInline(paragraph.join(" "), opts) + "</p>");
      paragraph = [];
    }
  }

  if (lines[0] === "---") {
    for (let end = 1; end < lines.length; end++) {
      if (lines[end].trim() === "---") {
        blocks.push('<pre class="md-frontmatter">' + escapeHTML(lines.slice(1, end).join("\n")) + "</pre>");
        i = end + 1;
        break;
      }
    }
  }

  while (i < lines.length) {
    const line = lines[i];
    if (/^```/.test(line)) {
      flushParagraph();
      const buffer: string[] = [];
      i += 1;
      while (i < lines.length && !/^```/.test(lines[i])) {
        buffer.push(lines[i]);
        i += 1;
      }
      i += 1;
      blocks.push("<pre><code>" + escapeHTML(buffer.join("\n")) + "</code></pre>");
      continue;
    }
    const heading = line.match(/^(#{1,6})\s+(.*)$/);
    if (heading) {
      flushParagraph();
      const level = heading[1].length;
      blocks.push("<h" + level + ">" + renderInline(heading[2], opts) + "</h" + level + ">");
      i += 1;
      continue;
    }
    if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) {
      flushParagraph();
      blocks.push("<hr />");
      i += 1;
      continue;
    }
    if (/^\s*>/.test(line)) {
      flushParagraph();
      const buffer: string[] = [];
      while (i < lines.length && /^\s*>/.test(lines[i])) {
        buffer.push(lines[i].replace(/^\s*>\s?/, ""));
        i += 1;
      }
      blocks.push("<blockquote>" + renderMarkdown(buffer.join("\n"), opts) + "</blockquote>");
      continue;
    }
    if (/^(\s*)(?:[-*+]|\d+\.)\s+/.test(line)) {
      flushParagraph();
      const items: ListItem[] = [];
      while (i < lines.length) {
        const item = lines[i].match(/^(\s*)([-*+]|\d+\.)\s+(.*)$/);
        if (item) {
          items.push({ indent: item[1].length, ordered: /^\d/.test(item[2]), text: item[3] });
          i += 1;
          continue;
        }
        if (items.length && /^\s+\S/.test(lines[i])) {
          items[items.length - 1].text += " " + lines[i].trim();
          i += 1;
          continue;
        }
        break;
      }
      blocks.push(renderListItems(items, opts));
      continue;
    }
    if (/^\s*\|.*\|\s*$/.test(line) && /^\s*\|(\s*:?-+:?\s*\|)+\s*$/.test(lines[i + 1] || "")) {
      flushParagraph();
      const head = splitTableRow(line);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && /^\s*\|.*\|\s*$/.test(lines[i])) {
        rows.push(splitTableRow(lines[i]));
        i += 1;
      }
      blocks.push(
        "<table><thead><tr>" +
          head.map((cell) => "<th>" + renderInline(cell, opts) + "</th>").join("") +
          "</tr></thead><tbody>" +
          rows
            .map(
              (row) => "<tr>" + row.map((cell) => "<td>" + renderInline(cell, opts) + "</td>").join("") + "</tr>",
            )
            .join("") +
          "</tbody></table>",
      );
      continue;
    }
    if (!line.trim()) {
      flushParagraph();
      i += 1;
      continue;
    }
    paragraph.push(line.trim());
    i += 1;
  }
  flushParagraph();
  return blocks.join("\n");
}

export function renderMarkdownFile(
  filePath: string,
  source: string,
  rawURL: (path: string) => string,
): string {
  const baseDir = parentPath(filePath);
  return renderMarkdown(source, {
    linkPath: (href) => resolveRelPath(baseDir, href),
    imageURL: (src) => rawURL(resolveRelPath(baseDir, src)),
  });
}

// File-type predicates (match the Alpine viewer).
export function isImagePath(path: string): boolean {
  return /\.(png|jpe?g|gif|webp)$/i.test(path || "");
}
export function isPdfPath(path: string): boolean {
  return /\.pdf$/i.test(path || "");
}
export function isMarkdownPath(path: string): boolean {
  return /\.(md|markdown)$/i.test(path || "");
}
export function isRenderablePath(path: string): boolean {
  return /\.(html?|svg)$/i.test(path || "");
}

(function () {
  function storageGet(key, fallback) {
    try {
      const value = window.localStorage.getItem(key);
      return value === null ? fallback : value;
    } catch (_) {
      return fallback;
    }
  }

  function storageSet(key, value) {
    try {
      window.localStorage.setItem(key, value);
    } catch (_) {}
  }

  // Use the saved theme if there is one; otherwise follow the OS preference.
  function resolveTheme() {
    const saved = storageGet("memd-theme", null);
    if (saved === "dark" || saved === "light") return saved;
    try {
      if (window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches) {
        return "dark";
      }
    } catch (_) {}
    return "light";
  }

  const initialTheme = resolveTheme();
  document.documentElement.setAttribute("data-theme", initialTheme === "dark" ? "dark" : "light");
  const initialLayout = storageGet("memd-layout", "wide");
  document.documentElement.setAttribute("data-layout", initialLayout === "centered" ? "centered" : "wide");

  async function responseJSON(response) {
    if (response.ok) {
      return response.json().catch(function () {
        return {};
      });
    }
    const payload = await response.json().catch(function () {
      return {};
    });
    const error = new Error(payload.error || response.statusText || "request failed");
    error.status = response.status;
    throw error;
  }

  async function api(path, options) {
    return responseJSON(await fetch(path, options || {}));
  }

  function copyText(text) {
    if (window.navigator.clipboard && window.navigator.clipboard.writeText) {
      return window.navigator.clipboard.writeText(text);
    }
    const textarea = document.createElement("textarea");
    textarea.value = text;
    textarea.setAttribute("readonly", "");
    textarea.style.position = "fixed";
    textarea.style.left = "-9999px";
    document.body.appendChild(textarea);
    textarea.select();
    try {
      document.execCommand("copy");
      return Promise.resolve();
    } finally {
      document.body.removeChild(textarea);
    }
  }

  function currentBaseURL() {
    return window.location.origin;
  }

  function publicURL(rawURL) {
    try {
      const url = new URL(rawURL, currentBaseURL());
      return currentBaseURL() + url.pathname + url.search + url.hash;
    } catch (_) {
      return rawURL || "";
    }
  }

  function publicPath(rawURL) {
    try {
      return new URL(rawURL, currentBaseURL()).pathname;
    } catch (_) {
      return "";
    }
  }

  function appendPath(baseURL, path) {
    return publicURL(baseURL).replace(/\/+$/, "") + "/" + path.replace(/^\/+/, "");
  }

  function tokenlessConnectorURL(connector) {
    if (connector.auth_url) {
      return publicURL(connector.auth_url);
    }
    try {
      const url = new URL(connector.url, currentBaseURL());
      const segments = url.pathname.split("/").filter(Boolean);
      if (segments[0] === "mcp" || segments[0] === "http") {
        return currentBaseURL() + "/" + segments[0];
      }
    } catch (_) {}
    return publicURL(connector.url);
  }

  function connectorPath(connector) {
    return publicPath(connector.url);
  }

  function defaultDirForm() {
    return {
      name: "",
      team_id: "",
      description: "",
      backend: "local",
      local_path: "",
      remote_url: "",
      branch: "main",
      base_path: "",
      author_name: "memd",
      author_email: "memd@localhost",
      auth_username: "",
      auth_token: "",
      ssh_key_path: "",
      checking: false,
      checkErr: "",
      checkResults: [],
      err: "",
      submitting: false
    };
  }

  function defaultDirEditForm() {
    return {
      id: "",
      originalName: "",
      name: "",
      description: "",
      err: "",
      submitting: false
    };
  }

  function defaultConnForm() {
    return {
      name: "",
      team_id: "",
      kind: "mcp",
      selected: [],
      write: true,
      err: "",
      submitting: false
    };
  }

  function defaultEditForm() {
    return {
      id: "",
      originalName: "",
      team_id: "",
      name: "",
      kind: "mcp",
      selected: [],
      write: true,
      err: "",
      submitting: false
    };
  }

  function defaultLoginForm() {
    return {
      username: "",
      password: "",
      err: "",
      submitting: false
    };
  }

  function defaultTeamForm() {
    return {
      name: "",
      slug: "",
      err: "",
      submitting: false
    };
  }

  function defaultMemberForm() {
    return {
      username: "",
      role: "member",
      err: "",
      submitting: false
    };
  }

  function defaultInviteForm() {
    return {
      role: "member",
      expires_at: "",
      max_uses: "",
      err: "",
      submitting: false
    };
  }

  function downloadJSON(filename, value) {
    const blob = new Blob([JSON.stringify(value, null, 2) + "\n"], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }

  function readFileText(file) {
    return new Promise(function (resolve, reject) {
      const reader = new FileReader();
      reader.onload = function () { resolve(String(reader.result || "")); };
      reader.onerror = function () { reject(reader.error || new Error("read failed")); };
      reader.readAsText(file);
    });
  }

  function parentPath(path) {
    const index = path.lastIndexOf("/");
    return index === -1 ? "" : path.slice(0, index);
  }

  function baseName(path) {
    const index = path.lastIndexOf("/");
    return index === -1 ? path : path.slice(index + 1);
  }

  // Resolve a relative markdown link against the linking file's folder,
  // clamping ".." at the directory root.
  function resolveRelPath(baseDir, rel) {
    const stack = rel.charAt(0) === "/" ? [] : baseDir.split("/").filter(Boolean);
    rel.split("/").forEach(function (part) {
      if (!part || part === ".") return;
      if (part === "..") {
        stack.pop();
      } else {
        stack.push(part);
      }
    });
    return stack.join("/");
  }

  function escapeHTML(text) {
    return String(text)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  // Inline markdown -> HTML. Source text is entity-escaped before any tags are
  // added, so stored memory content can never inject markup of its own.
  function renderInline(text, opts) {
    const tokens = [];
    function stash(html) {
      tokens.push(html);
      return "\u0000" + (tokens.length - 1) + "\u0000";
    }
    let out = String(text).replace(/\u0000/g, "");
    out = out.replace(/`([^`]+)`/g, function (_, code) {
      return stash("<code>" + escapeHTML(code) + "</code>");
    });
    out = out.replace(/!\[([^\]]*)\]\(([^()\s]+)\)/g, function (_, alt, src) {
      if (/^[a-z][a-z0-9+.-]*:/i.test(src)) {
        // Never auto-load remote images from stored memory content.
        return "[" + alt + "](" + src + ")";
      }
      return stash('<img src="' + escapeHTML(opts.imageURL(src)) + '" alt="' + escapeHTML(alt) + '" loading="lazy" />');
    });
    out = out.replace(/\[([^\]]+)\]\(([^()\s]+)\)/g, function (_, label, href) {
      const labelHTML = escapeHTML(label);
      if (/^https?:\/\//i.test(href)) {
        return stash('<a href="' + escapeHTML(href) + '" target="_blank" rel="noopener noreferrer">' + labelHTML + "</a>");
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
    return out.replace(/\u0000(\d+)\u0000/g, function (_, index) {
      return tokens[Number(index)];
    });
  }

  function renderListItems(items, opts) {
    const tag = items[0].ordered ? "ol" : "ul";
    let html = "<" + tag + ">";
    let index = 0;
    while (index < items.length) {
      const item = items[index];
      index += 1;
      const children = [];
      while (index < items.length && items[index].indent > item.indent) {
        children.push(items[index]);
        index += 1;
      }
      html += "<li>" + renderInline(item.text, opts) + (children.length ? renderListItems(children, opts) : "") + "</li>";
    }
    return html + "</" + tag + ">";
  }

  function splitTableRow(line) {
    return line.trim().replace(/^\|/, "").replace(/\|$/, "").split("|").map(function (cell) {
      return cell.trim();
    });
  }

  // Minimal markdown renderer for the file viewer. The whole source is
  // escaped and only a fixed tag set is emitted, so markup stored in memory
  // files cannot run on the UI origin. Fidelity beyond the common constructs
  // (headings, lists, code, tables, quotes, links, front matter) is not a goal;
  // the raw bytes stay one click away via "Open in new tab".
  function renderMarkdown(source, opts) {
    const lines = String(source || "").replace(/\r\n?/g, "\n").split("\n");
    const blocks = [];
    let paragraph = [];
    let i = 0;

    function flushParagraph() {
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
        const buffer = [];
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
        const buffer = [];
        while (i < lines.length && /^\s*>/.test(lines[i])) {
          buffer.push(lines[i].replace(/^\s*>\s?/, ""));
          i += 1;
        }
        blocks.push("<blockquote>" + renderMarkdown(buffer.join("\n"), opts) + "</blockquote>");
        continue;
      }
      if (/^(\s*)(?:[-*+]|\d+\.)\s+/.test(line)) {
        flushParagraph();
        const items = [];
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
        const rows = [];
        while (i < lines.length && /^\s*\|.*\|\s*$/.test(lines[i])) {
          rows.push(splitTableRow(lines[i]));
          i += 1;
        }
        blocks.push(
          "<table><thead><tr>" +
            head.map(function (cell) { return "<th>" + renderInline(cell, opts) + "</th>"; }).join("") +
            "</tr></thead><tbody>" +
            rows.map(function (row) {
              return "<tr>" + row.map(function (cell) { return "<td>" + renderInline(cell, opts) + "</td>"; }).join("") + "</tr>";
            }).join("") +
            "</tbody></table>"
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

  function renderMarkdownFile(filePath, source, rawURL) {
    const baseDir = parentPath(filePath);
    return renderMarkdown(source, {
      linkPath: function (href) { return resolveRelPath(baseDir, href); },
      imageURL: function (src) { return rawURL(resolveRelPath(baseDir, src)); }
    });
  }

  window.memdApp = function () {
    return {
      sessionChecked: false,
      user: null,
      oidcEnabled: false,
      showLocalLogin: false,
      ssoRedirecting: false,
      loading: true,
      loadErr: "",
      inviteToken: "",
      invitePreview: null,
      inviteErr: "",
      inviteAccepting: false,
      directories: [],
      connectors: [],
      teams: [],
      activeView: storageGet("memd-view", "directories"),
      navOpen: false,
      loginForm: defaultLoginForm(),
      theme: resolveTheme(),
      layoutMode: storageGet("memd-layout", "wide") === "centered" ? "centered" : "wide",
      logsHidden: storageGet("memd-logs-hidden", "0") === "1",
      logsWidth: parseInt(storageGet("memd-logs-w", "340"), 10) || 340,
      sheet: null,
      dirForm: defaultDirForm(),
      dirEditForm: defaultDirEditForm(),
      connForm: defaultConnForm(),
      editForm: defaultEditForm(),
      teamForm: defaultTeamForm(),
      teamDetail: null,
      teamMembers: [],
      teamInvites: [],
      memberForm: defaultMemberForm(),
      inviteForm: defaultInviteForm(),
      createdInviteURL: "",
      pickerOpen: false,
      pickerPath: "",
      pickerEntries: [],
      pickerParent: "",
      pickerErr: "",
      browserDir: null,
      browserPath: "",
      browserEntries: [],
      browserLoading: false,
      browserErr: "",
      browserFile: null,
      browserFileContent: "",
      browserFileHTML: "",
      browserFileLoading: false,
      browserFileErr: "",
      tasksAll: [],
      tasksFilter: "",
      tasksLoading: false,
      tasksLoaded: false,
      tasksErr: "",
      tasksHideDone: storageGet("memd-tasks-hide-done", "") === "1",
      routeApplying: false,
      toast: "",
      toastLevel: "info",
      toastTimer: null,
      entries: [],
      lastID: -1,
      logsPolling: false,
      logsTimer: null,

      async init() {
        // Back-navigation from the identity provider restores the page from
        // bfcache with stale state; reset so the SSO button is usable again.
        window.addEventListener("pageshow", (event) => {
          if (event.persisted) {
            this.ssoRedirecting = false;
          }
        });
        this.setTheme(this.theme);
        this.setLayout(this.layoutMode);
        this.setLogsWidth(this.logsWidth);
        this.inviteToken = new URLSearchParams(window.location.search).get("invite") || "";
        if (this.inviteToken) {
          await this.loadInvitePreview();
        }
        this.readLoginError();
        window.addEventListener("hashchange", () => { this.applyBrowserRoute(); });
        await this.checkSession();
        if (this.user) {
          await this.load();
          this.startLogs();
          await this.applyBrowserRoute();
          // View restored from localStorage (no hash) still needs its data.
          if (this.activeView === "tasks" && !this.tasksLoaded) {
            await this.loadTasksAll();
          }
          // Reflect the active view in the address bar on first load too,
          // without adding a history entry.
          this.syncURL(true);
        }
      },

      readLoginError() {
        const params = new URLSearchParams(window.location.search);
        const err = params.get("login_error");
        if (err) {
          this.loginForm.err = err;
          params.delete("login_error");
          const qs = params.toString();
          window.history.replaceState({}, "", window.location.pathname + (qs ? "?" + qs : ""));
        }
      },

      async checkSession() {
        try {
          const data = await api("/api/session", { cache: "no-store" });
          this.user = data.user || null;
          this.oidcEnabled = Boolean(data.auth && data.auth.oidc_enabled);
          // When SSO is the default, keep the local form tucked away as a backup.
          this.showLocalLogin = !this.oidcEnabled;
        } catch (error) {
          this.user = null;
        } finally {
          this.normalizeView();
          this.sessionChecked = true;
          this.loading = false;
        }
      },

      ssoLogin() {
        if (this.ssoRedirecting) return;
        this.ssoRedirecting = true;
        window.location.href = "/auth/login";
      },

      async login() {
        this.loginForm.err = "";
        this.loginForm.submitting = true;
        try {
          const data = await api("/api/auth/login", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              username: this.loginForm.username,
              password: this.loginForm.password
            })
          });
          this.user = data.user || null;
          this.normalizeView();
          this.loginForm = defaultLoginForm();
          await this.load();
          this.startLogs();
          await this.applyBrowserRoute();
        } catch (error) {
          this.loginForm.err = error.message || "login failed";
        } finally {
          this.loginForm.submitting = false;
        }
      },

      async logout() {
        let logoutURL = "";
        try {
          const data = await api("/api/auth/logout", { method: "POST" });
          logoutURL = (data && data.logout_url) || "";
        } catch (error) {}
        this.stopLogs();
        this.user = null;
        this.directories = [];
        this.connectors = [];
        this.teams = [];
        this.teamDetail = null;
        this.teamMembers = [];
        this.teamInvites = [];
        this.entries = [];
        this.lastID = -1;
        this.activeView = "directories";
        this.navOpen = false;
        storageSet("memd-view", this.activeView);
        this.closeOverlays();
        if (logoutURL) {
          window.location.href = logoutURL;
        }
      },

      startLogs() {
        this.pollLogs();
        if (!this.logsTimer) {
          this.logsTimer = window.setInterval(() => this.pollLogs(), 2000);
        }
      },

      stopLogs() {
        if (this.logsTimer) {
          window.clearInterval(this.logsTimer);
          this.logsTimer = null;
        }
      },

      async load() {
        if (!this.user) {
          return;
        }
        this.loading = true;
        this.loadErr = "";
        try {
          const results = await Promise.all([
            api("/api/directories"),
            api("/api/connectors"),
            api("/api/teams")
          ]);
          this.directories = results[0].directories || [];
          this.connectors = (results[1].connectors || []).map(function (connector) {
            connector.revealed = false;
            connector.expanded = false;
            connector.kind = connector.kind || "mcp";
            connector.url = publicURL(connector.url);
            connector.auth_url = tokenlessConnectorURL(connector);
            return connector;
          });
          this.teams = results[2].teams || [];
          this.normalizeView();
          // Defer the onboarding view switch to the next tick so it is not
          // batched into the same reactive flush as loading=false / fresh data.
          this.$nextTick(() => this.maybeShowOnboarding());
        } catch (error) {
          if (error.status === 401) {
            this.user = null;
            this.stopLogs();
          }
          this.loadErr = error.message || String(error);
        } finally {
          this.loading = false;
        }
      },

      // --- Onboarding: a first login with no connectors lands on "How it
      // works" with a guided checklist, until the user creates a connector or
      // dismisses it. ---

      onboardingKey() {
        return this.user ? "memd-onboarded-" + this.user.id : "memd-onboarded";
      },

      get showGetStarted() {
        return Boolean(this.user) && !this.connectors.length;
      },

      sharedDirectories() {
        const self = this;
        return this.directories.filter(function (directory) {
          return directory.team_id && directory.owner_user_id && self.user && directory.owner_user_id !== self.user.id;
        });
      },

      maybeShowOnboarding() {
        if (!this.showGetStarted || storageGet(this.onboardingKey(), "") === "1") {
          return;
        }
        // Respect a view the user deep-linked to (or restored from the hash);
        // only steer the default landing into onboarding.
        if (this.activeView !== "directories") {
          return;
        }
        this.activeView = "info";
        storageSet("memd-view", this.activeView);
        this.syncURL();
      },

      dismissOnboarding() {
        storageSet(this.onboardingKey(), "1");
        this.setView("directories");
      },

      normalizeView() {
        const valid = ["info", "directories", "tasks", "connectors", "logs"];
        if (this.user && !this.user.super_admin) {
          valid.unshift("teams");
        }
        if (!valid.includes(this.activeView)) {
          this.activeView = "directories";
        }
        storageSet("memd-view", this.activeView);
      },

      // viewIs powers each main section's x-show. It reads activeView, loading
      // and loadErr into locals *before* combining them, so all three are always
      // registered as reactive dependencies. A bare `a === x && !b` expression
      // short-circuits and can drop a dependency mid-flush, which wedged the
      // initially-active section's x-show effect (it stopped reacting to
      // activeView). Reading unconditionally avoids that entirely.
      viewIs(name) {
        const view = this.activeView;
        const loading = this.loading;
        const err = this.loadErr;
        return view === name && !loading && !err;
      },

      setView(view) {
        this.activeView = view || "directories";
        this.normalizeView();
        if (this.activeView === "tasks" && !this.routeApplying) {
          this.loadTasksAll();
        }
        this.syncURL();
        this.closeNavIfMobile();
      },

      // Reflect the current location in the URL hash so every view (and the
      // browse sheet / Tasks filter) survives a reload and can be shared:
      //   #browse=<dir>&...   the file browser sheet (takes precedence)
      //   #tasks=<dir|all>    the Tasks view + its directory filter
      //   #view=<name>        any other main view (info/teams/directories/…)
      syncURL(replace) {
        if (this.routeApplying) return;
        let target;
        if (this.sheet === "browse" && this.browserDir) {
          target = this.browserHash();
        } else if (this.activeView === "tasks") {
          target = "#tasks=" + (this.tasksFilter || "all");
        } else {
          target = "#view=" + this.activeView;
        }
        const current = window.location.hash;
        if (target === current) return;
        const url = window.location.pathname + window.location.search + target;
        if (replace) {
          window.history.replaceState({}, "", url);
        } else {
          window.history.pushState({}, "", url);
        }
      },

      // Back-compat aliases used by the browse-sheet and Tasks-filter call sites.
      syncBrowserURL() {
        this.syncURL();
      },
      syncTasksURL() {
        this.syncURL();
      },

      isMobileNav() {
        return window.matchMedia && window.matchMedia("(max-width: 920px)").matches;
      },

      toggleNav() {
        this.navOpen = !this.navOpen;
      },

      closeNav() {
        this.navOpen = false;
      },

      closeNavIfMobile() {
        if (this.isMobileNav()) {
          this.closeNav();
        }
      },

      manageableTeams() {
        return this.teams.filter((team) => team.can_manage);
      },

      teamName(teamID) {
        const team = this.teams.find((item) => item.id === teamID);
        return team ? team.name : "";
      },

      // Everything you can use, in one list: personal items first, then
      // team-shared items grouped per team. The team badge on each card is
      // what tells personal and shared items apart.
      sortedDirectories() {
        return this.sortByTeam(this.directories);
      },

      sortedConnectors() {
        return this.sortByTeam(this.connectors);
      },

      sortByTeam(items) {
        const self = this;
        return items.slice().sort(function (a, b) {
          const teamA = a.team_id ? (self.teamName(a.team_id) || "Team") : "";
          const teamB = b.team_id ? (self.teamName(b.team_id) || "Team") : "";
          if (teamA !== teamB) {
            if (!teamA) return -1;
            if (!teamB) return 1;
            return teamA.localeCompare(teamB);
          }
          return (a.name || "").localeCompare(b.name || "");
        });
      },

      // Directories a connector may reference. A team-scoped connector is limited
      // to that team's directories; a personal connector may reference anything
      // you can attach — your own directories plus any team directory you have
      // write access to (so a member can build their own connector against a
      // teammate's shared directory).
      attachableDirectories(teamID) {
        const scope = teamID || "";
        return this.directories.filter((directory) => {
          if (!directory.can_attach) return false;
          if (scope === "") return true;
          return (directory.team_id || "") === scope;
        });
      },

      roleLabel(role) {
        return role || "member";
      },

      inviteUsage(invite) {
        const limit = invite.max_uses ? String(invite.max_uses) : "unlimited";
        return String(invite.use_count || 0) + " / " + limit;
      },

      inviteExpiry(invite) {
        if (!invite.expires_at) return "No expiry";
        return new Date(invite.expires_at).toLocaleString();
      },

      async loadInvitePreview() {
        this.inviteErr = "";
        this.invitePreview = null;
        if (!this.inviteToken) return;
        try {
          const data = await api("/api/team-invites/" + encodeURIComponent(this.inviteToken), { cache: "no-store" });
          this.invitePreview = data.invite || null;
        } catch (error) {
          this.inviteErr = error.message || "invite not found";
        }
      },

      async acceptInvite() {
        if (!this.inviteToken || !this.user) return;
        this.inviteAccepting = true;
        this.inviteErr = "";
        try {
          const data = await api("/api/team-invites/" + encodeURIComponent(this.inviteToken) + "/accept", { method: "POST" });
          this.showToast(data.team ? "Joined " + data.team.name : "Joined team");
          this.inviteToken = "";
          this.invitePreview = null;
          const url = new URL(window.location.href);
          url.searchParams.delete("invite");
          window.history.replaceState({}, "", url.pathname + url.search + url.hash);
          await this.load();
        } catch (error) {
          this.inviteErr = error.message || "join failed";
        } finally {
          this.inviteAccepting = false;
        }
      },

      dismissInvite() {
        this.inviteToken = "";
        this.invitePreview = null;
        this.inviteErr = "";
        const url = new URL(window.location.href);
        url.searchParams.delete("invite");
        window.history.replaceState({}, "", url.pathname + url.search + url.hash);
      },

      setTheme(theme) {
        this.theme = theme === "dark" ? "dark" : "light";
        document.documentElement.setAttribute("data-theme", this.theme);
        storageSet("memd-theme", this.theme);
      },

      toggleTheme() {
        this.setTheme(this.theme === "light" ? "dark" : "light");
      },

      setLayout(mode) {
        this.layoutMode = mode === "centered" ? "centered" : "wide";
        document.documentElement.setAttribute("data-layout", this.layoutMode);
        storageSet("memd-layout", this.layoutMode);
      },

      toggleLayout() {
        this.setLayout(this.layoutMode === "wide" ? "centered" : "wide");
      },

      setLogsHidden(value) {
        this.logsHidden = Boolean(value);
        storageSet("memd-logs-hidden", this.logsHidden ? "1" : "0");
      },

      setLogsWidth(width) {
        const clamped = Math.max(240, Math.min(720, Math.round(width)));
        this.logsWidth = clamped;
        document.documentElement.style.setProperty("--logs-w", clamped + "px");
        storageSet("memd-logs-w", String(clamped));
      },

      startResize(event) {
        event.preventDefault();
        const body = document.querySelector(".body");
        const startX = event.clientX;
        const startWidth = this.logsWidth;
        const move = (moveEvent) => {
          this.setLogsWidth(startWidth + (startX - moveEvent.clientX));
        };
        const up = () => {
          if (body) body.classList.remove("resizing");
          window.removeEventListener("mousemove", move);
          window.removeEventListener("mouseup", up);
        };
        if (body) body.classList.add("resizing");
        window.addEventListener("mousemove", move);
        window.addEventListener("mouseup", up);
      },

      openSheet(name) {
        if (name === "dir") {
          this.dirForm = defaultDirForm();
        }
        if (name === "conn") {
          this.connForm = defaultConnForm();
        }
        if (name === "team-new") {
          this.teamForm = defaultTeamForm();
        }
        this.sheet = name;
      },

      openDirEdit(directory) {
        this.dirEditForm = {
          id: directory.id,
          originalName: directory.name,
          name: directory.name,
          description: directory.description || "",
          err: "",
          submitting: false
        };
        this.sheet = "dir-edit";
      },

      openEdit(connector) {
        this.editForm = {
          id: connector.id,
          originalName: connector.name,
          team_id: connector.team_id || "",
          name: connector.name,
          kind: connector.kind || "mcp",
          selected: (connector.directory_ids || []).slice(),
          write: Boolean(connector.write),
          err: "",
          submitting: false
        };
        this.sheet = "edit";
      },

      async openTeam(team) {
        this.teamDetail = team;
        this.memberForm = defaultMemberForm();
        this.inviteForm = defaultInviteForm();
        this.createdInviteURL = "";
        this.sheet = "team";
        await this.loadTeamDetail(team.id);
      },

      closeSheets() {
        this.sheet = null;
        this.syncBrowserURL();
      },

      closeOverlays() {
        this.closeSheets();
        this.pickerOpen = false;
        this.closeNav();
      },

      showToast(message, level) {
        this.toast = message;
        this.toastLevel = level === "error" ? "error" : "info";
        window.clearTimeout(this.toastTimer);
        this.toastTimer = window.setTimeout(() => {
          this.toast = "";
        }, 1800);
      },

      copy(text, label) {
        copyText(text).then(() => this.showToast(label || "Copied"));
      },

      copyURL(connector) {
        this.copy(publicURL(connector.url), "URL copied");
      },

      copyAuth(connector) {
        this.copy("URL: " + tokenlessConnectorURL(connector) + "\n" + connector.auth_header, "Auth copied");
      },

      async copySkill(connector) {
        try {
          const path = connectorPath(connector);
          if (!path) {
            throw new Error("missing connector path");
          }
          const response = await fetch(path, { cache: "no-store" });
          if (!response.ok) {
            throw new Error(response.statusText || "request failed");
          }
          await copyText(await response.text());
          this.showToast("Skill copied");
        } catch (_) {
          this.showToast("Copy failed", "error");
        }
      },

      truncate(url) {
        if (!url || url.length <= 44) return url || "";
        return url.slice(0, 30) + "..." + url.slice(-10);
      },

      toggleConnectorReveal(connector) {
        connector.revealed = !connector.revealed;
      },

      toggleConnectorExpand(connector) {
        connector.expanded = !connector.expanded;
      },

      connectorInstructionURL(connector) {
        const base = tokenlessConnectorURL(connector);
        return connector.kind === "http" ? appendPath(base, "memory_load") : base;
      },

      connectorDirectoryNames(connector) {
        if (connector.directory_names) {
          return connector.directory_names;
        }
        const names = (connector.directory_ids || []).map((id) => {
          const directory = this.directories.find((d) => d.id === id);
          return directory ? directory.name : "(missing)";
        });
        return names.length ? names.join(", ") : "(none)";
      },

      toggleSelection(list, id, checked) {
        const index = list.indexOf(id);
        if (checked && index === -1) {
          list.push(id);
        } else if (!checked && index >= 0) {
          list.splice(index, 1);
        }
      },

      setConnectorTeam(form, teamID) {
        form.team_id = teamID || "";
        const allowed = new Set(this.attachableDirectories(form.team_id).map((directory) => directory.id));
        form.selected = form.selected.filter((id) => allowed.has(id));
      },

      gitDirectoryPayload() {
        return {
          remote_url: this.dirForm.remote_url,
          branch: this.dirForm.branch || "main",
          base_path: this.dirForm.base_path || "",
          author_name: this.dirForm.author_name || "memd",
          author_email: this.dirForm.author_email || "memd@localhost",
          auth_username: this.dirForm.auth_username || "",
          auth_token: this.dirForm.auth_token || "",
          ssh_key_path: this.dirForm.ssh_key_path || ""
        };
      },

      async checkGitDirectory() {
        this.dirForm.checkErr = "";
        this.dirForm.checkResults = [];
        this.dirForm.checking = true;
        try {
          const data = await api("/api/git/check", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ git: this.gitDirectoryPayload() })
          });
          this.dirForm.checkResults = data.checks || [];
          if (data.ok) {
            this.showToast("Git connection verified");
          } else {
            this.dirForm.checkErr = "Connection check failed";
          }
        } catch (error) {
          this.dirForm.checkErr = error.message || "connection check failed";
        } finally {
          this.dirForm.checking = false;
        }
      },

      async createDirectory() {
        this.dirForm.err = "";
        this.dirForm.submitting = true;
        const payload = {
          name: this.dirForm.name,
          team_id: this.dirForm.team_id || "",
          description: this.dirForm.description,
          backend: this.dirForm.backend
        };
        if (this.dirForm.backend === "local") {
          payload.local_path = this.dirForm.local_path;
        } else {
          payload.git = this.gitDirectoryPayload();
        }
        try {
          await api("/api/directories", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload)
          });
          this.closeSheets();
          await this.load();
        } catch (error) {
          this.dirForm.err = error.message || "create failed";
        } finally {
          this.dirForm.submitting = false;
        }
      },

      async updateDirectoryDetails() {
        this.dirEditForm.err = "";
        this.dirEditForm.submitting = true;
        try {
          await api("/api/directories/" + encodeURIComponent(this.dirEditForm.id), {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              name: this.dirEditForm.name,
              description: this.dirEditForm.description
            })
          });
          this.closeSheets();
          await this.load();
        } catch (error) {
          this.dirEditForm.err = error.message || "update failed";
        } finally {
          this.dirEditForm.submitting = false;
        }
      },

      async deleteDirectory(directory) {
        if (!window.confirm("Delete directory " + directory.name + "? Connectors using it will lose access.")) {
          return;
        }
        try {
          await api("/api/directories/" + encodeURIComponent(directory.id), { method: "DELETE" });
          await this.load();
        } catch (error) {
          window.alert("Could not delete directory: " + (error.message || "request failed"));
          await this.load().catch(function () {});
        }
      },

      async createConnector() {
        this.connForm.err = "";
        this.connForm.submitting = true;
        try {
          await api("/api/connectors", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              name: this.connForm.name,
              team_id: this.connForm.team_id || "",
              kind: this.connForm.kind,
              directory_ids: this.connForm.selected,
              write: this.connForm.write
            })
          });
          storageSet(this.onboardingKey(), "1");
          this.closeSheets();
          await this.load();
        } catch (error) {
          this.connForm.err = error.message || "create failed";
        } finally {
          this.connForm.submitting = false;
        }
      },

      async updateConnector() {
        this.editForm.err = "";
        this.editForm.submitting = true;
        try {
          await api("/api/connectors/" + encodeURIComponent(this.editForm.id), {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              name: this.editForm.name,
              team_id: this.editForm.team_id || "",
              kind: this.editForm.kind,
              directory_ids: this.editForm.selected,
              write: this.editForm.write
            })
          });
          this.closeSheets();
          await this.load();
        } catch (error) {
          this.editForm.err = error.message || "update failed";
        } finally {
          this.editForm.submitting = false;
        }
      },

      async rotateConnector(connector) {
        const message = "Rotate token for " + connector.name + "? The current URL stops working immediately and you will need to paste the new one into the agent.";
        if (!window.confirm(message)) {
          return;
        }
        try {
          await api("/api/connectors/" + encodeURIComponent(connector.id) + "/rotate", { method: "POST" });
          await this.load();
        } catch (error) {
          window.alert(error.message || "rotate failed");
          await this.load().catch(function () {});
        }
      },

      async deleteConnector(connector) {
        if (!window.confirm("Delete connector " + connector.name + "?")) {
          return;
        }
        try {
          await api("/api/connectors/" + encodeURIComponent(connector.id), { method: "DELETE" });
          await this.load();
        } catch (error) {
          window.alert("Could not delete connector: " + (error.message || "request failed"));
          await this.load().catch(function () {});
        }
      },

      async createTeam() {
        this.teamForm.err = "";
        this.teamForm.submitting = true;
        try {
          const data = await api("/api/teams", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              name: this.teamForm.name,
              slug: this.teamForm.slug
            })
          });
          this.closeSheets();
          await this.load();
          // Land in the new team's management sheet so the next step —
          // inviting people — is right in front of the user.
          const created = data.team && this.teams.find((team) => team.id === data.team.id);
          if (created) {
            await this.openTeam(created);
          }
        } catch (error) {
          this.teamForm.err = error.message || "create failed";
        } finally {
          this.teamForm.submitting = false;
        }
      },

      async loadTeamDetail(teamID) {
        try {
          const results = await Promise.all([
            api("/api/teams/" + encodeURIComponent(teamID) + "/members", { cache: "no-store" }),
            api("/api/teams/" + encodeURIComponent(teamID) + "/invites", { cache: "no-store" }).catch(function () { return { invites: [] }; })
          ]);
          this.teamMembers = results[0].members || [];
          this.teamInvites = results[1].invites || [];
        } catch (error) {
          this.memberForm.err = error.message || "load failed";
        }
      },

      async addTeamMember() {
        if (!this.teamDetail) return;
        this.memberForm.err = "";
        this.memberForm.submitting = true;
        try {
          await api("/api/teams/" + encodeURIComponent(this.teamDetail.id) + "/members", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              username: this.memberForm.username,
              role: this.memberForm.role
            })
          });
          this.memberForm = defaultMemberForm();
          await this.loadTeamDetail(this.teamDetail.id);
        } catch (error) {
          this.memberForm.err = error.message || "add failed";
        } finally {
          this.memberForm.submitting = false;
        }
      },

      async updateTeamMemberRole(member, role) {
        if (!this.teamDetail) return;
        try {
          await api("/api/teams/" + encodeURIComponent(this.teamDetail.id) + "/members/" + encodeURIComponent(member.user_id), {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ role: role })
          });
          await this.loadTeamDetail(this.teamDetail.id);
          await this.load();
        } catch (error) {
          window.alert(error.message || "update failed");
          await this.loadTeamDetail(this.teamDetail.id);
        }
      },

      async removeTeamMember(member) {
        if (!this.teamDetail) return;
        if (!window.confirm("Remove " + member.username + " from " + this.teamDetail.name + "?")) {
          return;
        }
        try {
          await api("/api/teams/" + encodeURIComponent(this.teamDetail.id) + "/members/" + encodeURIComponent(member.user_id), { method: "DELETE" });
          await this.loadTeamDetail(this.teamDetail.id);
          await this.load();
        } catch (error) {
          window.alert(error.message || "remove failed");
        }
      },

      async createInvite() {
        if (!this.teamDetail) return;
        this.inviteForm.err = "";
        this.inviteForm.submitting = true;
        this.createdInviteURL = "";
        let expiresAt = "";
        if (this.inviteForm.expires_at) {
          expiresAt = new Date(this.inviteForm.expires_at).toISOString();
        }
        const maxUses = parseInt(this.inviteForm.max_uses, 10);
        try {
          const data = await api("/api/teams/" + encodeURIComponent(this.teamDetail.id) + "/invites", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              role: this.inviteForm.role,
              expires_at: expiresAt,
              max_uses: Number.isFinite(maxUses) && maxUses > 0 ? maxUses : null
            })
          });
          // The server builds invite URLs against its 127.0.0.1 bind; rewrite
          // them to the public origin the user is actually browsing.
          this.createdInviteURL = publicURL(data.invite_url || "");
          this.inviteForm = defaultInviteForm();
          await this.loadTeamDetail(this.teamDetail.id);
          if (this.createdInviteURL) {
            await copyText(this.createdInviteURL);
            this.showToast("Invite copied");
          }
        } catch (error) {
          this.inviteForm.err = error.message || "invite failed";
        } finally {
          this.inviteForm.submitting = false;
        }
      },

      async revokeInvite(invite) {
        if (!this.teamDetail) return;
        try {
          await api("/api/teams/" + encodeURIComponent(this.teamDetail.id) + "/invites/" + encodeURIComponent(invite.id) + "/revoke", { method: "POST" });
          await this.loadTeamDetail(this.teamDetail.id);
        } catch (error) {
          window.alert(error.message || "revoke failed");
        }
      },

      async deleteTeam(team) {
        if (!window.confirm("Delete team " + team.name + "? Team-scoped directories and connectors become personal to their original owners.")) {
          return;
        }
        try {
          await api("/api/teams/" + encodeURIComponent(team.id), { method: "DELETE" });
          this.closeSheets();
          await this.load();
        } catch (error) {
          window.alert(error.message || "delete failed");
        }
      },

      async setDirectoryTeam(directory, teamID) {
        try {
          await api("/api/directories/" + encodeURIComponent(directory.id), {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ team_id: teamID || "" })
          });
          await this.load();
        } catch (error) {
          window.alert(error.message || "update failed");
          await this.load();
        }
      },

      async setDirectoryFeature(directory, key, enabled) {
        try {
          await api("/api/directories/" + encodeURIComponent(directory.id), {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ feature: { key: key, enabled: enabled } })
          });
          await this.load();
        } catch (error) {
          window.alert(error.message || "update failed");
          await this.load();
        }
      },

      // Your own connectors that have this directory attached — the candidates
      // for the directory's main-branch connector.
      ownConnectorsForDirectory(directory) {
        return this.connectors.filter((connector) => connector.owned && (connector.directory_ids || []).includes(directory.id));
      },

      async setDirectoryOwnerConnector(directory, connectorID) {
        try {
          await api("/api/directories/" + encodeURIComponent(directory.id), {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ owner_connector_id: connectorID || "" })
          });
          await this.load();
        } catch (error) {
          window.alert(error.message || "update failed");
          await this.load();
        }
      },

      async exportUserData() {
        try {
          const bundle = await api("/api/data", { cache: "no-store" });
          downloadJSON("memd-user-data-" + this.user.username + ".json", bundle);
        } catch (error) {
          window.alert(error.message || "export failed");
        }
      },

      openImportUserData() {
        const input = this.$refs.userDataImport;
        if (input) {
          input.value = "";
          input.click();
        }
      },

      async importUserData(event) {
        const file = event.target.files && event.target.files[0];
        if (!file) {
          return;
        }
        const replace = window.confirm("Replace your existing directories and connectors? Choose Cancel to merge/update by id.");
        try {
          const text = await readFileText(file);
          const bundle = JSON.parse(text);
          const suffix = replace ? "?replace=1" : "";
          await api("/api/data" + suffix, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(bundle)
          });
          this.showToast("Imported data");
          await this.load();
        } catch (error) {
          window.alert(error.message || "import failed");
        }
      },

      // --- Tasks dashboard (aggregate, cross-directory) ---

      tasksEnabled(directory) {
        return (directory.features || []).some((f) => f.key === "tasks" && f.enabled);
      },

      // Open the Tasks view filtered to one directory (from a directory card).
      async openTasks(directory) {
        this.tasksFilter = directory ? directory.id : "";
        this.setView("tasks");
      },

      // Open the Tasks view showing every directory (from the sidenav).
      async openTasksView() {
        this.tasksFilter = "";
        this.setView("tasks");
      },

      async loadTasksAll() {
        this.tasksLoading = true;
        this.tasksErr = "";
        try {
          const data = await api("/api/tasks", { cache: "no-store" });
          this.tasksAll = data.directories || [];
          this.tasksLoaded = true;
        } catch (error) {
          this.tasksErr = error.message || "failed to load tasks";
        } finally {
          this.tasksLoading = false;
        }
      },

      setTasksFilter(id) {
        this.tasksFilter = id || "";
        this.syncTasksURL();
      },

      // Directory groups to render, honouring the current filter.
      tasksGroups() {
        if (!this.tasksFilter) return this.tasksAll;
        return this.tasksAll.filter((g) => g.id === this.tasksFilter);
      },

      tasksOpenCount() {
        return this.tasksGroups().reduce((sum, g) => sum + this.groupOpen(g), 0);
      },

      groupOpen(group) {
        return (group.lists || []).reduce((sum, l) => sum + (l.open || 0), 0);
      },

      tasksDirURL(dirID) {
        return "/api/directories/" + encodeURIComponent(dirID) + "/tasks";
      },

      async toggleTask(group, task) {
        try {
          await api(this.tasksDirURL(group.id), {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ action: "toggle", file: task.file, line: task.line, expect: task.raw })
          });
          await this.loadTasksAll();
        } catch (error) {
          this.showToast(error.message || "toggle failed", "error");
          await this.loadTasksAll();
        }
      },

      async addTask(group, file, event) {
        const input = event.target.querySelector('input[type="text"]');
        const title = input ? input.value.trim() : "";
        if (!title) return;
        try {
          await api(this.tasksDirURL(group.id), {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ action: "add", file: file, title: title })
          });
          if (input) input.value = "";
          await this.loadTasksAll();
        } catch (error) {
          this.showToast(error.message || "add failed", "error");
        }
      },

      async addList(group, event) {
        const input = event.target.querySelector('input[type="text"]');
        const name = input ? input.value.trim() : "";
        if (!name) return;
        const title = window.prompt("First task for “" + name + "”:");
        if (title === null || !title.trim()) return;
        try {
          await api(this.tasksDirURL(group.id), {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ action: "add", list_name: name, title: title.trim() })
          });
          if (input) input.value = "";
          await this.loadTasksAll();
        } catch (error) {
          this.showToast(error.message || "create list failed", "error");
        }
      },

      boardBuckets(board) {
        const b = board || {};
        return [
          { key: "overdue", label: "Overdue", items: b.overdue || [] },
          { key: "due_soon", label: "Due this week", items: b.due_soon || [] },
          { key: "later", label: "Later", items: b.later || [] },
          { key: "no_date", label: "No date", items: b.no_date || [] }
        ].filter((bucket) => bucket.items.length);
      },

      toggleHideDone() {
        storageSet("memd-tasks-hide-done", this.tasksHideDone ? "1" : "0");
      },

      // Tasks to render for a list, dropping completed ones (and completed
      // subtasks) when "Hide completed" is on. The files are untouched — this is
      // display-only.
      visibleTasks(list) {
        if (!this.tasksHideDone) return list.tasks;
        return (list.tasks || [])
          .filter((t) => !t.done)
          .map((t) => ({ ...t, subtasks: (t.subtasks || []).filter((s) => !s.done) }));
      },

      listName(group, file) {
        const list = (group.lists || []).find((l) => l.file === file);
        if (list) return list.name;
        const base = (file || "").split("/").pop() || "";
        return base.replace(/\.md$/i, "");
      },

      formatDue(due) {
        if (!due) return "";
        const parts = due.split("-");
        if (parts.length !== 3) return due;
        const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
        const m = parseInt(parts[1], 10) - 1;
        if (m < 0 || m > 11) return due;
        return months[m] + " " + parseInt(parts[2], 10);
      },

      dueClass(due) {
        if (!due) return "";
        const today = new Date();
        const t = new Date(today.getFullYear(), today.getMonth(), today.getDate());
        const d = new Date(due + "T00:00:00");
        if (isNaN(d.getTime())) return "";
        const days = Math.round((d - t) / 86400000);
        if (days < 0) return "overdue";
        if (days <= 7) return "soon";
        return "later";
      },

      // Resolve a promoted-task link (relative to the list's folder) to its raw
      // file URL so it opens in a new tab.
      rawFileURLFor(dirID, listFile, link) {
        const dir = listFile.includes("/") ? listFile.slice(0, listFile.lastIndexOf("/")) : "";
        const target = dir ? dir + "/" + link : link;
        return "/api/directories/" + encodeURIComponent(dirID) + "/raw?path=" + encodeURIComponent(target);
      },

      async openBrowser(directory) {
        this.browserDir = directory;
        this.browserPath = "";
        this.browserEntries = [];
        this.browserErr = "";
        this.browserFile = null;
        this.browserFileContent = "";
        this.browserFileHTML = "";
        this.browserFileErr = "";
        this.sheet = "browse";
        await this.browseFiles("");
      },

      async browseFiles(path) {
        if (!this.browserDir) return;
        this.browserFile = null;
        this.browserFileContent = "";
        this.browserFileHTML = "";
        this.browserFileErr = "";
        this.browserLoading = true;
        this.browserErr = "";
        try {
          const suffix = path ? "?path=" + encodeURIComponent(path) : "";
          const data = await api("/api/directories/" + encodeURIComponent(this.browserDir.id) + "/files" + suffix);
          this.browserPath = data.path || "";
          this.browserEntries = data.entries || [];
          this.syncBrowserURL();
        } catch (error) {
          this.browserErr = error.message || "listing failed";
        } finally {
          this.browserLoading = false;
        }
      },

      // The browse sheet mirrors its state into the URL hash
      // (#browse=<dir>&path=<folder> or #browse=<dir>&file=<path>) so folder
      // navigation builds history entries and back/forward walks folders
      // instead of leaving the app.
      browserHash() {
        if (this.sheet !== "browse" || !this.browserDir) return "";
        const params = new URLSearchParams();
        params.set("browse", this.browserDir.id);
        if (this.browserFile) {
          params.set("file", this.browserFile.path);
        } else if (this.browserPath) {
          params.set("path", this.browserPath);
        }
        return "#" + params.toString();
      },

      // Restore app state from the URL hash (on load and on back/forward).
      async applyBrowserRoute() {
        if (!this.user || this.routeApplying) return;
        const params = new URLSearchParams(window.location.hash.replace(/^#/, ""));
        this.routeApplying = true;
        try {
          // Tasks view: #tasks=<dirID|all>
          if (params.has("tasks")) {
            if (this.sheet === "browse") this.closeSheets();
            const f = params.get("tasks");
            this.activeView = "tasks";
            this.normalizeView();
            this.tasksFilter = f && f !== "all" ? f : "";
            await this.loadTasksAll();
            return;
          }
          // File browser sheet: #browse=<dir>&path|file=…
          if (params.has("browse")) {
            const dirID = params.get("browse");
            const directory = this.directories.find((d) => d.id === dirID);
            if (!directory) {
              window.history.replaceState({}, "", window.location.pathname + window.location.search);
              if (this.sheet === "browse") this.closeSheets();
              return;
            }
            this.activeView = "directories";
            this.normalizeView();
            const file = params.get("file") || "";
            const path = file ? parentPath(file) : params.get("path") || "";
            this.browserDir = directory;
            this.sheet = "browse";
            await this.browseFiles(path);
            if (file) {
              await this.openBrowserFile({ path: file, name: baseName(file) });
            }
            return;
          }
          // Any other main view: #view=<name>
          if (this.sheet === "browse") this.closeSheets();
          if (params.has("view")) {
            this.activeView = params.get("view");
            this.normalizeView();
            if (this.activeView === "tasks" && !this.tasksLoaded) {
              await this.loadTasksAll();
            }
          }
        } finally {
          this.routeApplying = false;
        }
      },

      browserCrumbs() {
        const crumbs = [{ label: this.browserDir ? this.browserDir.name : "root", path: "" }];
        if (!this.browserPath) return crumbs;
        let acc = "";
        this.browserPath.split("/").filter(Boolean).forEach(function (part) {
          acc = acc ? acc + "/" + part : part;
          crumbs.push({ label: part, path: acc });
        });
        return crumbs;
      },

      browserParent() {
        if (this.browserFile) return this.browserPath;
        if (!this.browserPath) return null;
        const index = this.browserPath.lastIndexOf("/");
        return index === -1 ? "" : this.browserPath.slice(0, index);
      },

      rawFileURL(path) {
        if (!this.browserDir) return "";
        return "/api/directories/" + encodeURIComponent(this.browserDir.id) + "/raw?path=" + encodeURIComponent(path);
      },

      isImagePath(path) {
        return /\.(png|jpe?g|gif|webp)$/i.test(path || "");
      },

      isPDFPath(path) {
        return /\.pdf$/i.test(path || "");
      },

      isMarkdownPath(path) {
        return /\.(md|markdown)$/i.test(path || "");
      },

      isRenderablePath(path) {
        return /\.(html?|svg)$/i.test(path || "");
      },

      // Open-in-tab target: HTML/SVG get the rendered view — served as real
      // markup but under a sandbox CSP, so the document has an opaque origin
      // with scripts and all network loads disabled and can never act as the
      // signed-in user. Everything else opens as plain text.
      openFileURL(path) {
        return this.isRenderablePath(path) ? this.rawFileURL(path) + "&render=1" : this.rawFileURL(path);
      },

      downloadFileURL(path) {
        return this.rawFileURL(path) + "&download=1";
      },

      async openBrowserFile(entry) {
        this.browserFile = {
          path: entry.path,
          name: entry.name,
          isImage: this.isImagePath(entry.name),
          isPDF: this.isPDFPath(entry.name),
          isMarkdown: this.isMarkdownPath(entry.name)
        };
        this.browserFileContent = "";
        this.browserFileHTML = "";
        this.browserFileErr = "";
        this.syncBrowserURL();
        if (this.browserFile.isImage || this.browserFile.isPDF) {
          return;
        }
        this.browserFileLoading = true;
        try {
          const response = await fetch(this.rawFileURL(entry.path), { cache: "no-store" });
          if (!response.ok) {
            const payload = await response.json().catch(function () { return {}; });
            throw new Error(payload.error || response.statusText || "read failed");
          }
          this.browserFileContent = await response.text();
          if (this.browserFile.isMarkdown) {
            this.browserFileHTML = renderMarkdownFile(this.browserFile.path, this.browserFileContent, (p) => this.rawFileURL(p));
          }
        } catch (error) {
          this.browserFileErr = error.message || "read failed";
        } finally {
          this.browserFileLoading = false;
        }
      },

      // Clicks on relative links inside rendered markdown stay inside the
      // browser: extension-less targets are treated as folders, everything
      // else opens in the file viewer.
      markdownClick(event) {
        const anchor = event.target && event.target.closest ? event.target.closest("a[data-rel]") : null;
        if (!anchor) return;
        event.preventDefault();
        this.openMarkdownLink(anchor.getAttribute("data-rel") || "");
      },

      async openMarkdownLink(rel) {
        if (!rel) return;
        const name = baseName(rel);
        if (name.indexOf(".") === -1) {
          await this.browseFiles(rel);
          return;
        }
        if (parentPath(rel) !== this.browserPath) {
          await this.browseFiles(parentPath(rel));
        }
        await this.openBrowserFile({ path: rel, name: name });
      },

      closeBrowserFile() {
        this.browserFile = null;
        this.browserFileContent = "";
        this.browserFileHTML = "";
        this.browserFileErr = "";
        this.syncBrowserURL();
      },

      copyBrowserFile() {
        this.copy(this.browserFileContent, "File copied");
      },

      async openPicker() {
        this.pickerOpen = true;
        await this.browse("");
      },

      async browse(path) {
        try {
          const suffix = path ? "?path=" + encodeURIComponent(path) : "";
          const data = await api("/api/browse" + suffix);
          this.pickerPath = data.path;
          this.pickerEntries = data.dirs || [];
          this.pickerParent = data.parent || "";
          this.pickerErr = "";
        } catch (error) {
          this.pickerErr = error.message || String(error);
        }
      },

      joinPath(base, name) {
        if (!base || base === "/") {
          return "/" + name;
        }
        return base + "/" + name;
      },

      selectFolder() {
        this.dirForm.local_path = this.pickerPath;
        this.pickerOpen = false;
      },

      async pollLogs() {
        if (!this.user) {
          return;
        }
        if (this.logsPolling) {
          return;
        }
        this.logsPolling = true;
        try {
          const data = await api("/api/logs?since=" + encodeURIComponent(String(this.lastID)), { cache: "no-store" });
          const fresh = data.entries || [];
          if (!fresh.length) return;
          fresh.forEach(function (entry) {
            entry.timeLocal = new Date(entry.time).toLocaleTimeString();
          });
          this.entries = this.entries.concat(fresh).slice(-200);
          this.lastID = fresh[fresh.length - 1].id;
          this.$nextTick(() => {
            document.querySelectorAll(".logs-list").forEach(function (el) {
              if (el.offsetParent !== null) {
                el.scrollTop = el.scrollHeight;
              }
            });
          });
        } catch (_) {
        } finally {
          this.logsPolling = false;
        }
      }
    };
  };
})();

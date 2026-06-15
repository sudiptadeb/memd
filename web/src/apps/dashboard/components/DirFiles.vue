<template>
  <aside
    class="sheet wide-sheet browse-sheet"
    :class="open ? 'open' : ''"
    :aria-hidden="!open"
    :inert="!open"
    @keydown.escape.stop="emit('close')"
  >
    <header class="sheet-head">
      <div>
        <h3>{{ directory ? directory.name : "Directory" }}</h3>
        <div class="sub">Browse the files this directory serves to agents.</div>
      </div>
      <span class="spacer"></span>
      <button class="icon-btn" type="button" @click="emit('close')" title="Close">
        <MIcon name="x" />
      </button>
    </header>

    <nav class="browse-crumbs" aria-label="Current path">
      <span v-for="(crumb, index) in crumbs" :key="crumb.path" class="crumb-wrap">
        <MIcon v-if="index > 0" name="chevron-right" class="crumb-sep" />
        <button
          class="crumb"
          type="button"
          :class="!file && index === crumbs.length - 1 ? 'current' : ''"
          @click="browseFiles(crumb.path)"
        >
          {{ crumb.label }}
        </button>
      </span>
      <span class="crumb-wrap" v-if="file">
        <MIcon name="chevron-right" class="crumb-sep" />
        <span class="crumb current">{{ file.name }}</span>
      </span>
    </nav>

    <div class="sheet-body browse-body">
      <div class="browse-listing" v-if="!file">
        <div class="picker-error" v-if="listErr">{{ listErr }}</div>
        <div class="dir-rows browse-rows">
          <div class="dir-row up" v-if="parent !== null" @click="browseFiles(parent ?? '')">
            <MIcon name="corner-left-up" />
            .. (up)
          </div>
          <div
            v-for="entry in entries"
            :key="entry.path"
            class="dir-row"
            role="button"
            tabindex="0"
            @click="entry.is_dir ? browseFiles(entry.path) : openFile(entry)"
            @keydown.enter.prevent="entry.is_dir ? browseFiles(entry.path) : openFile(entry)"
          >
            <MIcon :name="entry.is_dir ? 'folder' : 'file'" />
            <span class="dir-row-name">{{ entry.name }}</span>
            <span class="spacer"></span>
            <a
              v-if="!entry.is_dir"
              class="icon-btn row-open"
              :href="openFileURL(entry.path)"
              target="_blank"
              rel="noopener"
              title="Open in new tab"
              @click.stop
            >
              <MIcon name="external-link" />
            </a>
            <MIcon v-if="entry.is_dir" name="chevron-right" class="arrow" />
          </div>
          <div class="picker-empty" v-if="!listLoading && !entries.length && !listErr">(empty folder)</div>
        </div>
      </div>

      <div class="file-view" v-if="file">
        <div class="file-toolbar">
          <button class="icon-btn" type="button" @click="closeFile" title="Back to files" aria-label="Back to files">
            <MIcon name="corner-left-up" />
          </button>
          <span class="spacer"></span>
          <button
            class="icon-btn"
            type="button"
            v-if="!file.isImage && !file.isPDF && fileContent"
            @click="copyFile"
            title="Copy contents"
            aria-label="Copy contents"
          >
            <MIcon name="copy" />
          </button>
          <a
            class="icon-btn"
            :href="openFileURL(file.path)"
            target="_blank"
            rel="noopener"
            :title="
              isRenderablePath(file.path)
                ? 'Open in new tab (rendered in a sandbox: no scripts, no access to your session)'
                : 'Open in new tab'
            "
            aria-label="Open in new tab"
          >
            <MIcon name="external-link" />
          </a>
          <a
            class="icon-btn"
            :href="downloadFileURL(file.path)"
            :download="file.name"
            title="Download"
            aria-label="Download"
          >
            <MIcon name="download" />
          </a>
        </div>
        <div class="picker-error" v-if="fileErr">{{ fileErr }}</div>
        <div class="file-loading" v-if="fileLoading">Loading file...</div>
        <img class="file-image" v-if="file.isImage" :src="rawFileURL(file.path)" :alt="file.name" />
        <div class="picker-empty" v-if="file.isPDF">
          PDF files render best in their own tab — use "Open in new tab".
        </div>
        <!-- eslint-disable-next-line vue/no-v-html -->
        <div
          class="file-markdown"
          v-if="file.isMarkdown && !fileLoading && !fileErr"
          v-html="fileHTML"
          @click="onMarkdownClick"
        ></div>
        <pre
          class="file-pre"
          v-if="!file.isImage && !file.isPDF && !file.isMarkdown && !fileLoading && !fileErr"
          >{{ fileContent }}</pre
        >
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { ref, watch } from "vue";
import MIcon from "@/shared/components/MIcon.vue";
import { directories, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import { copyToClipboard } from "@/shared/utils";
import {
  baseName,
  isImagePath,
  isMarkdownPath,
  isPdfPath,
  isRenderablePath,
  parentPath,
  renderMarkdownFile,
} from "./dirMarkdown";
import type { DirEntry, DirectoryView } from "@/shared/types";

// The directory file browser: a folder tree plus an in-sheet viewer for images,
// PDFs (link-out), Markdown (sandbox-rendered), and plain text.
const props = defineProps<{ open: boolean; directory: DirectoryView | null }>();
const emit = defineEmits<{ (e: "close"): void }>();

interface OpenFile {
  path: string;
  name: string;
  isImage: boolean;
  isPDF: boolean;
  isMarkdown: boolean;
}

const dirPath = ref("");
const entries = ref<DirEntry[]>([]);
const listErr = ref("");
const listLoading = ref(false);

const file = ref<OpenFile | null>(null);
const fileContent = ref("");
const fileHTML = ref("");
const fileErr = ref("");
const fileLoading = ref(false);

const crumbs = ref<{ label: string; path: string }[]>([]);
const parent = ref<string | null>(null);

function rawFileURL(path: string): string {
  if (!props.directory) return "";
  return directories.rawUrl(props.directory.id, path);
}

// HTML/SVG open as a sandbox-rendered document; everything else as plain text.
function openFileURL(path: string): string {
  if (!props.directory) return "";
  return directories.rawUrl(props.directory.id, path, isRenderablePath(path) ? { render: true } : {});
}

function downloadFileURL(path: string): string {
  if (!props.directory) return "";
  return directories.rawUrl(props.directory.id, path, { download: true });
}

function recomputeCrumbs(): void {
  const list = [{ label: props.directory ? props.directory.name : "root", path: "" }];
  if (dirPath.value) {
    let acc = "";
    dirPath.value
      .split("/")
      .filter(Boolean)
      .forEach((part) => {
        acc = acc ? acc + "/" + part : part;
        list.push({ label: part, path: acc });
      });
  }
  crumbs.value = list;
}

function recomputeParent(): void {
  if (file.value) {
    parent.value = dirPath.value;
    return;
  }
  if (!dirPath.value) {
    parent.value = null;
    return;
  }
  const index = dirPath.value.lastIndexOf("/");
  parent.value = index === -1 ? "" : dirPath.value.slice(0, index);
}

async function browseFiles(path: string): Promise<void> {
  if (!props.directory) return;
  file.value = null;
  fileContent.value = "";
  fileHTML.value = "";
  fileErr.value = "";
  listLoading.value = true;
  listErr.value = "";
  try {
    const data = await directories.files(props.directory.id, path);
    dirPath.value = data.path || "";
    entries.value = data.entries || [];
  } catch (e) {
    listErr.value = e instanceof ApiError ? e.message : String(e);
  } finally {
    listLoading.value = false;
    recomputeCrumbs();
    recomputeParent();
  }
}

async function openFile(entry: { path: string; name: string }): Promise<void> {
  if (!props.directory) return;
  const dirId = props.directory.id;
  const current: OpenFile = {
    path: entry.path,
    name: entry.name,
    isImage: isImagePath(entry.name),
    isPDF: isPdfPath(entry.name),
    isMarkdown: isMarkdownPath(entry.name),
  };
  file.value = current;
  fileContent.value = "";
  fileHTML.value = "";
  fileErr.value = "";
  recomputeCrumbs();
  recomputeParent();
  if (current.isImage || current.isPDF) {
    return;
  }
  fileLoading.value = true;
  try {
    const text = await directories.raw(dirId, current.path);
    fileContent.value = text;
    if (current.isMarkdown) {
      fileHTML.value = renderMarkdownFile(current.path, text, (p) => directories.rawUrl(dirId, p));
    }
  } catch (e) {
    fileErr.value = e instanceof ApiError ? e.message : String(e);
  } finally {
    fileLoading.value = false;
  }
}

function closeFile(): void {
  file.value = null;
  fileContent.value = "";
  fileHTML.value = "";
  fileErr.value = "";
  recomputeCrumbs();
  recomputeParent();
}

async function copyFile(): Promise<void> {
  const ok = await copyToClipboard(fileContent.value);
  toast(ok ? "File copied" : "Copy failed", ok ? "success" : "error");
}

// Clicks on relative links inside rendered markdown stay inside the browser:
// extension-less targets are treated as folders, everything else opens here.
function onMarkdownClick(event: MouseEvent): void {
  const target = event.target as HTMLElement | null;
  const anchor = target && target.closest ? (target.closest("a[data-rel]") as HTMLElement | null) : null;
  if (!anchor) return;
  event.preventDefault();
  void openMarkdownLink(anchor.getAttribute("data-rel") || "");
}

async function openMarkdownLink(rel: string): Promise<void> {
  if (!rel) return;
  const name = baseName(rel);
  if (name.indexOf(".") === -1) {
    await browseFiles(rel);
    return;
  }
  if (parentPath(rel) !== dirPath.value) {
    await browseFiles(parentPath(rel));
  }
  await openFile({ path: rel, name });
}

// Load the directory root whenever the sheet opens for a directory.
watch(
  () => [props.open, props.directory?.id] as const,
  ([isOpen]) => {
    if (isOpen && props.directory) {
      dirPath.value = "";
      entries.value = [];
      listErr.value = "";
      file.value = null;
      fileContent.value = "";
      fileHTML.value = "";
      fileErr.value = "";
      recomputeCrumbs();
      recomputeParent();
      void browseFiles("");
    }
  },
);
</script>

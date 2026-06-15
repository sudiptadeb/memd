import { ref, watch } from "vue";

// Theme + layout-width preferences, persisted to localStorage and reflected on
// <html> via data-attributes the ported style.css already keys off. Module-level
// singletons so every component shares one source of truth.

export type Theme = "light" | "dark";
export type Layout = "wide" | "centered";

const THEME_KEY = "memd-theme";
const LAYOUT_KEY = "memd-layout";

function initialTheme(): Theme {
  const saved = localStorage.getItem(THEME_KEY);
  if (saved === "light" || saved === "dark") return saved;
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function initialLayout(): Layout {
  const saved = localStorage.getItem(LAYOUT_KEY);
  return saved === "centered" ? "centered" : "wide";
}

const theme = ref<Theme>(initialTheme());
const layout = ref<Layout>(initialLayout());

function applyTheme(t: Theme): void {
  document.documentElement.setAttribute("data-theme", t);
}
function applyLayout(l: Layout): void {
  document.documentElement.setAttribute("data-layout", l);
}

applyTheme(theme.value);
applyLayout(layout.value);

watch(theme, (t) => {
  applyTheme(t);
  localStorage.setItem(THEME_KEY, t);
});
watch(layout, (l) => {
  applyLayout(l);
  localStorage.setItem(LAYOUT_KEY, l);
});

export function useTheme() {
  return {
    theme,
    layout,
    toggleTheme(): void {
      theme.value = theme.value === "light" ? "dark" : "light";
    },
    toggleLayout(): void {
      layout.value = layout.value === "wide" ? "centered" : "wide";
    },
  };
}

import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import { fileURLToPath, URL } from "node:url";
import { resolve } from "node:path";

// One project, two independently-built apps. `VITE_APP` selects the app (set by
// build/build.sh for each app; defaults to the dashboard for `npm run dev`).
// Each app is rooted at its own folder so its index.html is the entry, served at
// its own base path, and emits a self-contained bundle into
// server/internal/ui/dist/<app>. Dev runs one app per process at the same base
// path it has in production, so history-mode routes match dev and prod.

const baseFor = (app: string): string => (app === "dashboard" ? "/" : `/${app}/`);

export default defineConfig(() => {
  const app = process.env.VITE_APP || "dashboard";
  const appsDir = fileURLToPath(new URL("./src/apps", import.meta.url));

  return {
    root: resolve(appsDir, app),
    base: baseFor(app),
    plugins: [vue()],
    resolve: {
      alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
    },
    build: {
      outDir: fileURLToPath(
        new URL(`./../server/internal/ui/dist/${app}`, import.meta.url),
      ),
      emptyOutDir: true,
    },
    server: {
      proxy: {
        "/api": { target: "http://127.0.0.1:7878", changeOrigin: true },
        "/auth": { target: "http://127.0.0.1:7878", changeOrigin: true },
        "/http": { target: "http://127.0.0.1:7878", changeOrigin: true },
      },
    },
  };
});

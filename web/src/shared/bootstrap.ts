import { createApp, type Component } from "vue";
import {
  createRouter,
  createWebHashHistory,
  type RouteRecordRaw,
  type Router,
} from "vue-router";
import "@/shared/css/style.css";

// The shared app template: every app's main.ts calls this with its root component
// and routes. Centralising the wiring here is the seam for upgrading all apps at
// once (global plugins, error handlers, directives) without touching each app.

export function createMemdApp(root: Component, routes: RouteRecordRaw[]): {
  app: ReturnType<typeof createApp>;
  router: Router;
} {
  // Hash history: client routes live after "#", so deep links never reach the
  // Go server (no SPA fallback needed) and work under any static mount. The
  // per-app Vite base (BASE_URL) — "/" for the dashboard, "/admin/" for admin —
  // is the path that precedes the "#".
  const router = createRouter({
    history: createWebHashHistory(import.meta.env.BASE_URL),
    routes,
    scrollBehavior: () => ({ top: 0 }),
  });

  const app = createApp(root);
  app.use(router);
  app.mount("#app");

  return { app, router };
}

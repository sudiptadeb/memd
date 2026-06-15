import { createApp, type Component } from "vue";
import {
  createRouter,
  createWebHistory,
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
  // history base comes from Vite's per-app base (BASE_URL): "/" for the
  // dashboard, "/admin/" for the admin app, so routes are clean and #-free.
  const router = createRouter({
    history: createWebHistory(import.meta.env.BASE_URL),
    routes,
    scrollBehavior: () => ({ top: 0 }),
  });

  const app = createApp(root);
  app.use(router);
  app.mount("#app");

  return { app, router };
}

import type { RouteRecordRaw } from "vue-router";

export const routes: RouteRecordRaw[] = [
  { path: "/", redirect: "/users" },
  { path: "/users", name: "users", component: () => import("./pages/Users.vue") },
  { path: "/sso", name: "sso", component: () => import("./pages/SSO.vue") },
  { path: "/doctrines", name: "doctrines", component: () => import("./pages/Doctrines.vue") },
  { path: "/:pathMatch(.*)*", redirect: "/users" },
];

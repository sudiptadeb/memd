import type { RouteRecordRaw } from "vue-router";

export const routes: RouteRecordRaw[] = [
  { path: "/", redirect: "/directories" },
  { path: "/info", name: "info", component: () => import("./pages/Info.vue") },
  { path: "/teams", name: "teams", component: () => import("./pages/Teams.vue") },
  { path: "/teams/:teamId", name: "team", component: () => import("./pages/TeamDetail.vue") },
  { path: "/directories", name: "directories", component: () => import("./pages/Directories.vue") },
  { path: "/directories/:dirId", name: "directory", component: () => import("./pages/DirDetail.vue") },
  { path: "/directories/:dirId/graph", name: "directory-graph", component: () => import("./pages/DirGraph.vue") },
  { path: "/tasks", name: "tasks", component: () => import("./pages/Tasks.vue") },
  { path: "/connectors", name: "connectors", component: () => import("./pages/Connectors.vue") },
  { path: "/activity", name: "activity", component: () => import("./pages/Activity.vue") },
  { path: "/invite/:token", name: "invite", component: () => import("./pages/InviteAccept.vue") },
  { path: "/:pathMatch(.*)*", redirect: "/directories" },
];

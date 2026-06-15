import App from "./App.vue";
import { routes } from "./router";
import { createMemdApp } from "@/shared/bootstrap";

createMemdApp(App, routes);

import { createApp } from "vue";
import App from "./App.vue";
import "./synckosh.css";

// /synckosh: a page of its own for Project B's sync buckets. It signs in with
// memd's accounts and uses memd's sync bucket API, but none of the dashboard's
// look or routes, so people who come from Project B's homepage see only sync.
createApp(App).mount("#app");

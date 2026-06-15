import mitt from "mitt";

// The event bus carries ephemeral, cross-cutting UI signals (toasts, a full-page
// loader) that any component can raise and the app shell renders. Durable state
// lives in composables; this is deliberately just for fire-and-forget signals.

export type ToastKind = "info" | "success" | "error";

export type BusEvents = {
  toast: { message: string; kind?: ToastKind; duration?: number };
  loading: boolean;
};

export const bus = mitt<BusEvents>();

export function toast(message: string, kind: ToastKind = "info", duration = 4000): void {
  bus.emit("toast", { message, kind, duration });
}

export function setLoading(active: boolean): void {
  bus.emit("loading", active);
}

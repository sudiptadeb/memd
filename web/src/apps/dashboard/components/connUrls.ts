// Connector URL helpers, ported from the Alpine build (script.js). The backend
// may return a relative `connector.url`; agents need an absolute one. These
// resolve everything against the current origin and derive the tokenless
// "instruction" forms used in the connector card.

import type { ConnectorView } from "@/shared/types";

function currentBaseURL(): string {
  return window.location.origin;
}

// Absolute URL for a possibly-relative connector URL, preserving query + hash.
export function publicURL(rawURL: string): string {
  try {
    const url = new URL(rawURL, currentBaseURL());
    return currentBaseURL() + url.pathname + url.search + url.hash;
  } catch {
    return rawURL || "";
  }
}

// Path-only form of a connector URL (used to fetch the HTTP "skill").
export function publicPath(rawURL: string): string {
  try {
    return new URL(rawURL, currentBaseURL()).pathname;
  } catch {
    return "";
  }
}

export function appendPath(baseURL: string, path: string): string {
  return publicURL(baseURL).replace(/\/+$/, "") + "/" + path.replace(/^\/+/, "");
}

// The shareable URL with no secret in it: the connector's auth_url if present,
// otherwise the transport root (/mcp or /http).
export function tokenlessConnectorURL(connector: ConnectorView): string {
  if (connector.auth_url) {
    return publicURL(connector.auth_url);
  }
  try {
    const url = new URL(connector.url, currentBaseURL());
    const segments = url.pathname.split("/").filter(Boolean);
    if (segments[0] === "mcp" || segments[0] === "http") {
      return currentBaseURL() + "/" + segments[0];
    }
  } catch {
    // fall through
  }
  return publicURL(connector.url);
}

export function connectorPath(connector: ConnectorView): string {
  return publicPath(connector.url);
}

// The URL to show in the instructions block. HTTP connectors point at the
// memory_load entry point; MCP connectors show the tokenless transport root.
export function connectorInstructionURL(connector: ConnectorView): string {
  const base = tokenlessConnectorURL(connector);
  return connector.kind === "http" ? appendPath(base, "memory_load") : base;
}

// Collapse a long URL for the masked display.
export function truncate(url: string): string {
  if (!url || url.length <= 44) return url || "";
  return url.slice(0, 30) + "..." + url.slice(-10);
}

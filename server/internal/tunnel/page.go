package tunnel

import (
	"fmt"
	"net/http"
)

// The /rc management page is deliberately not part of memd's Vue build: it is
// a small self-contained server-rendered page with no npm dependency. memd's
// global Content-Security-Policy allows same-origin scripts but NOT inline
// ones (script-src 'self' 'unsafe-eval'), so the page's JavaScript is served
// as a separate same-origin route (/rc/app.js) instead of an inline <script>.
// Inline CSS is fine (style-src includes 'unsafe-inline').

const rcPageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>memd · remote terminal</title>
<style>
  :root {
    --bg: #f6f7f9; --panel: #ffffff; --ink: #1c2330; --muted: #66707f;
    --accent: #3556c4; --ok: #1e7a46; --line: #dfe3ea; --code-bg: #10151f;
    --code-ink: #d8e0ee;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; background: var(--bg); color: var(--ink);
    font: 15px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif;
  }
  main { max-width: 780px; margin: 0 auto; padding: 32px 20px 64px; }
  h1 { font-size: 22px; margin: 0 0 4px; }
  h2 { font-size: 16px; margin: 0 0 12px; }
  .sub { color: var(--muted); margin: 0 0 28px; }
  .card {
    background: var(--panel); border: 1px solid var(--line); border-radius: 10px;
    padding: 20px; margin-bottom: 20px;
  }
  .agents-empty { color: var(--muted); }
  ul.agents { list-style: none; margin: 0; padding: 0; }
  ul.agents li {
    display: flex; flex-wrap: wrap; gap: 6px 14px; align-items: baseline;
    padding: 10px 0; border-top: 1px solid var(--line);
  }
  ul.agents li:first-child { border-top: 0; }
  .agent-label { font-weight: 600; }
  .agent-id { color: var(--muted); font-family: ui-monospace, monospace; font-size: 13px; }
  .agent-tunnels { color: var(--ok); font-weight: 600; }
  .agent-meta { color: var(--muted); font-size: 13px; }
  form.mint { display: flex; flex-wrap: wrap; gap: 10px; align-items: flex-end; }
  form.mint label { display: block; font-size: 13px; color: var(--muted); margin-bottom: 4px; }
  form.mint input {
    font: inherit; padding: 7px 10px; border: 1px solid var(--line);
    border-radius: 7px; background: var(--panel); color: var(--ink);
  }
  form.mint input[name=ttl] { width: 90px; }
  button {
    font: inherit; font-weight: 600; padding: 8px 16px; border: 0;
    border-radius: 7px; background: var(--accent); color: #fff; cursor: pointer;
  }
  button.copy { background: transparent; color: var(--accent); padding: 2px 8px; }
  .result { display: none; margin-top: 18px; }
  .result.show { display: block; }
  .once { color: var(--muted); font-size: 13px; margin: 4px 0 10px; }
  pre.tok {
    background: var(--code-bg); color: var(--code-ink); border-radius: 8px;
    padding: 12px 14px; overflow-x: auto; font: 13px/1.5 ui-monospace, monospace;
    white-space: pre-wrap; word-break: break-all; margin: 6px 0 14px;
  }
  .error { color: #a3261f; margin-top: 10px; }
  a { color: var(--accent); }
</style>
</head>
<body>
<main>
  <h1>Remote terminal</h1>
  <p class="sub">Reach a <code>termulaa</code> terminal running on your own machine
  from anywhere. Mint a token, run the agent, then open the terminal on the view
  host. One <em>tunnel</em> is one live pooled connection from the agent; your
  browser traffic is multiplexed across them.</p>

  <section class="card">
    <h2>Connected agents</h2>
    <p class="agents-empty" id="agents-empty">No agents connected.</p>
    <ul class="agents" id="agents"></ul>
  </section>

  <section class="card">
    <h2>Pair a new agent</h2>
    <form class="mint" id="mint">
      <div>
        <label for="label">Label (e.g. the machine's name)</label>
        <input id="label" name="label" maxlength="64" placeholder="my-laptop">
      </div>
      <div>
        <label for="ttl">Valid for (days, max 90)</label>
        <input id="ttl" name="ttl" type="number" min="1" max="90" value="30">
      </div>
      <button type="submit">Mint token</button>
    </form>
    <div class="error" id="mint-error" hidden></div>
    <div class="result" id="result">
      <p class="once">Shown once — copy it now. Expires <span id="expires"></span>.</p>
      <div><strong>Token</strong> <button class="copy" data-copy="token">copy</button></div>
      <pre class="tok" id="token"></pre>
      <div><strong>Run on the machine with termulaa</strong> <button class="copy" data-copy="command">copy</button></div>
      <pre class="tok" id="command"></pre>
      <div id="open-wrap"><strong>Then open</strong> <a id="open-link" href="#" target="_blank" rel="noopener"></a>
        <button class="copy" data-copy="open">copy</button></div>
    </div>
  </section>
</main>
<script src="/rc/app.js"></script>
</body>
</html>
`

const rcPageJS = `"use strict";
(function () {
  var viewHost = "";

  function el(id) { return document.getElementById(id); }

  function renderAgents(agents) {
    var list = el("agents"), empty = el("agents-empty");
    list.textContent = "";
    empty.style.display = agents.length ? "none" : "";
    agents.forEach(function (a) {
      var li = document.createElement("li");
      var label = document.createElement("span");
      label.className = "agent-label";
      label.textContent = a.label || "(unnamed)";
      var id = document.createElement("span");
      id.className = "agent-id";
      id.textContent = a.id;
      var tunnels = document.createElement("span");
      tunnels.className = "agent-tunnels";
      tunnels.textContent = a.tunnels + (a.tunnels === 1 ? " tunnel up" : " tunnels up");
      var meta = document.createElement("span");
      meta.className = "agent-meta";
      meta.textContent = "local port " + a.port + " · since " +
        new Date(a.connected_at).toLocaleString();
      li.appendChild(label); li.appendChild(id); li.appendChild(tunnels); li.appendChild(meta);
      list.appendChild(li);
    });
  }

  function refresh() {
    fetch("/rc/api/agents", { headers: { "Accept": "application/json" } })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(); })
      .then(function (data) {
        viewHost = data.view_host || viewHost;
        renderAgents(data.agents || []);
      })
      .catch(function () { /* leave the last known rendering */ });
  }

  el("mint").addEventListener("submit", function (ev) {
    ev.preventDefault();
    var err = el("mint-error");
    err.hidden = true;
    var label = el("label").value.trim();
    var ttl = parseInt(el("ttl").value, 10) || 0;
    fetch("/rc/api/tokens", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ label: label, ttl: ttl })
    })
      .then(function (r) {
        if (!r.ok) { return r.json().then(function (b) { throw new Error(b.error || r.status); }); }
        return r.json();
      })
      .then(function (data) {
        el("token").textContent = data.token;
        el("expires").textContent = new Date(data.expires_at).toLocaleString();
        var cmd = "termulaa -rc -rc-server https://" + window.location.host +
          " -rc-token '" + data.token + "'";
        if (label) { cmd += " -rc-label '" + label.replace(/'/g, "'\\''") + "'"; }
        el("command").textContent = cmd;
        var open = "https://" + viewHost + "/?t=" + encodeURIComponent(data.token);
        el("open-wrap").style.display = viewHost ? "" : "none";
        el("open-link").href = open;
        el("open-link").textContent = "https://" + viewHost + "/";
        el("open-link").dataset.url = open;
        el("result").classList.add("show");
      })
      .catch(function (e) {
        err.textContent = "Mint failed: " + e.message;
        err.hidden = false;
      });
  });

  document.addEventListener("click", function (ev) {
    var btn = ev.target.closest ? ev.target.closest("button.copy") : null;
    if (!btn) { return; }
    var text = "";
    if (btn.dataset.copy === "token") { text = el("token").textContent; }
    else if (btn.dataset.copy === "command") { text = el("command").textContent; }
    else if (btn.dataset.copy === "open") { text = el("open-link").dataset.url || ""; }
    if (text && navigator.clipboard) {
      navigator.clipboard.writeText(text).then(function () {
        var old = btn.textContent;
        btn.textContent = "copied";
        setTimeout(function () { btn.textContent = old; }, 1200);
      });
    }
  });

  refresh();
  setInterval(refresh, 5000);
})();
`

func servePageJS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(rcPageJS))
}

// serveSignInPage answers an unauthenticated GET /rc with a pointer to memd's
// login rather than a bare JSON 401.
func serveSignInPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>memd · sign in</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 32em; margin: 4em auto;">
<h1 style="font-size: 1.3em;">Sign in required</h1>
<p>The remote-terminal page needs a memd login.
<a href="/">Sign in</a>, then come back to <code>/rc</code>.</p>
</body></html>
`))
}

// The pages below are served on the VIEW host, outside memd's global security
// headers, so they carry their own restrictive per-response policy.

func viewerPageHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
}

// servePairPage is the view host's answer to a browser with no valid pairing
// cookie.
func servePairPage(w http.ResponseWriter, status int) {
	viewerPageHeaders(w)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Pair this browser</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 34em; margin: 4em auto;">
<h1 style="font-size: 1.3em;">Pair this browser</h1>
<p>This terminal needs a valid tunnel token. Open the pairing link from your
memd <code>/rc</code> page (it looks like <code>/?t=&lt;token&gt;</code>), or
mint a fresh token there if yours has expired.</p>
</body></html>
`))
}

// serveAgentOffline is shown when the token is valid but the agent has no
// live tunnel.
func serveAgentOffline(w http.ResponseWriter) {
	serveStatusPage(w, http.StatusServiceUnavailable, "Agent offline",
		"No tunnel from this agent is currently connected. Start "+
			"<code>termulaa -rc</code> on the target machine, then reload.")
}

func serveStatusPage(w http.ResponseWriter, status int, title, body string) {
	viewerPageHeaders(w)
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>%s</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 34em; margin: 4em auto;">
<h1 style="font-size: 1.3em;">%s</h1>
<p>%s</p>
</body></html>
`, title, title, body)
}

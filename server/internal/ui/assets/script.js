(function () {
  function storageGet(key, fallback) {
    try {
      const value = window.localStorage.getItem(key);
      return value === null ? fallback : value;
    } catch (_) {
      return fallback;
    }
  }

  function storageSet(key, value) {
    try {
      window.localStorage.setItem(key, value);
    } catch (_) {}
  }

  const initialTheme = storageGet("memd-theme", "light");
  document.documentElement.setAttribute("data-theme", initialTheme === "dark" ? "dark" : "light");
  const initialLayout = storageGet("memd-layout", "wide");
  document.documentElement.setAttribute("data-layout", initialLayout === "centered" ? "centered" : "wide");

  async function responseJSON(response) {
    if (response.ok) {
      return response.json().catch(function () {
        return {};
      });
    }
    const payload = await response.json().catch(function () {
      return {};
    });
    throw new Error(payload.error || response.statusText || "request failed");
  }

  async function api(path, options) {
    return responseJSON(await fetch(path, options || {}));
  }

  function copyText(text) {
    if (window.navigator.clipboard && window.navigator.clipboard.writeText) {
      return window.navigator.clipboard.writeText(text);
    }
    const textarea = document.createElement("textarea");
    textarea.value = text;
    textarea.setAttribute("readonly", "");
    textarea.style.position = "fixed";
    textarea.style.left = "-9999px";
    document.body.appendChild(textarea);
    textarea.select();
    try {
      document.execCommand("copy");
      return Promise.resolve();
    } finally {
      document.body.removeChild(textarea);
    }
  }

  function currentOriginURL(rawURL) {
    try {
      const url = new URL(rawURL, window.location.origin);
      return window.location.origin + url.pathname + url.search + url.hash;
    } catch (_) {
      return rawURL || "";
    }
  }

  function connectorPath(connector) {
    try {
      return new URL(connector.url, window.location.origin).pathname;
    } catch (_) {
      return "";
    }
  }

  function defaultDirForm() {
    return {
      name: "",
      description: "",
      backend: "local",
      local_path: "",
      remote_url: "",
      branch: "main",
      base_path: "",
      author_name: "memd",
      author_email: "memd@localhost",
      ssh_key_path: "",
      err: "",
      submitting: false
    };
  }

  function defaultConnForm() {
    return {
      name: "",
      kind: "mcp",
      selected: [],
      write: true,
      err: "",
      submitting: false
    };
  }

  function defaultEditForm() {
    return {
      id: "",
      originalName: "",
      name: "",
      kind: "mcp",
      selected: [],
      write: true,
      err: "",
      submitting: false
    };
  }

  window.memdApp = function () {
    return {
      loading: true,
      loadErr: "",
      directories: [],
      connectors: [],
      theme: storageGet("memd-theme", "light"),
      layoutMode: storageGet("memd-layout", "wide") === "centered" ? "centered" : "wide",
      infoMode: storageGet("memd-info", "1") !== "0",
      logsHidden: storageGet("memd-logs-hidden", "0") === "1",
      logsWidth: parseInt(storageGet("memd-logs-w", "340"), 10) || 340,
      sheet: null,
      dirForm: defaultDirForm(),
      connForm: defaultConnForm(),
      editForm: defaultEditForm(),
      pickerOpen: false,
      pickerPath: "",
      pickerEntries: [],
      pickerParent: "",
      pickerErr: "",
      toast: "",
      toastTimer: null,
      entries: [],
      lastID: -1,
      logsPolling: false,
      logsTimer: null,

      init() {
        this.setTheme(this.theme);
        this.setLayout(this.layoutMode);
        this.setLogsWidth(this.logsWidth);
        this.load();
        this.pollLogs();
        this.logsTimer = window.setInterval(() => this.pollLogs(), 2000);
      },

      async load() {
        this.loading = true;
        this.loadErr = "";
        try {
          const results = await Promise.all([
            api("/api/directories"),
            api("/api/connectors")
          ]);
          this.directories = results[0].directories || [];
          this.connectors = (results[1].connectors || []).map(function (connector) {
            connector.revealed = false;
            connector.kind = connector.kind || "mcp";
            connector.url = currentOriginURL(connector.url);
            return connector;
          });
        } catch (error) {
          this.loadErr = error.message || String(error);
        } finally {
          this.loading = false;
        }
      },

      setTheme(theme) {
        this.theme = theme === "dark" ? "dark" : "light";
        document.documentElement.setAttribute("data-theme", this.theme);
        storageSet("memd-theme", this.theme);
      },

      toggleTheme() {
        this.setTheme(this.theme === "light" ? "dark" : "light");
      },

      setLayout(mode) {
        this.layoutMode = mode === "centered" ? "centered" : "wide";
        document.documentElement.setAttribute("data-layout", this.layoutMode);
        storageSet("memd-layout", this.layoutMode);
      },

      toggleLayout() {
        this.setLayout(this.layoutMode === "wide" ? "centered" : "wide");
      },

      setInfo(value) {
        this.infoMode = Boolean(value);
        storageSet("memd-info", this.infoMode ? "1" : "0");
      },

      setLogsHidden(value) {
        this.logsHidden = Boolean(value);
        storageSet("memd-logs-hidden", this.logsHidden ? "1" : "0");
      },

      setLogsWidth(width) {
        const clamped = Math.max(240, Math.min(720, Math.round(width)));
        this.logsWidth = clamped;
        document.documentElement.style.setProperty("--logs-w", clamped + "px");
        storageSet("memd-logs-w", String(clamped));
      },

      startResize(event) {
        event.preventDefault();
        const body = document.querySelector(".body");
        const startX = event.clientX;
        const startWidth = this.logsWidth;
        const move = (moveEvent) => {
          this.setLogsWidth(startWidth + (startX - moveEvent.clientX));
        };
        const up = () => {
          if (body) body.classList.remove("resizing");
          window.removeEventListener("mousemove", move);
          window.removeEventListener("mouseup", up);
        };
        if (body) body.classList.add("resizing");
        window.addEventListener("mousemove", move);
        window.addEventListener("mouseup", up);
      },

      openSheet(name) {
        if (name === "dir") {
          this.dirForm = defaultDirForm();
        }
        if (name === "conn") {
          this.connForm = defaultConnForm();
        }
        this.sheet = name;
      },

      openEdit(connector) {
        this.editForm = {
          id: connector.id,
          originalName: connector.name,
          name: connector.name,
          kind: connector.kind || "mcp",
          selected: (connector.directory_ids || []).slice(),
          write: Boolean(connector.write),
          err: "",
          submitting: false
        };
        this.sheet = "edit";
      },

      closeSheets() {
        this.sheet = null;
      },

      closeOverlays() {
        this.closeSheets();
        this.pickerOpen = false;
      },

      showToast(message) {
        this.toast = message;
        window.clearTimeout(this.toastTimer);
        this.toastTimer = window.setTimeout(() => {
          this.toast = "";
        }, 1800);
      },

      copy(text, label) {
        copyText(text).then(() => this.showToast(label || "Copied"));
      },

      copyURL(connector) {
        this.copy(connector.url, "URL copied");
      },

      async copySkill(connector) {
        try {
          const path = connectorPath(connector);
          if (!path) {
            throw new Error("missing connector path");
          }
          const response = await fetch(path, { cache: "no-store" });
          if (!response.ok) {
            throw new Error(response.statusText || "request failed");
          }
          await copyText(await response.text());
          this.showToast("Skill copied");
        } catch (_) {
          this.showToast("Copy failed");
        }
      },

      truncate(url) {
        if (!url || url.length <= 44) return url || "";
        return url.slice(0, 30) + "..." + url.slice(-10);
      },

      toggleConnectorReveal(connector) {
        connector.revealed = !connector.revealed;
      },

      connectorDirectoryNames(connector) {
        if (connector.directory_names) {
          return connector.directory_names;
        }
        const names = (connector.directory_ids || []).map((id) => {
          const directory = this.directories.find((d) => d.id === id);
          return directory ? directory.name : "(missing)";
        });
        return names.length ? names.join(", ") : "(none)";
      },

      toggleSelection(list, id, checked) {
        const index = list.indexOf(id);
        if (checked && index === -1) {
          list.push(id);
        } else if (!checked && index >= 0) {
          list.splice(index, 1);
        }
      },

      async createDirectory() {
        this.dirForm.err = "";
        this.dirForm.submitting = true;
        const payload = {
          name: this.dirForm.name,
          description: this.dirForm.description,
          backend: this.dirForm.backend
        };
        if (this.dirForm.backend === "local") {
          payload.local_path = this.dirForm.local_path;
        } else {
          payload.git = {
            remote_url: this.dirForm.remote_url,
            branch: this.dirForm.branch || "main",
            base_path: this.dirForm.base_path || "",
            author_name: this.dirForm.author_name || "memd",
            author_email: this.dirForm.author_email || "memd@localhost",
            ssh_key_path: this.dirForm.ssh_key_path || ""
          };
        }
        try {
          await api("/api/directories", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload)
          });
          this.closeSheets();
          await this.load();
        } catch (error) {
          this.dirForm.err = error.message || "create failed";
        } finally {
          this.dirForm.submitting = false;
        }
      },

      async deleteDirectory(directory) {
        if (!window.confirm("Delete directory " + directory.name + "? Connectors using it will lose access.")) {
          return;
        }
        await fetch("/api/directories/" + encodeURIComponent(directory.id), { method: "DELETE" });
        await this.load();
      },

      async createConnector() {
        this.connForm.err = "";
        this.connForm.submitting = true;
        try {
          await api("/api/connectors", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              name: this.connForm.name,
              kind: this.connForm.kind,
              directory_ids: this.connForm.selected,
              write: this.connForm.write
            })
          });
          this.closeSheets();
          await this.load();
        } catch (error) {
          this.connForm.err = error.message || "create failed";
        } finally {
          this.connForm.submitting = false;
        }
      },

      async updateConnector() {
        this.editForm.err = "";
        this.editForm.submitting = true;
        try {
          await api("/api/connectors/" + encodeURIComponent(this.editForm.id), {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              name: this.editForm.name,
              kind: this.editForm.kind,
              directory_ids: this.editForm.selected,
              write: this.editForm.write
            })
          });
          this.closeSheets();
          await this.load();
        } catch (error) {
          this.editForm.err = error.message || "update failed";
        } finally {
          this.editForm.submitting = false;
        }
      },

      async rotateConnector(connector) {
        const message = "Rotate token for " + connector.name + "? The current URL stops working immediately and you will need to paste the new one into the agent.";
        if (!window.confirm(message)) {
          return;
        }
        try {
          await api("/api/connectors/" + encodeURIComponent(connector.id) + "/rotate", { method: "POST" });
          await this.load();
        } catch (error) {
          window.alert(error.message || "rotate failed");
        }
      },

      async deleteConnector(connector) {
        if (!window.confirm("Delete connector " + connector.name + "?")) {
          return;
        }
        await fetch("/api/connectors/" + encodeURIComponent(connector.id), { method: "DELETE" });
        await this.load();
      },

      async openPicker() {
        this.pickerOpen = true;
        await this.browse("");
      },

      async browse(path) {
        try {
          const suffix = path ? "?path=" + encodeURIComponent(path) : "";
          const data = await api("/api/browse" + suffix);
          this.pickerPath = data.path;
          this.pickerEntries = data.dirs || [];
          this.pickerParent = data.parent || "";
          this.pickerErr = "";
        } catch (error) {
          this.pickerErr = error.message || String(error);
        }
      },

      joinPath(base, name) {
        if (!base || base === "/") {
          return "/" + name;
        }
        return base + "/" + name;
      },

      selectFolder() {
        this.dirForm.local_path = this.pickerPath;
        this.pickerOpen = false;
      },

      async pollLogs() {
        if (this.logsPolling) {
          return;
        }
        this.logsPolling = true;
        try {
          const data = await api("/api/logs?since=" + encodeURIComponent(String(this.lastID)), { cache: "no-store" });
          const fresh = data.entries || [];
          if (!fresh.length) return;
          fresh.forEach(function (entry) {
            entry.timeLocal = new Date(entry.time).toLocaleTimeString();
          });
          this.entries = this.entries.concat(fresh).slice(-200);
          this.lastID = fresh[fresh.length - 1].id;
          this.$nextTick(() => {
            const el = this.$refs.logsScroll;
            if (el) {
              el.scrollTop = el.scrollHeight;
            }
          });
        } catch (_) {
        } finally {
          this.logsPolling = false;
        }
      }
    };
  };
})();

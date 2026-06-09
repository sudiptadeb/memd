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
    const error = new Error(payload.error || response.statusText || "request failed");
    error.status = response.status;
    throw error;
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

  function currentBaseURL() {
    return window.location.origin;
  }

  function publicURL(rawURL) {
    try {
      const url = new URL(rawURL, currentBaseURL());
      return currentBaseURL() + url.pathname + url.search + url.hash;
    } catch (_) {
      return rawURL || "";
    }
  }

  function publicPath(rawURL) {
    try {
      return new URL(rawURL, currentBaseURL()).pathname;
    } catch (_) {
      return "";
    }
  }

  function appendPath(baseURL, path) {
    return publicURL(baseURL).replace(/\/+$/, "") + "/" + path.replace(/^\/+/, "");
  }

  function tokenlessConnectorURL(connector) {
    if (connector.auth_url) {
      return publicURL(connector.auth_url);
    }
    try {
      const url = new URL(connector.url, currentBaseURL());
      const segments = url.pathname.split("/").filter(Boolean);
      if (segments[0] === "mcp" || segments[0] === "http") {
        return currentBaseURL() + "/" + segments[0];
      }
    } catch (_) {}
    return publicURL(connector.url);
  }

  function connectorPath(connector) {
    return publicPath(connector.url);
  }

  function defaultDirForm() {
    return {
      name: "",
      team_id: "",
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
      team_id: "",
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
      team_id: "",
      name: "",
      kind: "mcp",
      selected: [],
      write: true,
      err: "",
      submitting: false
    };
  }

  function defaultLoginForm() {
    return {
      username: "",
      password: "",
      err: "",
      submitting: false
    };
  }

  function defaultTeamForm() {
    return {
      name: "",
      slug: "",
      err: "",
      submitting: false
    };
  }

  function defaultMemberForm() {
    return {
      username: "",
      role: "member",
      err: "",
      submitting: false
    };
  }

  function defaultInviteForm() {
    return {
      role: "member",
      expires_at: "",
      max_uses: "",
      err: "",
      submitting: false
    };
  }

  function downloadJSON(filename, value) {
    const blob = new Blob([JSON.stringify(value, null, 2) + "\n"], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }

  function readFileText(file) {
    return new Promise(function (resolve, reject) {
      const reader = new FileReader();
      reader.onload = function () { resolve(String(reader.result || "")); };
      reader.onerror = function () { reject(reader.error || new Error("read failed")); };
      reader.readAsText(file);
    });
  }

  window.memdApp = function () {
    return {
      sessionChecked: false,
      user: null,
      oidcEnabled: false,
      showLocalLogin: false,
      loading: true,
      loadErr: "",
      inviteToken: "",
      invitePreview: null,
      inviteErr: "",
      inviteAccepting: false,
      directories: [],
      connectors: [],
      teams: [],
      activeView: storageGet("memd-view", "directories"),
      activeScope: storageGet("memd-scope", "personal"),
      navOpen: false,
      loginForm: defaultLoginForm(),
      theme: storageGet("memd-theme", "light"),
      layoutMode: storageGet("memd-layout", "wide") === "centered" ? "centered" : "wide",
      logsHidden: storageGet("memd-logs-hidden", "0") === "1",
      logsWidth: parseInt(storageGet("memd-logs-w", "340"), 10) || 340,
      sheet: null,
      dirForm: defaultDirForm(),
      connForm: defaultConnForm(),
      editForm: defaultEditForm(),
      teamForm: defaultTeamForm(),
      teamDetail: null,
      teamMembers: [],
      teamInvites: [],
      memberForm: defaultMemberForm(),
      inviteForm: defaultInviteForm(),
      createdInviteURL: "",
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

      async init() {
        this.setTheme(this.theme);
        this.setLayout(this.layoutMode);
        this.setLogsWidth(this.logsWidth);
        this.inviteToken = new URLSearchParams(window.location.search).get("invite") || "";
        if (this.inviteToken) {
          await this.loadInvitePreview();
        }
        this.readLoginError();
        await this.checkSession();
        if (this.user) {
          await this.load();
          this.startLogs();
        }
      },

      readLoginError() {
        const params = new URLSearchParams(window.location.search);
        const err = params.get("login_error");
        if (err) {
          this.loginForm.err = err;
          params.delete("login_error");
          const qs = params.toString();
          window.history.replaceState({}, "", window.location.pathname + (qs ? "?" + qs : ""));
        }
      },

      async checkSession() {
        try {
          const data = await api("/api/session", { cache: "no-store" });
          this.user = data.user || null;
          this.oidcEnabled = Boolean(data.auth && data.auth.oidc_enabled);
          // When SSO is the default, keep the local form tucked away as a backup.
          this.showLocalLogin = !this.oidcEnabled;
        } catch (error) {
          this.user = null;
        } finally {
          this.normalizeView();
          this.sessionChecked = true;
          this.loading = false;
        }
      },

      ssoLogin() {
        window.location.href = "/auth/login";
      },

      async login() {
        this.loginForm.err = "";
        this.loginForm.submitting = true;
        try {
          const data = await api("/api/auth/login", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              username: this.loginForm.username,
              password: this.loginForm.password
            })
          });
          this.user = data.user || null;
          this.normalizeView();
          this.loginForm = defaultLoginForm();
          await this.load();
          this.startLogs();
        } catch (error) {
          this.loginForm.err = error.message || "login failed";
        } finally {
          this.loginForm.submitting = false;
        }
      },

      async logout() {
        let logoutURL = "";
        try {
          const data = await api("/api/auth/logout", { method: "POST" });
          logoutURL = (data && data.logout_url) || "";
        } catch (error) {}
        this.stopLogs();
        this.user = null;
        this.directories = [];
        this.connectors = [];
        this.teams = [];
        this.teamDetail = null;
        this.teamMembers = [];
        this.teamInvites = [];
        this.entries = [];
        this.lastID = -1;
        this.activeView = "directories";
        this.navOpen = false;
        storageSet("memd-view", this.activeView);
        this.closeOverlays();
        if (logoutURL) {
          window.location.href = logoutURL;
        }
      },

      startLogs() {
        this.pollLogs();
        if (!this.logsTimer) {
          this.logsTimer = window.setInterval(() => this.pollLogs(), 2000);
        }
      },

      stopLogs() {
        if (this.logsTimer) {
          window.clearInterval(this.logsTimer);
          this.logsTimer = null;
        }
      },

      async load() {
        if (!this.user) {
          return;
        }
        this.loading = true;
        this.loadErr = "";
        try {
          const results = await Promise.all([
            api("/api/directories"),
            api("/api/connectors"),
            api("/api/teams")
          ]);
          this.directories = results[0].directories || [];
          this.connectors = (results[1].connectors || []).map(function (connector) {
            connector.revealed = false;
            connector.expanded = false;
            connector.kind = connector.kind || "mcp";
            connector.url = publicURL(connector.url);
            connector.auth_url = tokenlessConnectorURL(connector);
            return connector;
          });
          this.teams = results[2].teams || [];
          this.normalizeView();
          this.normalizeScope();
        } catch (error) {
          if (error.status === 401) {
            this.user = null;
            this.stopLogs();
          }
          this.loadErr = error.message || String(error);
        } finally {
          this.loading = false;
        }
      },

      normalizeScope() {
        if (this.activeScope !== "personal" && !this.teams.some((team) => team.id === this.activeScope)) {
          this.activeScope = "personal";
        }
        storageSet("memd-scope", this.activeScope);
      },

      normalizeView() {
        const valid = ["info", "directories", "connectors", "logs"];
        if (this.user && !this.user.super_admin) {
          valid.unshift("teams");
        }
        if (!valid.includes(this.activeView)) {
          this.activeView = "directories";
        }
        storageSet("memd-view", this.activeView);
      },

      setView(view) {
        this.activeView = view || "directories";
        this.normalizeView();
        this.closeNavIfMobile();
      },

      setScope(scope) {
        this.activeScope = scope || "personal";
        this.normalizeScope();
        this.closeNavIfMobile();
      },

      isMobileNav() {
        return window.matchMedia && window.matchMedia("(max-width: 920px)").matches;
      },

      toggleNav() {
        this.navOpen = !this.navOpen;
      },

      closeNav() {
        this.navOpen = false;
      },

      closeNavIfMobile() {
        if (this.isMobileNav()) {
          this.closeNav();
        }
      },

      activeTeam() {
        return this.teams.find((team) => team.id === this.activeScope) || null;
      },

      manageableTeams() {
        return this.teams.filter((team) => team.can_manage);
      },

      teamName(teamID) {
        const team = this.teams.find((item) => item.id === teamID);
        return team ? team.name : "";
      },

      activeScopeLabel() {
        return this.activeScope === "personal" ? "Personal" : (this.teamName(this.activeScope) || "Team");
      },

      directoryCount(scope) {
        const wanted = scope === "personal" ? "" : (scope || "");
        return this.directories.filter((directory) => (directory.team_id || "") === wanted).length;
      },

      connectorCount(scope) {
        const wanted = scope === "personal" ? "" : (scope || "");
        return this.connectors.filter((connector) => (connector.team_id || "") === wanted).length;
      },

      visibleDirectories() {
        if (this.activeScope === "personal") {
          return this.directories.filter((directory) => !directory.team_id);
        }
        return this.directories.filter((directory) => directory.team_id === this.activeScope);
      },

      visibleConnectors() {
        if (this.activeScope === "personal") {
          return this.connectors.filter((connector) => !connector.team_id);
        }
        return this.connectors.filter((connector) => connector.team_id === this.activeScope);
      },

      attachableDirectories(teamID) {
        const scope = teamID || "";
        return this.directories.filter((directory) => {
          if (!directory.can_attach) return false;
          return (directory.team_id || "") === scope;
        });
      },

      roleLabel(role) {
        return role || "member";
      },

      inviteUsage(invite) {
        const limit = invite.max_uses ? String(invite.max_uses) : "unlimited";
        return String(invite.use_count || 0) + " / " + limit;
      },

      inviteExpiry(invite) {
        if (!invite.expires_at) return "No expiry";
        return new Date(invite.expires_at).toLocaleString();
      },

      async loadInvitePreview() {
        this.inviteErr = "";
        this.invitePreview = null;
        if (!this.inviteToken) return;
        try {
          const data = await api("/api/team-invites/" + encodeURIComponent(this.inviteToken), { cache: "no-store" });
          this.invitePreview = data.invite || null;
        } catch (error) {
          this.inviteErr = error.message || "invite not found";
        }
      },

      async acceptInvite() {
        if (!this.inviteToken || !this.user) return;
        this.inviteAccepting = true;
        this.inviteErr = "";
        try {
          const data = await api("/api/team-invites/" + encodeURIComponent(this.inviteToken) + "/accept", { method: "POST" });
          this.showToast(data.team ? "Joined " + data.team.name : "Joined team");
          this.inviteToken = "";
          this.invitePreview = null;
          const url = new URL(window.location.href);
          url.searchParams.delete("invite");
          window.history.replaceState({}, "", url.pathname + url.search + url.hash);
          await this.load();
        } catch (error) {
          this.inviteErr = error.message || "join failed";
        } finally {
          this.inviteAccepting = false;
        }
      },

      dismissInvite() {
        this.inviteToken = "";
        this.invitePreview = null;
        this.inviteErr = "";
        const url = new URL(window.location.href);
        url.searchParams.delete("invite");
        window.history.replaceState({}, "", url.pathname + url.search + url.hash);
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
          const team = this.activeTeam();
          if (team && team.can_manage) {
            this.dirForm.team_id = team.id;
          }
        }
        if (name === "conn") {
          this.connForm = defaultConnForm();
          const team = this.activeTeam();
          if (team && team.can_manage) {
            this.connForm.team_id = team.id;
          }
        }
        if (name === "team-new") {
          this.teamForm = defaultTeamForm();
        }
        this.sheet = name;
      },

      openEdit(connector) {
        this.editForm = {
          id: connector.id,
          originalName: connector.name,
          team_id: connector.team_id || "",
          name: connector.name,
          kind: connector.kind || "mcp",
          selected: (connector.directory_ids || []).slice(),
          write: Boolean(connector.write),
          err: "",
          submitting: false
        };
        this.sheet = "edit";
      },

      async openTeam(team) {
        this.teamDetail = team;
        this.memberForm = defaultMemberForm();
        this.inviteForm = defaultInviteForm();
        this.createdInviteURL = "";
        this.sheet = "team";
        await this.loadTeamDetail(team.id);
      },

      closeSheets() {
        this.sheet = null;
      },

      closeOverlays() {
        this.closeSheets();
        this.pickerOpen = false;
        this.closeNav();
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
        this.copy(publicURL(connector.url), "URL copied");
      },

      copyAuth(connector) {
        this.copy("URL: " + tokenlessConnectorURL(connector) + "\n" + connector.auth_header, "Auth copied");
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

      toggleConnectorExpand(connector) {
        connector.expanded = !connector.expanded;
      },

      connectorInstructionURL(connector) {
        const base = tokenlessConnectorURL(connector);
        return connector.kind === "http" ? appendPath(base, "memory_load") : base;
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

      setConnectorTeam(form, teamID) {
        form.team_id = teamID || "";
        const allowed = new Set(this.attachableDirectories(form.team_id).map((directory) => directory.id));
        form.selected = form.selected.filter((id) => allowed.has(id));
      },

      async createDirectory() {
        this.dirForm.err = "";
        this.dirForm.submitting = true;
        const payload = {
          name: this.dirForm.name,
          team_id: this.dirForm.team_id || "",
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
              team_id: this.connForm.team_id || "",
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
              team_id: this.editForm.team_id || "",
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

      async createTeam() {
        this.teamForm.err = "";
        this.teamForm.submitting = true;
        try {
          const data = await api("/api/teams", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              name: this.teamForm.name,
              slug: this.teamForm.slug
            })
          });
          this.closeSheets();
          await this.load();
          if (data.team && data.team.id) {
            this.setScope(data.team.id);
          }
        } catch (error) {
          this.teamForm.err = error.message || "create failed";
        } finally {
          this.teamForm.submitting = false;
        }
      },

      async loadTeamDetail(teamID) {
        try {
          const results = await Promise.all([
            api("/api/teams/" + encodeURIComponent(teamID) + "/members", { cache: "no-store" }),
            api("/api/teams/" + encodeURIComponent(teamID) + "/invites", { cache: "no-store" }).catch(function () { return { invites: [] }; })
          ]);
          this.teamMembers = results[0].members || [];
          this.teamInvites = results[1].invites || [];
        } catch (error) {
          this.memberForm.err = error.message || "load failed";
        }
      },

      async addTeamMember() {
        if (!this.teamDetail) return;
        this.memberForm.err = "";
        this.memberForm.submitting = true;
        try {
          await api("/api/teams/" + encodeURIComponent(this.teamDetail.id) + "/members", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              username: this.memberForm.username,
              role: this.memberForm.role
            })
          });
          this.memberForm = defaultMemberForm();
          await this.loadTeamDetail(this.teamDetail.id);
        } catch (error) {
          this.memberForm.err = error.message || "add failed";
        } finally {
          this.memberForm.submitting = false;
        }
      },

      async updateTeamMemberRole(member, role) {
        if (!this.teamDetail) return;
        try {
          await api("/api/teams/" + encodeURIComponent(this.teamDetail.id) + "/members/" + encodeURIComponent(member.user_id), {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ role: role })
          });
          await this.loadTeamDetail(this.teamDetail.id);
          await this.load();
        } catch (error) {
          window.alert(error.message || "update failed");
          await this.loadTeamDetail(this.teamDetail.id);
        }
      },

      async removeTeamMember(member) {
        if (!this.teamDetail) return;
        if (!window.confirm("Remove " + member.username + " from " + this.teamDetail.name + "?")) {
          return;
        }
        try {
          await api("/api/teams/" + encodeURIComponent(this.teamDetail.id) + "/members/" + encodeURIComponent(member.user_id), { method: "DELETE" });
          await this.loadTeamDetail(this.teamDetail.id);
          await this.load();
        } catch (error) {
          window.alert(error.message || "remove failed");
        }
      },

      async createInvite() {
        if (!this.teamDetail) return;
        this.inviteForm.err = "";
        this.inviteForm.submitting = true;
        this.createdInviteURL = "";
        let expiresAt = "";
        if (this.inviteForm.expires_at) {
          expiresAt = new Date(this.inviteForm.expires_at).toISOString();
        }
        const maxUses = parseInt(this.inviteForm.max_uses, 10);
        try {
          const data = await api("/api/teams/" + encodeURIComponent(this.teamDetail.id) + "/invites", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              role: this.inviteForm.role,
              expires_at: expiresAt,
              max_uses: Number.isFinite(maxUses) && maxUses > 0 ? maxUses : null
            })
          });
          this.createdInviteURL = data.invite_url || "";
          this.inviteForm = defaultInviteForm();
          await this.loadTeamDetail(this.teamDetail.id);
          if (this.createdInviteURL) {
            await copyText(this.createdInviteURL);
            this.showToast("Invite copied");
          }
        } catch (error) {
          this.inviteForm.err = error.message || "invite failed";
        } finally {
          this.inviteForm.submitting = false;
        }
      },

      async revokeInvite(invite) {
        if (!this.teamDetail) return;
        try {
          await api("/api/teams/" + encodeURIComponent(this.teamDetail.id) + "/invites/" + encodeURIComponent(invite.id) + "/revoke", { method: "POST" });
          await this.loadTeamDetail(this.teamDetail.id);
        } catch (error) {
          window.alert(error.message || "revoke failed");
        }
      },

      async deleteTeam(team) {
        if (!window.confirm("Delete team " + team.name + "? Team-scoped directories and connectors become personal to their original owners.")) {
          return;
        }
        try {
          await api("/api/teams/" + encodeURIComponent(team.id), { method: "DELETE" });
          this.closeSheets();
          this.setScope("personal");
          await this.load();
        } catch (error) {
          window.alert(error.message || "delete failed");
        }
      },

      async setDirectoryTeam(directory, teamID) {
        try {
          await api("/api/directories/" + encodeURIComponent(directory.id), {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ team_id: teamID || "" })
          });
          await this.load();
        } catch (error) {
          window.alert(error.message || "update failed");
          await this.load();
        }
      },

      async exportUserData() {
        try {
          const bundle = await api("/api/data", { cache: "no-store" });
          downloadJSON("memd-user-data-" + this.user.username + ".json", bundle);
        } catch (error) {
          window.alert(error.message || "export failed");
        }
      },

      openImportUserData() {
        const input = this.$refs.userDataImport;
        if (input) {
          input.value = "";
          input.click();
        }
      },

      async importUserData(event) {
        const file = event.target.files && event.target.files[0];
        if (!file) {
          return;
        }
        const replace = window.confirm("Replace your existing directories and connectors? Choose Cancel to merge/update by id.");
        try {
          const text = await readFileText(file);
          const bundle = JSON.parse(text);
          const suffix = replace ? "?replace=1" : "";
          await api("/api/data" + suffix, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(bundle)
          });
          this.showToast("Imported data");
          await this.load();
        } catch (error) {
          window.alert(error.message || "import failed");
        }
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
        if (!this.user) {
          return;
        }
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
            document.querySelectorAll(".logs-list").forEach(function (el) {
              if (el.offsetParent !== null) {
                el.scrollTop = el.scrollHeight;
              }
            });
          });
        } catch (_) {
        } finally {
          this.logsPolling = false;
        }
      }
    };
  };
})();

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

  function defaultLoginForm() {
    return { username: "", password: "", err: "", submitting: false };
  }

  function defaultUserForm() {
    return { username: "", display_name: "", password: "", err: "", submitting: false };
  }

  window.memdAdminApp = function () {
    return {
      sessionChecked: false,
      user: null,
      users: [],
      loading: false,
      loadErr: "",
      loginForm: defaultLoginForm(),
      userForm: defaultUserForm(),
      theme: storageGet("memd-theme", "light"),
      sheet: null,
      toast: "",
      toastTimer: null,

      async init() {
        this.setTheme(this.theme);
        await this.checkSession();
        if (this.user && this.user.super_admin) {
          await this.loadUsers();
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

      async checkSession() {
        try {
          const data = await api("/api/session", { cache: "no-store" });
          this.user = data.user || null;
        } catch (_) {
          this.user = null;
        } finally {
          this.sessionChecked = true;
        }
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
          this.loginForm = defaultLoginForm();
          if (this.user && this.user.super_admin) {
            await this.loadUsers();
          }
        } catch (error) {
          this.loginForm.err = error.message || "login failed";
        } finally {
          this.loginForm.submitting = false;
        }
      },

      async logout() {
        await fetch("/api/auth/logout", { method: "POST" }).catch(function () {});
        this.user = null;
        this.users = [];
        this.closeSheets();
      },

      async loadUsers() {
        this.loading = true;
        this.loadErr = "";
        try {
          const data = await api("/api/admin/users", { cache: "no-store" });
          this.users = data.users || [];
        } catch (error) {
          this.loadErr = error.message || "could not load users";
        } finally {
          this.loading = false;
        }
      },

      openSheet(name) {
        if (name === "user") {
          this.userForm = defaultUserForm();
        }
        this.sheet = name;
      },

      closeSheets() {
        this.sheet = null;
      },

      showToast(message) {
        this.toast = message;
        window.clearTimeout(this.toastTimer);
        this.toastTimer = window.setTimeout(() => {
          this.toast = "";
        }, 1800);
      },

      async createUser() {
        this.userForm.err = "";
        this.userForm.submitting = true;
        try {
          await api("/api/admin/users", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              username: this.userForm.username,
              display_name: this.userForm.display_name,
              password: this.userForm.password
            })
          });
          this.closeSheets();
          await this.loadUsers();
        } catch (error) {
          this.userForm.err = error.message || "create failed";
        } finally {
          this.userForm.submitting = false;
        }
      },

      async toggleDisabled(target) {
        if (target.super_admin && !target.disabled) {
          if (!window.confirm("Disable super admin " + target.username + "?")) {
            return;
          }
        }
        try {
          await api("/api/admin/users/" + encodeURIComponent(target.id), {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ disabled: !target.disabled })
          });
          await this.loadUsers();
        } catch (error) {
          window.alert(error.message || "update failed");
        }
      },

      async resetPassword(target) {
        const password = window.prompt("New password for " + target.username);
        if (!password) {
          return;
        }
        try {
          await api("/api/admin/users/" + encodeURIComponent(target.id) + "/password", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ password: password })
          });
          this.showToast("Password reset");
        } catch (error) {
          window.alert(error.message || "password reset failed");
        }
      },

      userDisplayName(target) {
        return target.display_name || target.username;
      }
    };
  };
})();

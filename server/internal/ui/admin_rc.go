package ui

import (
	"encoding/json"
	"net/http"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/logs"
)

// rcConfigView is the admin-facing state of the reverse-tunnel rendezvous
// (termulaa rc). Enabled is the persisted super-admin setting (default on when
// nothing has been stored); Active is the effective state right now, which
// also folds in the MEMD_RC=0 kill switch and token-key availability.
type rcConfigView struct {
	Enabled bool `json:"enabled"`
	Active  bool `json:"active"`
	// KillSwitch: MEMD_RC is explicitly set to an off value in the server's
	// environment, forcing the feature off regardless of Enabled.
	KillSwitch bool `json:"kill_switch"`
	// Available: a token-signing key exists (MEMD_RC_TOKEN_SECRET or
	// MEMD_SESSION_SECRET). Without one the feature cannot run at all.
	Available bool `json:"available"`
	// ViewHost is the dedicated viewer hostname in host mode, "" in path mode.
	ViewHost string `json:"view_host"`
}

// adminRCAPI lets a super admin read and flip the reverse-tunnel rendezvous at
// runtime. The setting is persisted in app_settings (so it survives deploys
// and restarts) and applied to the live handler immediately: disabling closes
// every registered tunnel and stops serving the /rc* routes and the viewer
// surface, and re-enabling serves again — agents reconnect on their own.
func (h *Handler) adminRCAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		stored, ok, err := h.accounts.GetRCSettings(r.Context())
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err)
			return
		}
		enabled := true // no stored value yet: the feature defaults to on
		if ok {
			enabled = stored.Enabled
		}
		writeJSON(w, http.StatusOK, map[string]any{"rc": h.rcView(enabled)})
	case http.MethodPut:
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		if err := h.accounts.SaveRCSettings(r.Context(), account.RCSettings{Enabled: body.Enabled}); err != nil {
			httpErr(w, http.StatusInternalServerError, err)
			return
		}
		// Apply to the running handler at once — no restart. When the feature
		// cannot run (kill switch / no key) the setting is still persisted so
		// it takes effect as soon as the environment allows.
		if h.rc != nil {
			h.rc.SetEnabled(body.Enabled)
		}
		if body.Enabled {
			logs.Info("rc: reverse tunnel enabled by super admin")
		} else {
			logs.Info("rc: reverse tunnel disabled by super admin; live tunnels closed")
		}
		writeJSON(w, http.StatusOK, map[string]any{"rc": h.rcView(body.Enabled)})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) rcView(enabled bool) rcConfigView {
	view := rcConfigView{
		Enabled:    enabled,
		Active:     h.rcActive(),
		KillSwitch: h.rcKillSwitch,
		Available:  h.rcKeyAvailable,
	}
	if h.rc != nil {
		view.ViewHost = h.rc.ViewHost()
	}
	return view
}

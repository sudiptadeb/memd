package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/backup"
	"github.com/sudiptadeb/memd/server/internal/logs"
)

// manualBackupTimeout bounds a "back up now" request: snapshot, encrypt,
// clone/pull, push.
const manualBackupTimeout = 10 * time.Minute

// backupConfigView is the admin-facing state of the encrypted backup. The
// access token and passphrase are never sent back; only their presence is.
type backupConfigView struct {
	Enabled       bool   `json:"enabled"`
	RemoteURL     string `json:"remote_url"`
	Branch        string `json:"branch"`
	AuthUsername  string `json:"auth_username"`
	HasAuthToken  bool   `json:"has_auth_token"`
	HasPassphrase bool   `json:"has_passphrase"`
	DailyAtUTC    string `json:"daily_at_utc"`
	RetentionDays int    `json:"retention_days"`
	// Supported is false when the account database is in-memory and cannot
	// be snapshotted.
	Supported bool `json:"supported"`
	// Running reports a backup in progress right now.
	Running bool `json:"running"`
	// NextRunAt is the scheduler's next planned run, null when none.
	NextRunAt *time.Time       `json:"next_run_at"`
	Status    backupStatusView `json:"status"`
}

// backupStatusView is the persisted outcome of the last run.
type backupStatusView struct {
	LastRunAt   *time.Time `json:"last_run_at"`
	LastOK      bool       `json:"last_ok"`
	LastError   string     `json:"last_error"`
	LastArchive string     `json:"last_archive"`
	LastSize    int64      `json:"last_size"`
	LastCommit  string     `json:"last_commit"`
	LastTrigger string     `json:"last_trigger"`
}

var errBackupUnavailable = errors.New("backup runner is not available")

// adminBackupAPI lets a super admin read and update the backup settings,
// which are persisted in app_settings and picked up by the scheduler at once.
func (h *Handler) adminBackupAPI(w http.ResponseWriter, r *http.Request) {
	if h.backup == nil {
		httpErr(w, http.StatusServiceUnavailable, errBackupUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.writeBackupView(w, r)
	case http.MethodPut:
		h.updateBackup(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) updateBackup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled      bool   `json:"enabled"`
		RemoteURL    string `json:"remote_url"`
		Branch       string `json:"branch"`
		AuthUsername string `json:"auth_username"`
		// Pointers: nil/absent keeps the stored value, "" clears it.
		AuthToken     *string `json:"auth_token"`
		Passphrase    *string `json:"passphrase"`
		DailyAtUTC    string  `json:"daily_at_utc"`
		RetentionDays int     `json:"retention_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	current, _, err := h.accounts.GetBackupSettings(r.Context())
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	settings := account.BackupSettings{
		Enabled:       body.Enabled,
		RemoteURL:     strings.TrimSpace(body.RemoteURL),
		Branch:        strings.TrimSpace(body.Branch),
		AuthUsername:  strings.TrimSpace(body.AuthUsername),
		AuthToken:     current.AuthToken,
		Passphrase:    current.Passphrase,
		DailyAtUTC:    strings.TrimSpace(body.DailyAtUTC),
		RetentionDays: body.RetentionDays,
	}
	if settings.Branch == "" {
		settings.Branch = account.DefaultBackupBranch
	}
	if settings.DailyAtUTC == "" {
		settings.DailyAtUTC = account.DefaultBackupDailyAtUTC
	}
	if body.AuthToken != nil {
		settings.AuthToken = strings.TrimSpace(*body.AuthToken)
	}
	if body.Passphrase != nil {
		settings.Passphrase = *body.Passphrase
	}
	if err := backup.Validate(settings); err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	if err := h.accounts.SaveBackupSettings(r.Context(), settings); err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	h.backup.Notify()
	if settings.Enabled {
		logs.Info("backup configured by super admin daily_at=%s UTC retention_days=%d", settings.DailyAtUTC, settings.RetentionDays)
	} else {
		logs.Info("backup disabled by super admin")
	}
	h.writeBackupView(w, r)
}

// adminBackupRunAPI performs a backup now and returns the updated view. A run
// already in progress answers 409; disabled or unconfigured backups 400; a
// run that started but failed 502 (the recorded status carries the error).
func (h *Handler) adminBackupRunAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.backup == nil {
		httpErr(w, http.StatusServiceUnavailable, errBackupUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), manualBackupTimeout)
	defer cancel()
	if _, err := h.backup.Run(ctx, backup.TriggerManual); err != nil {
		switch {
		case errors.Is(err, backup.ErrAlreadyRunning):
			httpErr(w, http.StatusConflict, err)
		case errors.Is(err, backup.ErrDisabled), errors.Is(err, backup.ErrNotConfigured), errors.Is(err, backup.ErrUnsupported):
			httpErr(w, http.StatusBadRequest, err)
		default:
			httpErr(w, http.StatusBadGateway, err)
		}
		return
	}
	h.writeBackupView(w, r)
}

// adminBackupCheckAPI tests the repository connection with the stored
// credentials, letting the body override remote/branch/username/token so an
// admin can test before saving.
func (h *Handler) adminBackupCheckAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.backup == nil {
		httpErr(w, http.StatusServiceUnavailable, errBackupUnavailable)
		return
	}
	var body struct {
		RemoteURL    string  `json:"remote_url"`
		Branch       string  `json:"branch"`
		AuthUsername string  `json:"auth_username"`
		AuthToken    *string `json:"auth_token"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
	}
	settings, _, err := h.accounts.GetBackupSettings(r.Context())
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	if v := strings.TrimSpace(body.RemoteURL); v != "" {
		settings.RemoteURL = v
	}
	if v := strings.TrimSpace(body.Branch); v != "" {
		settings.Branch = v
	}
	if v := strings.TrimSpace(body.AuthUsername); v != "" {
		settings.AuthUsername = v
	}
	if body.AuthToken != nil && strings.TrimSpace(*body.AuthToken) != "" {
		settings.AuthToken = strings.TrimSpace(*body.AuthToken)
	}
	probe := settings
	probe.Enabled = false // validate transport rules only, not "configured"
	if err := backup.Validate(probe); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	ok, message := h.backup.Check(r.Context(), settings)
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "message": message})
}

// adminBackupDownloadAPI builds an archive with the stored passphrase and
// streams it as a file download.
func (h *Handler) adminBackupDownloadAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.backup == nil {
		httpErr(w, http.StatusServiceUnavailable, errBackupUnavailable)
		return
	}
	if !h.backup.Supported() {
		httpErr(w, http.StatusBadRequest, backup.ErrUnsupported)
		return
	}
	settings, _, err := h.accounts.GetBackupSettings(r.Context())
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	if settings.Passphrase == "" {
		httpErr(w, http.StatusBadRequest, errors.New("no backup passphrase is configured"))
		return
	}
	archive, err := h.backup.Build(r.Context())
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	logs.Info("backup downloaded by super admin archive=%s bytes=%d", archive.Name, len(archive.Data))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", archive.Name))
	w.Header().Set("Content-Length", fmt.Sprint(len(archive.Data)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(archive.Data)
}

func (h *Handler) writeBackupView(w http.ResponseWriter, r *http.Request) {
	settings, _, err := h.accounts.GetBackupSettings(r.Context())
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	status, _, err := h.accounts.GetBackupStatus(r.Context())
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backup": h.backupView(settings, status)})
}

func (h *Handler) backupView(s account.BackupSettings, st account.BackupStatus) backupConfigView {
	view := backupConfigView{
		Enabled:       s.Enabled,
		RemoteURL:     s.RemoteURL,
		Branch:        s.Branch,
		AuthUsername:  s.AuthUsername,
		HasAuthToken:  s.AuthToken != "",
		HasPassphrase: s.Passphrase != "",
		DailyAtUTC:    s.DailyAtUTC,
		RetentionDays: s.RetentionDays,
		Supported:     h.backup.Supported(),
		Running:       h.backup.Running(),
		Status: backupStatusView{
			LastOK:      st.LastOK,
			LastError:   st.LastError,
			LastArchive: st.LastArchive,
			LastSize:    st.LastSize,
			LastCommit:  st.LastCommit,
			LastTrigger: st.LastTrigger,
		},
	}
	if next, ok := h.backup.NextRunAt(); ok {
		view.NextRunAt = &next
	}
	if !st.LastRunAt.IsZero() {
		at := st.LastRunAt
		view.Status.LastRunAt = &at
	}
	return view
}

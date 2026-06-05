package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sudiptadeb/memd/server/internal/account"
)

type sessionUser struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	SuperAdmin  bool   `json:"super_admin"`
}

type authContextKey struct{}

func (h *Handler) sessionAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	user, ok := h.currentUser(w, r)
	if !ok {
		httpErr(w, http.StatusUnauthorized, errors.New("not authenticated"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": sessionUserFromAccount(user)})
}

func (h *Handler) loginAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	user, err := h.accounts.AuthenticateLocal(r.Context(), body.Username, body.Password)
	if err != nil {
		httpErr(w, http.StatusUnauthorized, account.ErrInvalidCredentials)
		return
	}
	if err := h.sessions.Create(w, r, user.ID, user.Username, user.SuperAdmin); err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": sessionUserFromAccount(user)})
}

func (h *Handler) logoutAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	h.sessions.Clear(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := h.currentUser(w, r)
		if !ok {
			httpErr(w, http.StatusUnauthorized, errors.New("not authenticated"))
			return
		}
		ctx := context.WithValue(r.Context(), authContextKey{}, user)
		next(w, r.WithContext(ctx))
	}
}

func (h *Handler) requireSuperAdmin(next http.HandlerFunc) http.HandlerFunc {
	return h.requireUser(func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		if user == nil || !user.SuperAdmin {
			httpErr(w, http.StatusForbidden, errors.New("super admin required"))
			return
		}
		next(w, r)
	})
}

func (h *Handler) currentUser(w http.ResponseWriter, r *http.Request) (account.User, bool) {
	session, ok := h.sessions.Get(r)
	if !ok {
		return account.User{}, false
	}
	user, err := h.accounts.UserByID(r.Context(), session.UserID)
	if err != nil || user.Disabled {
		h.sessions.Clear(w, r)
		return account.User{}, false
	}
	return user, true
}

func userFromContext(ctx context.Context) *account.User {
	user, ok := ctx.Value(authContextKey{}).(account.User)
	if !ok {
		return nil
	}
	return &user
}

func sessionUserFromAccount(user account.User) sessionUser {
	return sessionUser{
		ID:          user.ID,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		SuperAdmin:  user.SuperAdmin,
	}
}

func (h *Handler) adminUsersAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		users, err := h.accounts.ListUsers(r.Context())
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": userViews(users)})
	case http.MethodPost:
		var body struct {
			Username    string `json:"username"`
			Password    string `json:"password"`
			DisplayName string `json:"display_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		user, err := h.accounts.CreateLocalUser(r.Context(), account.CreateUserInput{
			Username:    body.Username,
			Password:    body.Password,
			DisplayName: body.DisplayName,
		})
		if err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"user": userView(user)})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) adminUserAPI(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/users/"), "/")
	if tail == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	id, action, _ := strings.Cut(tail, "/")
	switch {
	case action == "" && r.Method == http.MethodPatch:
		var body struct {
			Disabled *bool `json:"disabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		if body.Disabled == nil {
			httpErr(w, http.StatusBadRequest, fmt.Errorf("disabled is required"))
			return
		}
		if err := h.accounts.SetUserDisabled(r.Context(), id, *body.Disabled); err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case action == "password" && r.Method == http.MethodPost:
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		if err := h.accounts.SetUserPassword(r.Context(), id, body.Password); err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) userDataAPI(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		httpErr(w, http.StatusUnauthorized, errors.New("not authenticated"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		bundle, err := h.accounts.ExportUserData(r.Context(), user.ID)
		if err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="memd-user-data-`+user.Username+`.json"`)
		writeJSON(w, http.StatusOK, bundle)
	case http.MethodPost:
		var bundle account.UserDataBundle
		if err := json.NewDecoder(r.Body).Decode(&bundle); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		replace := r.URL.Query().Get("replace") == "1" || r.URL.Query().Get("replace") == "true"
		if err := h.reg.ImportUserData(user.ID, bundle, replace); err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

type userViewData struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Disabled    bool   `json:"disabled"`
	SuperAdmin  bool   `json:"super_admin"`
	CreatedAt   string `json:"created_at"`
	LastLoginAt string `json:"last_login_at,omitempty"`
}

func userViews(users []account.User) []userViewData {
	out := make([]userViewData, 0, len(users))
	for _, user := range users {
		out = append(out, userView(user))
	}
	return out
}

func userView(user account.User) userViewData {
	out := userViewData{
		ID:          user.ID,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Disabled:    user.Disabled,
		SuperAdmin:  user.SuperAdmin,
		CreatedAt:   user.CreatedAt.Format("2006-01-02 15:04 MST"),
	}
	if user.LastLoginAt != nil {
		out.LastLoginAt = user.LastLoginAt.Format("2006-01-02 15:04 MST")
	}
	return out
}

func statusForAccountError(err error) int {
	switch {
	case errors.Is(err, account.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, account.ErrAlreadyExists):
		return http.StatusConflict
	case errors.Is(err, account.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, account.ErrNotInitialized):
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadRequest
	}
}

package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sudiptadeb/memd/server/internal/account"
)

type teamView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Role      string `json:"role"`
	CanManage bool   `json:"can_manage"`
	CanDelete bool   `json:"can_delete"`
}

type teamMemberView struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

type teamInviteView struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	MaxUses   *int   `json:"max_uses,omitempty"`
	UseCount  int    `json:"use_count"`
	ExpiresAt string `json:"expires_at,omitempty"`
	RevokedAt string `json:"revoked_at,omitempty"`
	CreatedAt string `json:"created_at"`
}

type invitePreviewView struct {
	TeamID    string `json:"team_id"`
	TeamName  string `json:"team_name"`
	Role      string `json:"role"`
	MaxUses   *int   `json:"max_uses,omitempty"`
	UseCount  int    `json:"use_count"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Valid     bool   `json:"valid"`
	Error     string `json:"error,omitempty"`
}

func (h *Handler) teamsAPI(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		httpErr(w, http.StatusUnauthorized, errors.New("not authenticated"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		teams, err := h.accounts.ListTeamsForUser(r.Context(), user.ID)
		if err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"teams": teamViews(teams)})
	case http.MethodPost:
		if user.SuperAdmin {
			httpErr(w, http.StatusForbidden, account.ErrForbidden)
			return
		}
		var body struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		team, err := h.accounts.CreateTeam(r.Context(), account.CreateTeamInput{
			Name:        body.Name,
			Slug:        body.Slug,
			OwnerUserID: user.ID,
		})
		if err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		team.Role = account.RoleOwner
		writeJSON(w, http.StatusOK, map[string]any{"team": teamViewFromTeam(team)})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) teamAPI(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		httpErr(w, http.StatusUnauthorized, errors.New("not authenticated"))
		return
	}
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/teams/"), "/")
	if tail == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	parts := strings.Split(tail, "/")
	teamID := parts[0]
	if len(parts) == 1 {
		h.singleTeamAPI(w, r, user, teamID)
		return
	}
	switch parts[1] {
	case "members":
		h.teamMembersAPI(w, r, user, teamID, parts[2:])
	case "invites":
		h.teamInvitesAPI(w, r, user, teamID, parts[2:])
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (h *Handler) singleTeamAPI(w http.ResponseWriter, r *http.Request, user *account.User, teamID string) {
	switch r.Method {
	case http.MethodGet:
		role, err := h.accounts.UserTeamRole(r.Context(), teamID, user.ID)
		if err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		team, err := h.accounts.TeamByID(r.Context(), teamID)
		if err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		team.Role = role
		writeJSON(w, http.StatusOK, map[string]any{"team": teamViewFromTeam(team)})
	case http.MethodPatch:
		var body struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		team, err := h.accounts.UpdateTeam(r.Context(), teamID, user.ID, body.Name, body.Slug)
		if err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		role, _ := h.accounts.UserTeamRole(r.Context(), teamID, user.ID)
		team.Role = role
		writeJSON(w, http.StatusOK, map[string]any{"team": teamViewFromTeam(team)})
	case http.MethodDelete:
		if err := h.accounts.DeleteTeam(r.Context(), teamID, user.ID); err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) teamMembersAPI(w http.ResponseWriter, r *http.Request, user *account.User, teamID string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			if ok, err := h.accounts.CanViewTeam(r.Context(), teamID, user.ID); err != nil || !ok {
				if err != nil {
					httpErr(w, statusForAccountError(err), err)
				} else {
					httpErr(w, http.StatusForbidden, account.ErrForbidden)
				}
				return
			}
			members, err := h.accounts.ListTeamMembers(r.Context(), teamID)
			if err != nil {
				httpErr(w, statusForAccountError(err), err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"members": teamMemberViews(members)})
		case http.MethodPost:
			var body struct {
				UserID   string `json:"user_id"`
				Username string `json:"username"`
				Role     string `json:"role"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				httpErr(w, http.StatusBadRequest, err)
				return
			}
			targetID := strings.TrimSpace(body.UserID)
			if targetID == "" && strings.TrimSpace(body.Username) != "" {
				target, err := h.accounts.UserByUsername(r.Context(), body.Username)
				if err != nil {
					httpErr(w, statusForAccountError(err), err)
					return
				}
				targetID = target.ID
			}
			if targetID == "" {
				httpErr(w, http.StatusBadRequest, errors.New("user_id or username is required"))
				return
			}
			if err := h.accounts.AddTeamMember(r.Context(), teamID, targetID, body.Role, user.ID); err != nil {
				httpErr(w, statusForAccountError(err), err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if len(rest) != 1 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	memberID := rest[0]
	switch r.Method {
	case http.MethodPatch:
		var body struct {
			Role string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		if err := h.accounts.SetTeamMemberRole(r.Context(), teamID, memberID, body.Role, user.ID); err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case http.MethodDelete:
		if err := h.accounts.RemoveTeamMember(r.Context(), teamID, memberID, user.ID); err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) teamInvitesAPI(w http.ResponseWriter, r *http.Request, user *account.User, teamID string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			invites, err := h.accounts.ListTeamInvites(r.Context(), teamID, user.ID)
			if err != nil {
				httpErr(w, statusForAccountError(err), err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"invites": teamInviteViews(invites)})
		case http.MethodPost:
			var body struct {
				Role      string `json:"role"`
				ExpiresAt string `json:"expires_at"`
				MaxUses   *int   `json:"max_uses"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				httpErr(w, http.StatusBadRequest, err)
				return
			}
			expiresAt, err := parseOptionalRFC3339(body.ExpiresAt)
			if err != nil {
				httpErr(w, http.StatusBadRequest, err)
				return
			}
			created, err := h.accounts.CreateTeamInvite(r.Context(), account.CreateTeamInviteInput{
				TeamID:          teamID,
				CreatedByUserID: user.ID,
				Role:            body.Role,
				ExpiresAt:       expiresAt,
				MaxUses:         body.MaxUses,
			})
			if err != nil {
				httpErr(w, statusForAccountError(err), err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"invite":     teamInviteViewFromInvite(created.Invite),
				"token":      created.Token,
				"invite_url": h.inviteURL(created.Token),
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if len(rest) == 2 && rest[1] == "revoke" && r.Method == http.MethodPost {
		if err := h.accounts.RevokeTeamInvite(r.Context(), rest[0], user.ID); err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (h *Handler) teamInviteAPI(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/team-invites/"), "/")
	if tail == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	tokenValue, action, _ := strings.Cut(tail, "/")
	switch {
	case action == "" && r.Method == http.MethodGet:
		invite, err := h.accounts.TeamInviteByToken(r.Context(), tokenValue)
		if err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		team, err := h.accounts.TeamByID(r.Context(), invite.TeamID)
		if err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"invite": invitePreview(invite, team)})
	case action == "accept" && r.Method == http.MethodPost:
		user, ok := h.currentUser(w, r)
		if !ok {
			httpErr(w, http.StatusUnauthorized, errors.New("not authenticated"))
			return
		}
		invite, err := h.accounts.AcceptTeamInvite(r.Context(), tokenValue, user.ID)
		if err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		team, err := h.accounts.TeamByID(r.Context(), invite.TeamID)
		if err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		role, _ := h.accounts.UserTeamRole(r.Context(), team.ID, user.ID)
		team.Role = role
		writeJSON(w, http.StatusOK, map[string]any{"team": teamViewFromTeam(team)})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) inviteURL(tokenValue string) string {
	return h.baseURL + "/?invite=" + tokenValue
}

func teamViews(teams []account.Team) []teamView {
	out := make([]teamView, 0, len(teams))
	for _, team := range teams {
		out = append(out, teamViewFromTeam(team))
	}
	return out
}

func teamViewFromTeam(team account.Team) teamView {
	return teamView{
		ID:        team.ID,
		Name:      team.Name,
		Slug:      team.Slug,
		Role:      team.Role,
		CanManage: team.Role == account.RoleOwner || team.Role == account.RoleAdmin,
		CanDelete: team.Role == account.RoleOwner,
	}
}

func teamMemberViews(members []account.TeamMember) []teamMemberView {
	out := make([]teamMemberView, 0, len(members))
	for _, member := range members {
		out = append(out, teamMemberView{
			UserID:      member.UserID,
			Username:    member.Username,
			DisplayName: member.DisplayName,
			Role:        member.Role,
		})
	}
	return out
}

func teamInviteViews(invites []account.TeamInvite) []teamInviteView {
	out := make([]teamInviteView, 0, len(invites))
	for _, invite := range invites {
		out = append(out, teamInviteViewFromInvite(invite))
	}
	return out
}

func teamInviteViewFromInvite(invite account.TeamInvite) teamInviteView {
	out := teamInviteView{
		ID:        invite.ID,
		Role:      invite.Role,
		MaxUses:   invite.MaxUses,
		UseCount:  invite.UseCount,
		CreatedAt: invite.CreatedAt.Format(time.RFC3339),
	}
	if invite.ExpiresAt != nil {
		out.ExpiresAt = invite.ExpiresAt.Format(time.RFC3339)
	}
	if invite.RevokedAt != nil {
		out.RevokedAt = invite.RevokedAt.Format(time.RFC3339)
	}
	return out
}

func invitePreview(invite account.TeamInvite, team account.Team) invitePreviewView {
	out := invitePreviewView{
		TeamID:    team.ID,
		TeamName:  team.Name,
		Role:      invite.Role,
		MaxUses:   invite.MaxUses,
		UseCount:  invite.UseCount,
		ExpiresAt: "",
		Valid:     true,
	}
	if invite.ExpiresAt != nil {
		out.ExpiresAt = invite.ExpiresAt.Format(time.RFC3339)
		if !time.Now().UTC().Before(invite.ExpiresAt.UTC()) {
			out.Valid = false
			out.Error = "invite has expired"
		}
	}
	if invite.RevokedAt != nil {
		out.Valid = false
		out.Error = "invite has been revoked"
	}
	if invite.MaxUses != nil && invite.UseCount >= *invite.MaxUses {
		out.Valid = false
		out.Error = "invite has reached its use limit"
	}
	return out
}

func parseOptionalRFC3339(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		t := time.Unix(n, 0).UTC()
		return &t, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("invalid expires_at: %w", err)
	}
	t = t.UTC()
	return &t, nil
}

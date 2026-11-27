package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/debkosh/memd/server/internal/account"
	"github.com/debkosh/memd/server/internal/logs"
)

// Notification channels (internal/notifychannel serves them at /notify). The
// dashboard lists the user's channels with their addresses, makes one, gives
// one a new token and deletes one. Each answer is the signed-in user's own
// channels only.

type notifyChannelView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	// URL is the channel's address with its token in it: senders POST to it
	// and the phone listens at <url>/ws, as with an ntfy.sh topic.
	URL string `json:"url"`
	// Messages is how many the server keeps for it now, in the list only.
	Messages int `json:"messages"`
}

func (h *Handler) notifyChannelView(c account.NotifyChannel) notifyChannelView {
	return notifyChannelView{
		ID:        c.ID,
		Name:      c.Name,
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
		URL:       h.baseURL + "/notify/" + c.Token,
	}
}

// GET /api/notify/channels lists the user's channels; POST makes one.
func (h *Handler) notifyChannelsAPI(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		httpErr(w, http.StatusUnauthorized, errors.New("not authenticated"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		channels, err := h.accounts.ListNotifyChannels(r.Context(), user.ID)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err)
			return
		}
		counts, err := h.accounts.NotifyCounts(r.Context(), user.ID)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err)
			return
		}
		views := make([]notifyChannelView, 0, len(channels))
		for _, c := range channels {
			v := h.notifyChannelView(c)
			v.Messages = counts[c.ID]
			views = append(views, v)
		}
		writeJSON(w, http.StatusOK, map[string]any{"channels": views})
	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		c, err := h.accounts.CreateNotifyChannel(r.Context(), user.ID, body.Name)
		if err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		logs.InfoUser(user.ID, "added notification channel %q (id=%s)", c.Name, c.ID)
		writeJSON(w, http.StatusOK, map[string]any{"channel": h.notifyChannelView(c)})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// POST /api/notify/channels/{id}/rotate gives a channel a new token, ending
// the old one for senders and the phone; DELETE /api/notify/channels/{id}
// deletes the channel and its messages.
func (h *Handler) notifyChannelAPI(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		httpErr(w, http.StatusUnauthorized, errors.New("not authenticated"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	id, action, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/notify/channels/"), "/")
	switch {
	case id != "" && action == "rotate" && r.Method == http.MethodPost:
		c, err := h.accounts.RotateNotifyChannelToken(r.Context(), user.ID, id)
		if err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		logs.InfoUser(user.ID, "rotated notification channel token %q (id=%s)", c.Name, c.ID)
		writeJSON(w, http.StatusOK, map[string]any{"channel": h.notifyChannelView(c)})
	case id != "" && action == "" && r.Method == http.MethodDelete:
		if err := h.accounts.DeleteNotifyChannel(r.Context(), user.ID, id); err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		logs.InfoUser(user.ID, "deleted notification channel id=%s", id)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

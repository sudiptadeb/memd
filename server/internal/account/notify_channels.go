package account

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/debkosh/memd/server/internal/token"
)

// A notification channel is one ntfy-style topic: senders publish to it and
// the owner's phone listens on it, both at /notify with its token (see
// internal/notifychannel). The token is the only lock, as a topic name is on
// ntfy.sh, so it is long and random; it is kept readable, like a sync
// bucket's, so the owner can copy the address again from the dashboard.
// Messages are stored as they came: senders seal them for the phone.

const (
	notifyChannelNameMax = 64
	// NotifyKeep is how long a message is kept for a phone that was away.
	NotifyKeep = 24 * time.Hour
	// NotifyKeepCount is the most messages kept per channel; older ones go.
	NotifyKeepCount = 500
)

// NotifyChannel is one of a user's notification channels.
type NotifyChannel struct {
	ID        string
	UserID    string
	Name      string
	Token     string
	CreatedAt time.Time
}

// NotifyMessage is one published message as kept: Seq orders a channel's
// messages, ID is what subscribers resume from, Time is unix seconds.
type NotifyMessage struct {
	Seq     int64
	ID      string
	Time    int64
	Message string
}

// CreateNotifyChannel makes a channel for userID, with a new token.
func (s *Store) CreateNotifyChannel(ctx context.Context, userID, name string) (NotifyChannel, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return NotifyChannel{}, errors.New("name is required")
	}
	if len(name) > notifyChannelNameMax {
		return NotifyChannel{}, fmt.Errorf("name is longer than %d characters", notifyChannelNameMax)
	}
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return NotifyChannel{}, err
	}
	if user.Disabled {
		return NotifyChannel{}, fmt.Errorf("%w: disabled users cannot make notification channels", ErrForbidden)
	}
	raw, err := token.New()
	if err != nil {
		return NotifyChannel{}, err
	}
	now := nowString()
	c := NotifyChannel{ID: newID("ntf"), UserID: userID, Name: name, Token: raw, CreatedAt: mustParseTime(now)}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO notify_channels(id, user_id, name, token, token_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		c.ID, c.UserID, c.Name, raw, syncTokenHash(raw), now)
	if err != nil {
		return NotifyChannel{}, err
	}
	return c, nil
}

// ListNotifyChannels returns the user's channels, oldest first.
func (s *Store) ListNotifyChannels(ctx context.Context, userID string) ([]NotifyChannel, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, name, token, created_at FROM notify_channels
		 WHERE user_id = ? ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotifyChannel
	for rows.Next() {
		c, err := scanNotifyChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// NotifyChannelByToken resolves a token to its channel. An unknown or
// replaced token, or one whose owner is disabled, is ErrNotFound.
func (s *Store) NotifyChannelByToken(ctx context.Context, rawToken string) (NotifyChannel, error) {
	c, err := scanNotifyChannel(s.db.QueryRowContext(ctx, `
		SELECT c.id, c.user_id, c.name, c.token, c.created_at FROM notify_channels c
		  JOIN users u ON u.id = c.user_id
		 WHERE c.token_hash = ? AND u.disabled = 0`, syncTokenHash(rawToken)))
	if errors.Is(err, sql.ErrNoRows) {
		return NotifyChannel{}, ErrNotFound
	}
	return c, err
}

// RotateNotifyChannelToken gives one of the user's channels a new token. The
// old one stops working at once, for senders and listeners.
func (s *Store) RotateNotifyChannelToken(ctx context.Context, userID, id string) (NotifyChannel, error) {
	raw, err := token.New()
	if err != nil {
		return NotifyChannel{}, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE notify_channels SET token = ?, token_hash = ? WHERE id = ? AND user_id = ?`,
		raw, syncTokenHash(raw), id, userID)
	if err != nil {
		return NotifyChannel{}, err
	}
	if err := rowsAffectedOrNotFound(res); err != nil {
		return NotifyChannel{}, err
	}
	return scanNotifyChannel(s.db.QueryRowContext(ctx,
		`SELECT id, user_id, name, token, created_at FROM notify_channels WHERE id = ?`, id))
}

// DeleteNotifyChannel removes one of the user's channels and its messages.
func (s *Store) DeleteNotifyChannel(ctx context.Context, userID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	res, err := tx.ExecContext(ctx, `DELETE FROM notify_channels WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if err := rowsAffectedOrNotFound(res); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM notify_messages WHERE channel = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// PublishNotify keeps message for channel and returns it with its id and
// time. Past NotifyKeepCount the channel's oldest messages go.
func (s *Store) PublishNotify(ctx context.Context, channel, message string) (NotifyMessage, error) {
	m := NotifyMessage{ID: notifyMessageID(), Time: time.Now().Unix(), Message: message}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NotifyMessage{}, err
	}
	defer rollback(tx)
	res, err := tx.ExecContext(ctx, `INSERT INTO notify_messages(channel, id, time, message) VALUES (?, ?, ?, ?)`,
		channel, m.ID, m.Time, m.Message)
	if err != nil {
		return NotifyMessage{}, err
	}
	if m.Seq, err = res.LastInsertId(); err != nil {
		return NotifyMessage{}, err
	}
	// Seq counts across all channels, so the cut is found within this one.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM notify_messages WHERE channel = ? AND seq < (
			SELECT seq FROM notify_messages WHERE channel = ? ORDER BY seq DESC LIMIT 1 OFFSET ?)`,
		channel, channel, NotifyKeepCount-1); err != nil {
		return NotifyMessage{}, err
	}
	return m, tx.Commit()
}

// NotifyAfter returns the channel's kept messages after since, oldest first.
// since is a message id, "all", or unix seconds; an id no longer kept (or
// never seen, as from another server) gives all of them, since the phone
// skips what it has had already.
func (s *Store) NotifyAfter(ctx context.Context, channel, since string) ([]NotifyMessage, error) {
	q := `SELECT seq, id, time, message FROM notify_messages WHERE channel = ? AND time >= ?`
	args := []any{channel, time.Now().Add(-NotifyKeep).Unix()}
	switch {
	case since == "" || since == "all":
	case isDigits(since):
		q += ` AND time >= ?`
		args = append(args, since)
	default:
		var seq int64
		err := s.db.QueryRowContext(ctx, `SELECT seq FROM notify_messages WHERE channel = ? AND id = ?`, channel, since).Scan(&seq)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		q += ` AND seq > ?`
		args = append(args, seq)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY seq`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotifyMessage
	for rows.Next() {
		var m NotifyMessage
		if err := rows.Scan(&m.Seq, &m.ID, &m.Time, &m.Message); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// NotifyCounts returns how many messages each of the user's channels keeps,
// by channel id; a channel with none is left out.
func (s *Store) NotifyCounts(ctx context.Context, userID string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.channel, count(*) FROM notify_messages m
		  JOIN notify_channels c ON c.id = m.channel
		 WHERE c.user_id = ? AND m.time >= ? GROUP BY m.channel`, userID, time.Now().Add(-NotifyKeep).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// CleanupNotify removes messages older than NotifyKeep, and any left by a
// channel or user that is gone.
func (s *Store) CleanupNotify(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM notify_messages
		 WHERE time < ? OR channel NOT IN (SELECT id FROM notify_channels)`, time.Now().Add(-NotifyKeep).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func scanNotifyChannel(row userScanner) (NotifyChannel, error) {
	var c NotifyChannel
	var created string
	if err := row.Scan(&c.ID, &c.UserID, &c.Name, &c.Token, &created); err != nil {
		return NotifyChannel{}, err
	}
	c.CreatedAt = mustParseTime(created)
	return c, nil
}

// notifyMessageID is 12 random letters and digits, as ntfy's ids are.
func notifyMessageID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	for i, b := range raw {
		raw[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(raw)
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

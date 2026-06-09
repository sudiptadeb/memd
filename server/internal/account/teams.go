package account

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sudiptadeb/memd/server/internal/token"
)

func (s *Store) CreateTeamForActor(ctx context.Context, in CreateTeamInput) (Team, error) {
	return s.CreateTeam(ctx, in)
}

func (s *Store) TeamByID(ctx context.Context, teamID string) (Team, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, slug, created_by_user_id, created_at, updated_at
		  FROM teams
		 WHERE id = ?`, teamID)
	team, err := scanTeam(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Team{}, ErrNotFound
	}
	return team, err
}

func (s *Store) UpdateTeam(ctx context.Context, teamID, actorUserID, name, slug string) (Team, error) {
	ok, err := s.CanManageTeam(ctx, teamID, actorUserID)
	if err != nil {
		return Team{}, err
	}
	if !ok {
		return Team{}, ErrForbidden
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Team{}, errors.New("team name is required")
	}
	slug = slugify(slug)
	if slug == "" {
		slug = slugify(name)
	}
	if slug == "" {
		return Team{}, errors.New("team slug is required")
	}
	now := nowString()
	res, err := s.db.ExecContext(ctx, `
		UPDATE teams
		   SET name = ?, slug = ?, updated_at = ?
		 WHERE id = ?`, name, slug, now, teamID)
	if err != nil {
		if isUniqueErr(err) {
			return Team{}, ErrAlreadyExists
		}
		return Team{}, err
	}
	if err := rowsAffectedOrNotFound(res); err != nil {
		return Team{}, err
	}
	return s.TeamByID(ctx, teamID)
}

func (s *Store) ListTeamsForUser(ctx context.Context, userID string) ([]Team, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, t.slug, t.created_by_user_id, tm.role, t.created_at, t.updated_at
		  FROM teams t
		  JOIN team_members tm ON tm.team_id = t.id
		 WHERE tm.user_id = ?
		 ORDER BY lower(t.name)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Team
	for rows.Next() {
		var t Team
		var created, updated string
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.CreatedByUserID, &t.Role, &created, &updated); err != nil {
			return nil, err
		}
		t.CreatedAt = mustParseTime(created)
		t.UpdatedAt = mustParseTime(updated)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ListTeamMembers(ctx context.Context, teamID string) ([]TeamMember, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tm.team_id, tm.user_id, u.username, u.display_name, tm.role, tm.created_at
		  FROM team_members tm
		  JOIN users u ON u.id = tm.user_id
		 WHERE tm.team_id = ?
		 ORDER BY
		       CASE tm.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'member' THEN 2 ELSE 3 END,
		       lower(u.username)`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TeamMember
	for rows.Next() {
		var m TeamMember
		var created string
		if err := rows.Scan(&m.TeamID, &m.UserID, &m.Username, &m.DisplayName, &m.Role, &created); err != nil {
			return nil, err
		}
		m.CreatedAt = mustParseTime(created)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) UserTeamRole(ctx context.Context, teamID, userID string) (string, error) {
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT role FROM team_members WHERE team_id = ? AND user_id = ?`, teamID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}

func (s *Store) SetTeamMemberRole(ctx context.Context, teamID, userID, role, actorUserID string) error {
	if !validRole(role) {
		return fmt.Errorf("invalid team role %q", role)
	}
	if err := s.EnsureRegularActiveUser(ctx, userID); err != nil {
		return err
	}
	currentRole, err := s.UserTeamRole(ctx, teamID, userID)
	if err != nil {
		return err
	}
	actorRole, err := s.UserTeamRole(ctx, teamID, actorUserID)
	if err != nil {
		return ErrForbidden
	}
	if actorRole != RoleOwner {
		if actorRole != RoleAdmin || currentRole == RoleOwner || currentRole == RoleAdmin || role == RoleOwner || role == RoleAdmin {
			return ErrForbidden
		}
	}
	if currentRole == RoleOwner && role != RoleOwner {
		ok, err := s.teamHasAnotherOwner(ctx, teamID, userID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: team must keep at least one owner", ErrForbidden)
		}
	}
	res, err := s.db.ExecContext(ctx, `UPDATE team_members SET role = ? WHERE team_id = ? AND user_id = ?`, role, teamID, userID)
	if err != nil {
		return err
	}
	return rowsAffectedOrNotFound(res)
}

func (s *Store) RemoveTeamMember(ctx context.Context, teamID, userID, actorUserID string) error {
	targetRole, err := s.UserTeamRole(ctx, teamID, userID)
	if err != nil {
		return err
	}
	actorRole, err := s.UserTeamRole(ctx, teamID, actorUserID)
	if err != nil && actorUserID != userID {
		return ErrForbidden
	}
	if actorUserID == userID && (targetRole == RoleMember || targetRole == RoleViewer) {
		actorRole = targetRole
	}
	if actorRole != RoleOwner {
		if actorUserID != userID || targetRole == RoleOwner || targetRole == RoleAdmin {
			return ErrForbidden
		}
	}
	if targetRole == RoleOwner {
		ok, err := s.teamHasAnotherOwner(ctx, teamID, userID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: team must keep at least one owner", ErrForbidden)
		}
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = ? AND user_id = ?`, teamID, userID)
	if err != nil {
		return err
	}
	return rowsAffectedOrNotFound(res)
}

func (s *Store) DeleteTeam(ctx context.Context, teamID, actorUserID string) error {
	role, err := s.UserTeamRole(ctx, teamID, actorUserID)
	if err != nil || role != RoleOwner {
		return ErrForbidden
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM teams WHERE id = ?`, teamID)
	if err != nil {
		return err
	}
	return rowsAffectedOrNotFound(res)
}

func (s *Store) CanManageTeam(ctx context.Context, teamID, userID string) (bool, error) {
	role, err := s.UserTeamRole(ctx, teamID, userID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return role == RoleOwner || role == RoleAdmin, nil
}

func (s *Store) CanManageTeamData(ctx context.Context, teamID, userID string) (bool, error) {
	return s.CanManageTeam(ctx, teamID, userID)
}

func (s *Store) CanViewTeam(ctx context.Context, teamID, userID string) (bool, error) {
	_, err := s.UserTeamRole(ctx, teamID, userID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) CreateTeamInvite(ctx context.Context, in CreateTeamInviteInput) (CreatedTeamInvite, error) {
	if err := validInviteRole(in.Role); err != nil {
		return CreatedTeamInvite{}, err
	}
	if in.MaxUses != nil && *in.MaxUses <= 0 {
		return CreatedTeamInvite{}, errors.New("max uses must be greater than zero")
	}
	actorRole, err := s.UserTeamRole(ctx, in.TeamID, in.CreatedByUserID)
	if err != nil {
		return CreatedTeamInvite{}, ErrForbidden
	}
	if actorRole != RoleOwner && actorRole != RoleAdmin {
		return CreatedTeamInvite{}, ErrForbidden
	}
	if in.Role == RoleAdmin && actorRole != RoleOwner {
		return CreatedTeamInvite{}, ErrForbidden
	}
	if err := s.EnsureRegularActiveUser(ctx, in.CreatedByUserID); err != nil {
		return CreatedTeamInvite{}, err
	}
	rawToken, err := token.New()
	if err != nil {
		return CreatedTeamInvite{}, err
	}
	now := nowString()
	invite := TeamInvite{
		ID:              newID("inv"),
		TeamID:          in.TeamID,
		Role:            in.Role,
		MaxUses:         in.MaxUses,
		ExpiresAt:       in.ExpiresAt,
		CreatedByUserID: in.CreatedByUserID,
		CreatedAt:       mustParseTime(now),
		UpdatedAt:       mustParseTime(now),
	}
	var maxUses any
	if in.MaxUses != nil {
		maxUses = *in.MaxUses
	}
	var expires any
	if in.ExpiresAt != nil {
		expires = in.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO team_invites(id, team_id, token_hash, role, max_uses, use_count, expires_at, revoked_at, created_by_user_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, NULL, ?, ?, ?)`,
		invite.ID, invite.TeamID, inviteTokenHash(rawToken), invite.Role, maxUses, expires, invite.CreatedByUserID, now, now)
	if err != nil {
		if isUniqueErr(err) {
			return CreatedTeamInvite{}, ErrAlreadyExists
		}
		return CreatedTeamInvite{}, err
	}
	return CreatedTeamInvite{Invite: invite, Token: rawToken}, nil
}

func (s *Store) ListTeamInvites(ctx context.Context, teamID, actorUserID string) ([]TeamInvite, error) {
	ok, err := s.CanManageTeam(ctx, teamID, actorUserID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrForbidden
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, team_id, role, max_uses, use_count, expires_at, revoked_at, created_by_user_id, created_at, updated_at
		  FROM team_invites
		 WHERE team_id = ?
		 ORDER BY created_at DESC`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TeamInvite
	for rows.Next() {
		invite, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, invite)
	}
	return out, rows.Err()
}

func (s *Store) RevokeTeamInvite(ctx context.Context, inviteID, actorUserID string) error {
	invite, err := s.teamInviteByID(ctx, inviteID)
	if err != nil {
		return err
	}
	ok, err := s.CanManageTeam(ctx, invite.TeamID, actorUserID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	now := nowString()
	res, err := s.db.ExecContext(ctx, `
		UPDATE team_invites
		   SET revoked_at = COALESCE(revoked_at, ?), updated_at = ?
		 WHERE id = ?`, now, now, inviteID)
	if err != nil {
		return err
	}
	return rowsAffectedOrNotFound(res)
}

func (s *Store) TeamInviteByToken(ctx context.Context, rawToken string) (TeamInvite, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, team_id, role, max_uses, use_count, expires_at, revoked_at, created_by_user_id, created_at, updated_at
		  FROM team_invites
		 WHERE token_hash = ?`, inviteTokenHash(rawToken))
	invite, err := scanInvite(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TeamInvite{}, ErrNotFound
	}
	return invite, err
}

func (s *Store) AcceptTeamInvite(ctx context.Context, rawToken, userID string) (TeamInvite, error) {
	if err := s.EnsureRegularActiveUser(ctx, userID); err != nil {
		return TeamInvite{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TeamInvite{}, err
	}
	defer rollback(tx)
	row := tx.QueryRowContext(ctx, `
		SELECT id, team_id, role, max_uses, use_count, expires_at, revoked_at, created_by_user_id, created_at, updated_at
		  FROM team_invites
		 WHERE token_hash = ?`, inviteTokenHash(rawToken))
	invite, err := scanInvite(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TeamInvite{}, ErrNotFound
	}
	if err != nil {
		return TeamInvite{}, err
	}
	var existingRole string
	err = tx.QueryRowContext(ctx, `SELECT role FROM team_members WHERE team_id = ? AND user_id = ?`, invite.TeamID, userID).Scan(&existingRole)
	if err == nil {
		return invite, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return TeamInvite{}, err
	}
	if err := validateInviteAcceptable(invite); err != nil {
		return TeamInvite{}, err
	}
	now := nowString()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO team_members(team_id, user_id, role, created_at, created_by_user_id)
		VALUES (?, ?, ?, ?, ?)`, invite.TeamID, userID, invite.Role, now, invite.CreatedByUserID); err != nil {
		return TeamInvite{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO team_invite_uses(invite_id, user_id, used_at)
		VALUES (?, ?, ?)`, invite.ID, userID, now); err != nil {
		return TeamInvite{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE team_invites
		   SET use_count = use_count + 1, updated_at = ?
		 WHERE id = ?`, now, invite.ID); err != nil {
		return TeamInvite{}, err
	}
	invite.UseCount++
	invite.UpdatedAt = mustParseTime(now)
	return invite, tx.Commit()
}

func (s *Store) EnsureRegularActiveUser(ctx context.Context, userID string) error {
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user.SuperAdmin {
		return fmt.Errorf("%w: super admin accounts cannot own team data", ErrForbidden)
	}
	if user.Disabled {
		return fmt.Errorf("%w: disabled users cannot use team data", ErrForbidden)
	}
	return nil
}

func (s *Store) canAddTeamMember(ctx context.Context, teamID, actorUserID, role string) error {
	actorRole, err := s.UserTeamRole(ctx, teamID, actorUserID)
	if err != nil {
		return ErrForbidden
	}
	if actorRole == RoleOwner {
		return nil
	}
	if actorRole == RoleAdmin && (role == RoleMember || role == RoleViewer) {
		return nil
	}
	return ErrForbidden
}

func (s *Store) teamHasAnotherOwner(ctx context.Context, teamID, exceptUserID string) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*)
		  FROM team_members
		 WHERE team_id = ? AND role = ? AND user_id <> ?`, teamID, RoleOwner, exceptUserID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) teamInviteByID(ctx context.Context, inviteID string) (TeamInvite, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, team_id, role, max_uses, use_count, expires_at, revoked_at, created_by_user_id, created_at, updated_at
		  FROM team_invites
		 WHERE id = ?`, inviteID)
	invite, err := scanInvite(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TeamInvite{}, ErrNotFound
	}
	return invite, err
}

func validInviteRole(role string) error {
	switch role {
	case RoleAdmin, RoleMember, RoleViewer:
		return nil
	default:
		return fmt.Errorf("invalid invite role %q", role)
	}
}

func validateInviteAcceptable(invite TeamInvite) error {
	if invite.RevokedAt != nil {
		return fmt.Errorf("%w: invite has been revoked", ErrForbidden)
	}
	if invite.ExpiresAt != nil && !time.Now().UTC().Before(invite.ExpiresAt.UTC()) {
		return fmt.Errorf("%w: invite has expired", ErrForbidden)
	}
	if invite.MaxUses != nil && invite.UseCount >= *invite.MaxUses {
		return fmt.Errorf("%w: invite has reached its use limit", ErrForbidden)
	}
	return nil
}

func inviteTokenHash(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

func scanTeam(row userScanner) (Team, error) {
	var t Team
	var created, updated string
	if err := row.Scan(&t.ID, &t.Name, &t.Slug, &t.CreatedByUserID, &created, &updated); err != nil {
		return Team{}, err
	}
	t.CreatedAt = mustParseTime(created)
	t.UpdatedAt = mustParseTime(updated)
	return t, nil
}

func scanInvite(row userScanner) (TeamInvite, error) {
	var invite TeamInvite
	var maxUses sql.NullInt64
	var expires, revoked sql.NullString
	var created, updated string
	if err := row.Scan(
		&invite.ID,
		&invite.TeamID,
		&invite.Role,
		&maxUses,
		&invite.UseCount,
		&expires,
		&revoked,
		&invite.CreatedByUserID,
		&created,
		&updated,
	); err != nil {
		return TeamInvite{}, err
	}
	if maxUses.Valid {
		v := int(maxUses.Int64)
		invite.MaxUses = &v
	}
	invite.ExpiresAt = parseOptionalTime(expires)
	invite.RevokedAt = parseOptionalTime(revoked)
	invite.CreatedAt = mustParseTime(created)
	invite.UpdatedAt = mustParseTime(updated)
	return invite, nil
}

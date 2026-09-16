package backup

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/storage"
)

// MinPassphraseLen is the shortest passphrase the settings form accepts.
const MinPassphraseLen = 12

// Validate checks settings as submitted by the admin form. The remote must be
// an https URL (the only transport the feature supports: token auth over
// HTTPS), the branch must be a legal ref name, the passphrase must meet the
// minimum length, and enabling requires a remote, token and passphrase.
func Validate(s account.BackupSettings) error {
	remote := strings.TrimSpace(s.RemoteURL)
	if remote != "" {
		if !strings.HasPrefix(strings.ToLower(remote), "https://") {
			return errors.New("repository URL must start with https://")
		}
		if err := storage.ValidateRemoteURL(remote); err != nil {
			return err
		}
	}
	if strings.TrimSpace(s.Branch) == "" {
		return errors.New("branch required")
	}
	if err := storage.ValidateBranch(strings.TrimSpace(s.Branch)); err != nil {
		return err
	}
	if s.Passphrase != "" && utf8.RuneCountInString(s.Passphrase) < MinPassphraseLen {
		return fmt.Errorf("passphrase must be at least %d characters", MinPassphraseLen)
	}
	if _, _, err := ParseDailyAt(s.DailyAtUTC); err != nil {
		return err
	}
	if s.RetentionDays < 0 {
		return errors.New("retention days must be 0 (keep all) or more")
	}
	if s.Enabled && !s.Configured() {
		return ErrNotConfigured
	}
	return nil
}

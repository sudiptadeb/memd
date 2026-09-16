package backup

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sudiptadeb/memd/server/internal/account"
)

func TestNextRunCrossesMidnight(t *testing.T) {
	cases := []struct {
		from string
		want string
	}{
		{"2026-09-16T23:30:00Z", "2026-09-17T03:00:00Z"}, // late evening: tomorrow
		{"2026-09-16T02:00:00Z", "2026-09-16T03:00:00Z"}, // before the slot: today
		{"2026-09-16T03:00:00Z", "2026-09-17T03:00:00Z"}, // exactly on the slot: strictly after
		{"2026-12-31T23:59:59Z", "2027-01-01T03:00:00Z"}, // year boundary
	}
	for _, c := range cases {
		from, _ := time.Parse(time.RFC3339, c.from)
		want, _ := time.Parse(time.RFC3339, c.want)
		got, err := NextRun("03:00", from)
		if err != nil {
			t.Fatalf("NextRun(%s): %v", c.from, err)
		}
		if !got.Equal(want) {
			t.Errorf("NextRun(%s) = %s, want %s", c.from, got, want)
		}
	}
	// A non-UTC "from" is still evaluated on the UTC clock.
	ist := time.FixedZone("IST", 5*3600+1800)
	from := time.Date(2026, 9, 17, 7, 0, 0, 0, ist) // 01:30Z
	got, err := NextRun("03:00", from)
	if err != nil || !got.Equal(time.Date(2026, 9, 17, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("NextRun from IST = %v, %v", got, err)
	}
	if _, err := NextRun("25:00", from); err == nil {
		t.Fatal("NextRun accepted 25:00")
	}
}

func TestPruneOldDeletesByNameTimestamp(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	names := []string{
		ArchiveName(now.Add(-40 * 24 * time.Hour)), // old: pruned
		ArchiveName(now.Add(-31 * 24 * time.Hour)), // just past 30 days: pruned
		ArchiveName(now.Add(-29 * 24 * time.Hour)), // kept
		ArchiveName(now), // kept
		"LATEST",         // never touched
		"notes.txt",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := PruneOld(dir, now, 30)
	if err != nil {
		t.Fatalf("PruneOld: %v", err)
	}
	if len(removed) != 2 || removed[0] != names[0] || removed[1] != names[1] {
		t.Fatalf("removed = %v", removed)
	}
	for _, n := range names[2:] {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s was removed", n)
		}
	}
	if removed, err := PruneOld(dir, now, 0); err != nil || len(removed) != 0 {
		t.Fatalf("PruneOld with retention 0 = %v, %v; want no-op", removed, err)
	}
}

func TestValidateRules(t *testing.T) {
	base := account.DefaultBackupSettings()
	if err := Validate(base); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	s := base
	s.RemoteURL = "git@github.com:org/repo.git"
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "https://") {
		t.Errorf("ssh remote accepted: %v", err)
	}
	s = base
	s.Passphrase = "short"
	if err := Validate(s); err == nil {
		t.Error("short passphrase accepted")
	}
	s = base
	s.Enabled = true
	if err := Validate(s); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("enabled without config: %v", err)
	}
	s = base
	s.Branch = "-x"
	if err := Validate(s); err == nil {
		t.Error("flag-like branch accepted")
	}
	s = base
	s.DailyAtUTC = "3pm"
	if err := Validate(s); err == nil {
		t.Error("bad time accepted")
	}
	s = base
	s.Enabled = true
	s.RemoteURL = "https://github.com/org/repo.git"
	s.AuthToken = "tok"
	s.Passphrase = "a passphrase long enough"
	if err := Validate(s); err != nil {
		t.Errorf("valid settings rejected: %v", err)
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable not available")
	}
}

func runGit(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out)
}

// TestRunPushesArchiveAndLatestToRemote: a full run against a local bare
// repository lands the archive, LATEST and README on the configured branch,
// records a successful status, and the pushed archive restores with the
// passphrase.
func TestRunPushesArchiveAndLatestToRemote(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	runGit(t, "init", "--bare", remote)

	store := openTestStore(t, filepath.Join(root, "live", "memd.db"))
	admin, err := store.CreateSuperAdmin(ctx, "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	// Settings go straight to the store: the https-only rule is enforced by
	// the admin API on save, and a local bare repository stands in for the
	// remote here.
	settings := account.BackupSettings{
		Enabled:       true,
		RemoteURL:     remote,
		Branch:        "main",
		AuthUsername:  "memd",
		AuthToken:     "unused-for-file-remotes",
		Passphrase:    "a passphrase long enough",
		DailyAtUTC:    "03:00",
		RetentionDays: 30,
	}
	if err := store.SaveBackupSettings(ctx, settings); err != nil {
		t.Fatalf("SaveBackupSettings: %v", err)
	}

	runner, err := New(store, Options{WorkDir: filepath.Join(root, "work")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	status, err := runner.Run(ctx, TriggerManual)
	if err != nil {
		t.Fatalf("Run: %v (status=%+v)", err, status)
	}
	if !status.LastOK || status.LastArchive == "" || status.LastCommit == "" || status.LastSize == 0 || status.LastTrigger != TriggerManual {
		t.Fatalf("status = %+v", status)
	}
	stored, ok, err := store.GetBackupStatus(ctx)
	if err != nil || !ok || stored.LastArchive != status.LastArchive {
		t.Fatalf("stored status = %+v ok=%v err=%v", stored, ok, err)
	}

	verify := filepath.Join(root, "verify")
	runGit(t, "clone", "--branch", "main", remote, verify)
	latest, err := os.ReadFile(filepath.Join(verify, repoDir, latestFile))
	if err != nil {
		t.Fatalf("LATEST missing from remote clone: %v", err)
	}
	if strings.TrimSpace(string(latest)) != status.LastArchive {
		t.Fatalf("LATEST = %q, want %q", latest, status.LastArchive)
	}
	if _, err := os.Stat(filepath.Join(verify, readmeFile)); err != nil {
		t.Fatalf("README.md missing from remote clone: %v", err)
	}
	blob, err := os.ReadFile(filepath.Join(verify, repoDir, status.LastArchive))
	if err != nil {
		t.Fatalf("archive missing from remote clone: %v", err)
	}
	if int64(len(blob)) != status.LastSize {
		t.Fatalf("pushed archive is %d bytes, status says %d", len(blob), status.LastSize)
	}
	subject := runGit(t, "-C", verify, "log", "-1", "--format=%s")
	if want := "memd backup " + status.LastArchive + " (manual)"; strings.TrimSpace(subject) != want {
		t.Fatalf("commit subject = %q, want %q", strings.TrimSpace(subject), want)
	}
	if short := runGit(t, "-C", verify, "rev-parse", "--short", "HEAD"); strings.TrimSpace(short) != status.LastCommit {
		t.Fatalf("remote HEAD %q != recorded commit %q", strings.TrimSpace(short), status.LastCommit)
	}

	dest := filepath.Join(root, "restored", "memd.db")
	if err := Restore(blob, settings.Passphrase, dest, false); err != nil {
		t.Fatalf("Restore pushed archive: %v", err)
	}
	restored := openTestStore(t, dest)
	if got, err := restored.UserByID(ctx, admin.ID); err != nil || got.Username != "admin" {
		t.Fatalf("restored user = %+v, %v", got, err)
	}
}

func TestRunRefusesWhenDisabledOrBusy(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := openTestStore(t, filepath.Join(root, "live", "memd.db"))
	runner, err := New(store, Options{WorkDir: filepath.Join(root, "work")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := runner.Run(ctx, TriggerManual); !errors.Is(err, ErrDisabled) {
		t.Fatalf("Run with nothing stored: err=%v, want ErrDisabled", err)
	}
	if err := store.SaveBackupSettings(ctx, account.BackupSettings{Enabled: true, Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, TriggerManual); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Run unconfigured: err=%v, want ErrNotConfigured", err)
	}
	runner.runMu.Lock()
	_, err = runner.Run(ctx, TriggerManual)
	runner.runMu.Unlock()
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Run while busy: err=%v, want ErrAlreadyRunning", err)
	}
	if _, ok := runner.NextRunAt(); ok {
		t.Fatal("NextRunAt reported a run before Start")
	}
}

// TestSchedulerRecomputesOnNotify: enabling backups and calling Notify makes
// the scheduler publish a next run without a restart; disabling clears it.
func TestSchedulerRecomputesOnNotify(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	store := openTestStore(t, filepath.Join(root, "live", "memd.db"))
	runner, err := New(store, Options{WorkDir: filepath.Join(root, "work")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runner.Start(ctx)
	waitNext := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, ok := runner.NextRunAt(); ok == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("scheduler never reached scheduled=%v", want)
	}
	waitNext(false)
	settings := account.BackupSettings{
		Enabled: true, RemoteURL: "https://example.com/r.git", Branch: "main",
		AuthToken: "tok", Passphrase: "a passphrase long enough", DailyAtUTC: "03:00",
	}
	if err := store.SaveBackupSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	runner.Notify()
	waitNext(true)
	next, _ := runner.NextRunAt()
	if next.Hour() != 3 || next.Minute() != 0 || !next.After(time.Now()) {
		t.Fatalf("next run = %s", next)
	}
	settings.Enabled = false
	if err := store.SaveBackupSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	runner.Notify()
	waitNext(false)
}

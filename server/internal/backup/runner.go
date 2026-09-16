package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/config"
	"github.com/sudiptadeb/memd/server/internal/logs"
	"github.com/sudiptadeb/memd/server/internal/storage"
)

const (
	// TriggerScheduled / TriggerManual label what started a run.
	TriggerScheduled = "scheduled"
	TriggerManual    = "manual"

	// repoDir is the directory inside the backup repository holding archives.
	repoDir = "backups"
	// latestFile names the pointer file holding the newest archive name.
	latestFile = "LATEST"
	readmeFile = "README.md"

	// gitQuiet is handed to the Git backend as WaitForWrites and SaveEvery so
	// neither the debounce nor the safety ticker ever fires: the runner
	// commits explicitly and closes the backend right after.
	gitQuiet = 24 * time.Hour * 365
)

var (
	// ErrAlreadyRunning is returned when a run is requested while another is
	// in progress.
	ErrAlreadyRunning = errors.New("backup already running")
	// ErrDisabled is returned when a run is requested with backups switched off.
	ErrDisabled = errors.New("backups are disabled")
	// ErrNotConfigured is returned when the stored settings lack a remote,
	// token or passphrase.
	ErrNotConfigured = errors.New("backup is not configured: repository URL, access token and passphrase are required")
)

// Options tunes a Runner; zero values pick the defaults.
type Options struct {
	// WorkDir is the git working copy for the backup repository. Defaults to
	// <workdirs root>/_backup.
	WorkDir string
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Runner builds archives and pushes them, on a daily schedule and on demand.
// Runs never overlap: the scheduler and manual requests share one lock, and a
// request that finds it held fails fast with ErrAlreadyRunning. Settings are
// re-read from the store at every run and every scheduler iteration, so
// changes saved in the admin console apply without a restart.
type Runner struct {
	store   *account.Store
	workDir string
	now     func() time.Time

	runMu   sync.Mutex // held for the duration of a run
	running bool
	stateMu sync.Mutex // guards running, nextRun
	nextRun time.Time

	notify chan struct{}
}

// New creates a Runner over the given store.
func New(store *account.Store, opts Options) (*Runner, error) {
	workDir := opts.WorkDir
	if workDir == "" {
		root, err := config.WorkdirsRoot()
		if err != nil {
			return nil, fmt.Errorf("backup workdir: %w", err)
		}
		workDir = filepath.Join(root, "_backup")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Runner{
		store:   store,
		workDir: workDir,
		now:     now,
		notify:  make(chan struct{}, 1),
	}, nil
}

// Supported reports whether this instance's database can be backed up.
func (r *Runner) Supported() bool { return Supported(r.store) }

// Running reports whether a run is in progress.
func (r *Runner) Running() bool {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	return r.running
}

// NextRunAt is the scheduler's next planned run; ok is false when nothing is
// scheduled (backups disabled, unsupported, or the scheduler is not started).
func (r *Runner) NextRunAt() (next time.Time, ok bool) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	return r.nextRun, !r.nextRun.IsZero()
}

// Notify tells the scheduler that the settings changed so it recomputes its
// next run immediately. Safe to call from any goroutine; coalesces.
func (r *Runner) Notify() {
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

// Build seals a fresh archive with the stored passphrase without touching the
// repository (the admin "download" action).
func (r *Runner) Build(ctx context.Context) (Archive, error) {
	settings, _, err := r.store.GetBackupSettings(ctx)
	if err != nil {
		return Archive{}, err
	}
	if settings.Passphrase == "" {
		return Archive{}, errors.New("no backup passphrase is configured")
	}
	return Build(ctx, r.store, settings.Passphrase)
}

// Check verifies that the repository in settings can be read, cloned and
// pushed to, using storage's non-destructive connection check.
func (r *Runner) Check(ctx context.Context, settings account.BackupSettings) (bool, string) {
	if strings.TrimSpace(settings.RemoteURL) == "" {
		return false, "repository URL required"
	}
	report := storage.CheckGitConnection(storage.GitConfig{
		RemoteURL:     settings.RemoteURL,
		Branch:        settings.Branch,
		AuthUsername:  settings.AuthUsername,
		AuthToken:     settings.AuthToken,
		WaitForWrites: gitQuiet,
		SaveEvery:     gitQuiet,
	})
	if report.OK {
		return true, "repository reachable: read, clone and push verified"
	}
	for _, c := range report.Checks {
		if !c.OK {
			return false, fmt.Sprintf("%s: %s", c.Label, c.Error)
		}
	}
	return false, "connection check failed"
}

// Run performs one backup: build the archive, place it in the repository
// working copy, prune old archives, commit and push, then record the outcome.
// The returned status is also persisted; err is non-nil when the run failed
// or could not start.
func (r *Runner) Run(ctx context.Context, trigger string) (account.BackupStatus, error) {
	if !r.runMu.TryLock() {
		return account.BackupStatus{}, ErrAlreadyRunning
	}
	defer r.runMu.Unlock()
	r.setRunning(true)
	defer r.setRunning(false)

	if trigger == "" {
		trigger = TriggerManual
	}
	settings, _, err := r.store.GetBackupSettings(ctx)
	if err != nil {
		return account.BackupStatus{}, err
	}
	if !settings.Enabled {
		return account.BackupStatus{}, ErrDisabled
	}
	if !settings.Configured() {
		return account.BackupStatus{}, ErrNotConfigured
	}
	if !r.Supported() {
		return account.BackupStatus{}, ErrUnsupported
	}

	started := r.now().UTC()
	status := account.BackupStatus{LastRunAt: started, LastTrigger: trigger}
	archive, commit, err := r.push(ctx, settings, trigger)
	if err != nil {
		status.LastError = err.Error()
		logs.Error("backup failed trigger=%s error=%v", trigger, err)
	} else {
		status.LastOK = true
		status.LastArchive = archive.Name
		status.LastSize = int64(len(archive.Data))
		status.LastCommit = commit
		logs.Info("backup pushed trigger=%s archive=%s bytes=%d commit=%s", trigger, archive.Name, len(archive.Data), commit)
	}
	if saveErr := r.store.SaveBackupStatus(ctx, status); saveErr != nil {
		logs.Warn("backup: record status: %v", saveErr)
		if err == nil {
			err = saveErr
		}
	}
	return status, err
}

func (r *Runner) push(ctx context.Context, settings account.BackupSettings, trigger string) (Archive, string, error) {
	archive, err := Build(ctx, r.store, settings.Passphrase)
	if err != nil {
		return Archive{}, "", err
	}
	g, err := storage.NewGit(storage.GitConfig{
		WorkDir:       r.workDir,
		RemoteURL:     settings.RemoteURL,
		Branch:        settings.Branch,
		AuthUsername:  settings.AuthUsername,
		AuthToken:     settings.AuthToken,
		AuthorName:    "memd backup",
		AuthorEmail:   "memd-backup@localhost",
		WaitForWrites: gitQuiet,
		SaveEvery:     gitQuiet,
	})
	if err != nil {
		return Archive{}, "", fmt.Errorf("open backup repository: %w", err)
	}
	// Close after every run so the askpass environment and credentials do
	// not linger in a long-lived process between daily runs.
	defer func() { _ = g.Close() }()
	if st := g.Status(); st.LastError != "" {
		return Archive{}, "", fmt.Errorf("sync backup repository: %s", st.LastError)
	}

	dir := filepath.Join(g.WorkDir(), repoDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Archive{}, "", err
	}
	if err := os.WriteFile(filepath.Join(dir, archive.Name), archive.Data, 0o644); err != nil {
		return Archive{}, "", err
	}
	if err := os.WriteFile(filepath.Join(dir, latestFile), []byte(archive.Name+"\n"), 0o644); err != nil {
		return Archive{}, "", err
	}
	if err := ensureReadme(g.WorkDir()); err != nil {
		return Archive{}, "", err
	}
	if settings.RetentionDays > 0 {
		pruned, err := PruneOld(dir, r.now(), settings.RetentionDays)
		if err != nil {
			return Archive{}, "", fmt.Errorf("prune old archives: %w", err)
		}
		if len(pruned) > 0 {
			logs.Info("backup pruned %d archive(s) older than %d days", len(pruned), settings.RetentionDays)
		}
	}
	if err := g.FlushWithMessage(fmt.Sprintf("memd backup %s (%s)", archive.Name, trigger)); err != nil {
		return Archive{}, "", fmt.Errorf("push: %w", err)
	}
	commit, err := g.HeadShort()
	if err != nil {
		return Archive{}, "", err
	}
	return archive, commit, nil
}

// PruneOld deletes archives in dir whose name-embedded timestamp is older
// than retentionDays before now, returning the names removed. Files that do
// not parse as archive names are left alone.
func PruneOld(dir string, now time.Time, retentionDays int) ([]string, error) {
	if retentionDays <= 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	cutoff := now.UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	var removed []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		created, ok := ParseArchiveName(e.Name())
		if !ok || !created.Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			return removed, err
		}
		removed = append(removed, e.Name())
	}
	sort.Strings(removed)
	return removed, nil
}

func ensureReadme(root string) error {
	path := filepath.Join(root, readmeFile)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	const body = `# memd backups

This repository is written by a memd instance's built-in backup.

- ` + "`backups/memd-<timestamp>.memdbk`" + ` — an encrypted archive of that instance's
  database (` + "`memd.db`" + `), a manifest, and the session-secret environment lines
  in force when it was made. Archives are sealed with AES-256-GCM under a key
  derived from the backup passphrase configured in the memd admin console.
- ` + "`backups/LATEST`" + ` — the file name of the newest archive.

Nothing here can be read without that passphrase. To restore:

    MEMD_BACKUP_PASSPHRASE=... memd backup restore --in backups/<name>.memdbk --out /path/to/memd.db

` + "`memd backup inspect --in <file>`" + ` prints an archive's manifest.
`
	return os.WriteFile(path, []byte(body), 0o644)
}

// NextRun returns the first occurrence of the daily "HH:MM" UTC time strictly
// after from.
func NextRun(dailyAtUTC string, from time.Time) (time.Time, error) {
	hh, mm, err := ParseDailyAt(dailyAtUTC)
	if err != nil {
		return time.Time{}, err
	}
	from = from.UTC()
	next := time.Date(from.Year(), from.Month(), from.Day(), hh, mm, 0, 0, time.UTC)
	if !next.After(from) {
		next = next.Add(24 * time.Hour)
	}
	return next, nil
}

// ParseDailyAt parses a "HH:MM" 24-hour clock string.
func ParseDailyAt(s string) (hour, minute int, err error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, 0, fmt.Errorf("daily time must be HH:MM (24-hour UTC), got %q", s)
	}
	return t.Hour(), t.Minute(), nil
}

// Start launches the scheduler goroutine. It re-reads the settings at every
// iteration, sleeps until the next scheduled time (or a Notify), and runs a
// scheduled backup when enabled and supported. It exits when ctx is done.
func (r *Runner) Start(ctx context.Context) {
	go r.loop(ctx)
}

func (r *Runner) loop(ctx context.Context) {
	// lastFired guards against a timer that wakes marginally before its
	// target, which would otherwise schedule the same slot twice.
	var lastFired time.Time
	for {
		settings, _, err := r.store.GetBackupSettings(ctx)
		if err != nil {
			logs.Warn("backup scheduler: read settings: %v", err)
		}
		active := err == nil && settings.Enabled && r.Supported()

		var (
			timerC <-chan time.Time
			timer  *time.Timer
		)
		if active {
			from := r.now()
			if !lastFired.IsZero() && !from.After(lastFired) {
				from = lastFired
			}
			next, err := NextRun(settings.DailyAtUTC, from)
			if err != nil {
				logs.Warn("backup scheduler: %v; not scheduling", err)
				r.setNext(time.Time{})
			} else {
				r.setNext(next)
				timer = time.NewTimer(next.Sub(r.now()))
				timerC = timer.C
				lastFired = next
			}
		} else {
			r.setNext(time.Time{})
		}

		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			r.setNext(time.Time{})
			return
		case <-r.notify:
			if timer != nil {
				timer.Stop()
			}
			lastFired = time.Time{}
		case <-timerC:
			// Run logs its own outcome; ErrAlreadyRunning means a manual run
			// is in flight, which covers this slot.
			_, _ = r.Run(ctx, TriggerScheduled)
		}
	}
}

func (r *Runner) setRunning(on bool) {
	r.stateMu.Lock()
	r.running = on
	r.stateMu.Unlock()
}

func (r *Runner) setNext(t time.Time) {
	r.stateMu.Lock()
	r.nextRun = t
	r.stateMu.Unlock()
}

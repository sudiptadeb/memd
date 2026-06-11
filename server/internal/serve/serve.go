package serve

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/doctrine"
	"github.com/sudiptadeb/memd/server/internal/logs"
	"github.com/sudiptadeb/memd/server/internal/mcp"
	"github.com/sudiptadeb/memd/server/internal/registry"
	"github.com/sudiptadeb/memd/server/internal/ui"
	"github.com/sudiptadeb/memd/server/internal/version"
	"golang.org/x/term"
)

type Options struct {
	Port                     int
	InitDB                   bool
	CreateSuperAdminUsername string
	CreateSuperAdminPassword string
	Stdin                    *os.File
	Stdout                   io.Writer
}

func Run(port int) error {
	return RunOptions(Options{Port: port, Stdin: os.Stdin, Stdout: os.Stdout})
}

func RunOptions(opts Options) error {
	if opts.Port == 0 {
		opts.Port = 7878
	}
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	ctx := context.Background()
	accountStore, err := openAccountStore(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = accountStore.Close() }()
	if err := ensureAccountDB(ctx, accountStore, opts); err != nil {
		return err
	}

	reg, err := registry.NewAccountBacked(ctx, accountStore)
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", opts.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	baseURL := fmt.Sprintf("http://%s", addr)

	sessions, err := ui.NewSessionManager(os.Getenv("MEMD_SESSION_SECRET"), sessionMaxAge())
	if err != nil {
		return fmt.Errorf("init sessions: %w", err)
	}
	if sessions.Ephemeral() {
		logs.Warn("MEMD_SESSION_SECRET is not set; using an ephemeral session key (logins reset on restart)")
	}
	oidcMgr := ui.LoadOIDCFromStore(ctx, accountStore)
	if oidcMgr.Enabled() {
		logs.Info("OIDC SSO enabled")
	}

	mux := http.NewServeMux()
	mcpSrv := mcp.New(reg, doctrine.Text, "memd", version.Value)
	mcpSrv.Mount(mux, "/mcp/")
	mcpSrv.MountHTTP(mux, "/http/")
	ui.New(reg, accountStore, baseURL, sessions, oidcMgr).Mount(mux)

	fmt.Fprintf(opts.Stdout, "memd web UI:  %s\n", baseURL)
	fmt.Fprintln(opts.Stdout, "Press Ctrl-C to stop.")
	logs.Info("memd %s started on %s", version.Value, baseURL)
	for _, d := range reg.Directories() {
		logs.Info("loaded directory %q (id=%s, backend=%s)", d.Name, d.ID, d.Backend)
	}
	for _, c := range reg.Connectors() {
		logs.Info("loaded connector %q (id=%s)", c.Name, c.ID)
	}

	srv := &http.Server{
		Handler:           withSecurityHeaders(withMaxBody(mux, maxRequestBody)),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-ctx.Done():
		fmt.Fprintln(opts.Stdout, "\nshutting down…")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		shutdownErr := srv.Shutdown(shutdownCtx)
		if err := reg.Close(); err != nil {
			logs.Warn("registry close: %v", err)
		}
		return shutdownErr
	case err := <-errCh:
		if err := reg.Close(); err != nil {
			logs.Warn("registry close: %v", err)
		}
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}

// maxRequestBody caps the size of any single request body. It is generous
// enough for large memory writes while preventing a client from streaming an
// unbounded body into memory.
const maxRequestBody = 32 << 20 // 32 MiB

// withMaxBody wraps every request body in an http.MaxBytesReader so handlers
// that decode the body can never be forced to read more than limit bytes.
func withMaxBody(next http.Handler, limit int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy is intentionally tight: everything the UI loads is
// same-origin. 'unsafe-eval' is required because Alpine.js evaluates its
// directive expressions via the Function constructor; 'unsafe-inline' covers
// Alpine's style/class attribute bindings. frame-ancestors and base-uri lock
// out clickjacking and base-tag hijacking.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' 'unsafe-eval'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'"

// withSecurityHeaders adds defence-in-depth headers to every response: a
// content-security policy, MIME-sniffing and framing protections, and a
// conservative referrer policy.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// sessionMaxAge is the absolute session-lifetime cap, overridable via
// MEMD_SESSION_MAX_AGE (a Go duration like "12h"). Defaults to 24h.
func sessionMaxAge() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("MEMD_SESSION_MAX_AGE")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
		logs.Warn("invalid MEMD_SESSION_MAX_AGE %q; using default", raw)
	}
	return 24 * time.Hour
}

func openAccountStore(ctx context.Context) (*account.Store, error) {
	cfg, err := account.ConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("account database config: %w", err)
	}
	store, err := account.Open(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open account database: %w", err)
	}
	return store, nil
}

func ensureAccountDB(ctx context.Context, store *account.Store, opts Options) error {
	reader := bufio.NewReader(opts.Stdin)
	initialized, err := store.IsInitialized(ctx)
	if err != nil {
		return fmt.Errorf("check account database: %w", err)
	}

	createdRequestedAdmin := false
	if !initialized {
		fmt.Fprintf(opts.Stdout, "memd account database is not initialized (%s).\n", databaseDescription(store.Config()))
		shouldInit := opts.InitDB
		if !shouldInit {
			if !isInteractive(opts.Stdin) {
				return fmt.Errorf("account database is not initialized; run `memd serve --init-db` from a terminal or set MEMD_INIT_DB=1")
			}
			yes, err := confirm(reader, opts.Stdout, "Initialize it now? [y/N] ")
			if err != nil {
				return err
			}
			shouldInit = yes
		}
		if !shouldInit {
			return account.ErrNotInitialized
		}
		if err := store.Init(ctx); err != nil {
			return fmt.Errorf("initialize account database: %w", err)
		}
		fmt.Fprintln(opts.Stdout, "Initialized account database.")
		if opts.CreateSuperAdminUsername != "" {
			if err := createSuperAdminFromOptions(ctx, store, reader, opts); err != nil {
				return err
			}
			createdRequestedAdmin = true
		} else {
			if err := promptInitialSuperAdmin(ctx, store, reader, opts); err != nil {
				return err
			}
		}
	}
	if initialized {
		if err := store.Init(ctx); err != nil {
			return fmt.Errorf("migrate account database: %w", err)
		}
	}

	if opts.CreateSuperAdminUsername != "" && !createdRequestedAdmin {
		if err := createSuperAdminFromOptions(ctx, store, reader, opts); err != nil {
			return err
		}
	}

	hasAdmin, err := store.HasSuperAdmin(ctx)
	if err != nil {
		return fmt.Errorf("check super admin: %w", err)
	}
	if !hasAdmin {
		if !isInteractive(opts.Stdin) && opts.CreateSuperAdminUsername == "" {
			return fmt.Errorf("account database has no super admin; start from a terminal or pass --create-super-admin with MEMD_CREATE_SUPER_ADMIN_PASSWORD")
		}
		if err := promptInitialSuperAdmin(ctx, store, reader, opts); err != nil {
			return err
		}
	}
	return nil
}

func createSuperAdminFromOptions(ctx context.Context, store *account.Store, reader *bufio.Reader, opts Options) error {
	username := strings.TrimSpace(opts.CreateSuperAdminUsername)
	if username == "" {
		return nil
	}
	password := opts.CreateSuperAdminPassword
	if password == "" {
		var err error
		password, err = promptPassword(reader, opts)
		if err != nil {
			return err
		}
	}
	user, err := store.CreateSuperAdmin(ctx, username, password)
	if err != nil {
		return fmt.Errorf("create super admin %q: %w", username, err)
	}
	fmt.Fprintf(opts.Stdout, "Created super admin %q (id=%s).\n", user.Username, user.ID)
	return nil
}

func promptInitialSuperAdmin(ctx context.Context, store *account.Store, reader *bufio.Reader, opts Options) error {
	if !isInteractive(opts.Stdin) && opts.CreateSuperAdminUsername == "" {
		return fmt.Errorf("cannot prompt for the initial super admin without a terminal")
	}
	username := strings.TrimSpace(opts.CreateSuperAdminUsername)
	var err error
	if username == "" {
		username, err = promptLine(reader, opts.Stdout, "Super admin username: ")
		if err != nil {
			return err
		}
	}
	password := opts.CreateSuperAdminPassword
	if password == "" {
		password, err = promptPassword(reader, opts)
		if err != nil {
			return err
		}
	}
	user, err := store.CreateSuperAdmin(ctx, username, password)
	if err != nil {
		return fmt.Errorf("create initial super admin: %w", err)
	}
	fmt.Fprintf(opts.Stdout, "Created initial super admin %q (id=%s).\n", user.Username, user.ID)
	return nil
}

func promptPassword(reader *bufio.Reader, opts Options) (string, error) {
	if !isInteractive(opts.Stdin) {
		return "", fmt.Errorf("super admin password is required; pass --super-admin-password or set MEMD_CREATE_SUPER_ADMIN_PASSWORD")
	}
	first, err := readSecret(opts.Stdin, opts.Stdout, "Super admin password: ")
	if err != nil {
		return "", err
	}
	second, err := readSecret(opts.Stdin, opts.Stdout, "Confirm password: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", fmt.Errorf("passwords do not match")
	}
	if strings.TrimSpace(first) == "" {
		return "", fmt.Errorf("password is required")
	}
	_ = reader
	return first, nil
}

func readSecret(in *os.File, out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, prompt)
	b, err := term.ReadPassword(int(in.Fd()))
	fmt.Fprintln(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func confirm(reader *bufio.Reader, out io.Writer, prompt string) (bool, error) {
	line, err := promptLine(reader, out, prompt)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func promptLine(reader *bufio.Reader, out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, prompt)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func isInteractive(f *os.File) bool {
	return f != nil && term.IsTerminal(int(f.Fd()))
}

func databaseDescription(cfg account.DBConfig) string {
	if cfg.SQLitePath != "" {
		return cfg.SQLitePath
	}
	return cfg.Driver
}

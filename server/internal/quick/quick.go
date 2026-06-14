package quick

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sudiptadeb/memd/server/internal/config"
	"github.com/sudiptadeb/memd/server/internal/doctrine"
	"github.com/sudiptadeb/memd/server/internal/feature"
	"github.com/sudiptadeb/memd/server/internal/mcp"
	"github.com/sudiptadeb/memd/server/internal/registry"
	"github.com/sudiptadeb/memd/server/internal/version"
)

func Run(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("directory %q: %w", abs, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", abs)
	}

	reg := registry.NewEphemeral()
	dirName := filepath.Base(abs)
	dirID, err := reg.AddDirectory(config.Directory{
		Name:        dirName,
		Description: "Quick mode memory: " + dirName,
		Backend:     "local",
		LocalPath:   abs,
	})
	if err != nil {
		return fmt.Errorf("open directory: %w", err)
	}
	conn, err := reg.AddConnector(config.Connector{
		Name:         "quick",
		DirectoryIDs: []string{dirID},
		Write:        true,
	})
	if err != nil {
		return fmt.Errorf("create connector: %w", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	features := feature.Builtins()
	live := doctrine.NewLive()
	live.Register(doctrine.GlobalID, "Global doctrine", doctrine.Text)
	feature.RegisterDoctrines(live, features)
	mcpSrv := mcp.New(reg, live, features, "memd", version.Value)
	mux := http.NewServeMux()
	mcpSrv.Mount(mux, "/mcp/")
	mcpSrv.MountHTTP(mux, "/http/")

	fmt.Printf("memd serving %s\n\n  http://127.0.0.1:%d/mcp/%s\n\n  URL: http://127.0.0.1:%d/mcp\n  Authorization: Bearer %s\n\nPress Ctrl-C to stop.\n",
		abs, port, conn.Token, port, conn.Token)

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, 32<<20) // 32 MiB
			}
			mux.ServeHTTP(w, r)
		}),
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
		fmt.Println("\nshutting down…")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}

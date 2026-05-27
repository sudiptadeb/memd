package serve

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/sudiptadeb/memd/server/internal/doctrine"
	"github.com/sudiptadeb/memd/server/internal/logs"
	"github.com/sudiptadeb/memd/server/internal/mcp"
	"github.com/sudiptadeb/memd/server/internal/registry"
	"github.com/sudiptadeb/memd/server/internal/ui"
	"github.com/sudiptadeb/memd/server/internal/version"
)

func Run(port int) error {
	reg, err := registry.NewPersistent()
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	baseURL := fmt.Sprintf("http://%s", addr)

	mux := http.NewServeMux()
	mcpSrv := mcp.New(reg, doctrine.Text, "memd", version.Value)
	mcpSrv.Mount(mux, "/mcp/")
	mcpSrv.MountHTTP(mux, "/http/")
	ui.New(reg, baseURL).Mount(mux)

	fmt.Printf("memd web UI:  %s\n", baseURL)
	fmt.Println("Press Ctrl-C to stop.")
	logs.Info("memd %s started on %s", version.Value, baseURL)
	for _, d := range reg.Directories() {
		logs.Info("loaded directory %q (id=%s, backend=%s)", d.Name, d.ID, d.Backend)
	}
	for _, c := range reg.Connectors() {
		logs.Info("loaded connector %q (id=%s)", c.Name, c.ID)
	}

	srv := &http.Server{Handler: mux}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-ctx.Done():
		fmt.Println("\nshutting down…")
		shutdownErr := srv.Shutdown(context.Background())
		if err := reg.Close(); err != nil {
			logs.Warn("registry close: %v", err)
		}
		return shutdownErr
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}

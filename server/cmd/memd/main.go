package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/sudiptadeb/memd/server/internal/migrate"
	"github.com/sudiptadeb/memd/server/internal/quick"
	"github.com/sudiptadeb/memd/server/internal/serve"
	"github.com/sudiptadeb/memd/server/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "memd:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "data":
		return runData(args[1:])
	case "version", "--version", "-v":
		fmt.Println("memd", version.Value)
		return nil
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		if len(args) > 1 {
			return errors.New("quick mode takes exactly one directory argument; use `memd serve` for configured mode")
		}
		return quick.Run(args[0])
	}
}

func runData(args []string) error {
	if len(args) == 0 {
		return errors.New("data command requires export, import, or export-legacy-config")
	}
	switch args[0] {
	case "export":
		fs := flag.NewFlagSet("data export", flag.ContinueOnError)
		user := fs.String("user", "", "username whose directories/connectors should be exported")
		out := fs.String("out", "-", "output JSON file, or - for stdout")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*user) == "" {
			return errors.New("--user is required")
		}
		return migrate.Export(context.Background(), migrate.UserDataOptions{Username: *user, Out: *out})
	case "import":
		fs := flag.NewFlagSet("data import", flag.ContinueOnError)
		user := fs.String("user", "", "username that should receive the imported directories/connectors")
		in := fs.String("in", "-", "input JSON file, or - for stdin")
		replace := fs.Bool("replace", false, "replace this user's existing directories/connectors")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*user) == "" {
			return errors.New("--user is required")
		}
		return migrate.Import(context.Background(), migrate.UserDataOptions{Username: *user, In: *in, Replace: *replace})
	case "export-legacy-config":
		fs := flag.NewFlagSet("data export-legacy-config", flag.ContinueOnError)
		out := fs.String("out", "-", "output JSON file, or - for stdout")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return migrate.ExportLegacyConfig(context.Background(), *out)
	default:
		return fmt.Errorf("unknown data command: %s", args[0])
	}
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fs.Int("port", 7878, "port for the local web UI and MCP endpoint")
	initDB := fs.Bool("init-db", envBool("MEMD_INIT_DB"), "initialize the account database if it is missing")
	createSuperAdmin := fs.String("create-super-admin", strings.TrimSpace(os.Getenv("MEMD_CREATE_SUPER_ADMIN_USERNAME")), "create a local super admin account before serving")
	superAdminPassword := fs.String("super-admin-password", os.Getenv("MEMD_CREATE_SUPER_ADMIN_PASSWORD"), "password for --create-super-admin; prefer the env var or interactive prompt")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return serve.RunOptions(serve.Options{
		Port:                     *port,
		InitDB:                   *initDB,
		CreateSuperAdminUsername: *createSuperAdmin,
		CreateSuperAdminPassword: *superAdminPassword,
		Stdin:                    os.Stdin,
		Stdout:                   os.Stdout,
	})
}

func printUsage() {
	fmt.Println(`Usage:
  memd <directory>                         quick mode — serve one directory, ephemeral URL
  memd serve [flags]                       configured mode — web UI for multiple directories
  memd data export --user USER --out FILE
  memd data import --user USER --in FILE [--replace]
  memd data export-legacy-config --out FILE
  memd version

serve flags:
  --port PORT                  port to listen on (default 7878)
  --init-db                    initialize the account database if needed
  --create-super-admin USER    create a local super admin before serving
  --super-admin-password PASS  password for --create-super-admin (prefer the env var or prompt)

Configured mode uses MEMD_DATABASE_URL for account metadata. If unset, it uses a cgo-free SQLite database in the memd config directory.
Both modes bind to 127.0.0.1.`)
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sudiptadeb/memd/server/internal/backup"
	"github.com/sudiptadeb/memd/server/internal/migrate"
	"github.com/sudiptadeb/memd/server/internal/quick"
	"github.com/sudiptadeb/memd/server/internal/serve"
	"github.com/sudiptadeb/memd/server/internal/version"
	"golang.org/x/term"
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
	case "backup":
		return runBackup(args[1:])
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

func runBackup(args []string) error {
	if len(args) == 0 {
		return errors.New("backup command requires restore or inspect")
	}
	switch args[0] {
	case "restore":
		fs := flag.NewFlagSet("backup restore", flag.ContinueOnError)
		in := fs.String("in", "", "encrypted archive to restore (.memdbk)")
		out := fs.String("out", "", "destination path for the restored memd.db")
		force := fs.Bool("force", false, "overwrite an existing destination file")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*in) == "" || strings.TrimSpace(*out) == "" {
			return errors.New("--in and --out are required")
		}
		blob, err := os.ReadFile(*in)
		if err != nil {
			return err
		}
		passphrase, err := backupPassphrase()
		if err != nil {
			return err
		}
		contents, err := backup.Inspect(blob, passphrase)
		if err != nil {
			return err
		}
		if err := backup.Restore(blob, passphrase, *out, *force); err != nil {
			return err
		}
		fmt.Printf("restored %s (%d bytes; archive created %s by memd %s on %s)\n",
			*out, contents.DBSize, contents.Manifest.CreatedAt.Format(time.RFC3339), contents.Manifest.MemdVersion, contents.Manifest.Hostname)
		if contents.HasEnv {
			fmt.Println("environment lines written next to it as env.restored; merge them into the service environment if wanted")
		}
		return nil
	case "inspect":
		fs := flag.NewFlagSet("backup inspect", flag.ContinueOnError)
		in := fs.String("in", "", "encrypted archive to inspect (.memdbk)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*in) == "" {
			return errors.New("--in is required")
		}
		blob, err := os.ReadFile(*in)
		if err != nil {
			return err
		}
		passphrase, err := backupPassphrase()
		if err != nil {
			return err
		}
		contents, err := backup.Inspect(blob, passphrase)
		if err != nil {
			return err
		}
		m := contents.Manifest
		fmt.Printf("format:        %s v%d\n", m.Format, m.Version)
		fmt.Printf("created_at:    %s\n", m.CreatedAt.Format(time.RFC3339))
		fmt.Printf("memd_version:  %s\n", m.MemdVersion)
		fmt.Printf("hostname:      %s\n", m.Hostname)
		fmt.Printf("memd.db:       %d bytes\n", contents.DBSize)
		fmt.Printf("env:           %v\n", contents.HasEnv)
		return nil
	default:
		return fmt.Errorf("unknown backup command: %s", args[0])
	}
}

// backupPassphrase reads the archive passphrase from MEMD_BACKUP_PASSPHRASE,
// falling back to a no-echo terminal prompt when stdin is a terminal.
func backupPassphrase() (string, error) {
	if v := os.Getenv("MEMD_BACKUP_PASSPHRASE"); v != "" {
		return v, nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("MEMD_BACKUP_PASSPHRASE is not set and stdin is not a terminal")
	}
	fmt.Fprint(os.Stderr, "Backup passphrase: ")
	raw, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", errors.New("passphrase required")
	}
	return string(raw), nil
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
  memd backup restore --in FILE --out PATH [--force]
  memd backup inspect --in FILE
  memd version

serve flags:
  --port PORT                  port to listen on (default 7878)
  --init-db                    initialize the account database if needed
  --create-super-admin USER    create a local super admin before serving
  --super-admin-password PASS  password for --create-super-admin (prefer the env var or prompt)

backup commands read the archive passphrase from MEMD_BACKUP_PASSPHRASE, or prompt for it when stdin is a terminal.
restore writes the archive's memd.db to PATH (refusing to overwrite without --force) and any captured environment lines next to it as env.restored.

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

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

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

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fs.Int("port", 7878, "port for the local web UI and MCP endpoint")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return serve.Run(*port)
}

func printUsage() {
	fmt.Println(`Usage:
  memd <directory>             quick mode — serve one directory, ephemeral URL
  memd serve [--port PORT]     configured mode — web UI for multiple directories
  memd version

Both modes bind to 127.0.0.1.`)
}

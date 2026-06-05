package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/config"
)

type UserDataOptions struct {
	Username string
	In       string
	Out      string
	Replace  bool
}

func Export(ctx context.Context, opts UserDataOptions) error {
	store, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	user, err := store.UserByUsername(ctx, opts.Username)
	if err != nil {
		return fmt.Errorf("find user %q: %w", opts.Username, err)
	}
	bundle, err := store.ExportUserData(ctx, user.ID)
	if err != nil {
		return err
	}
	return writeJSON(opts.Out, bundle)
}

func Import(ctx context.Context, opts UserDataOptions) error {
	store, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	user, err := store.UserByUsername(ctx, opts.Username)
	if err != nil {
		return fmt.Errorf("find user %q: %w", opts.Username, err)
	}
	var bundle account.UserDataBundle
	if err := readJSON(opts.In, &bundle); err != nil {
		return err
	}
	return store.ImportUserData(ctx, user.ID, bundle, opts.Replace)
}

func ExportLegacyConfig(_ context.Context, out string) error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	bundle := account.NewUserDataBundle(c.Directories, c.Connectors)
	return writeJSON(out, bundle)
}

func openStore(ctx context.Context) (*account.Store, error) {
	cfg, err := account.ConfigFromEnv()
	if err != nil {
		return nil, err
	}
	store, err := account.Open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if ok, err := store.IsInitialized(ctx); err != nil {
		_ = store.Close()
		return nil, err
	} else if !ok {
		_ = store.Close()
		return nil, account.ErrNotInitialized
	}
	if err := store.Init(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func readJSON(path string, dst any) error {
	var r io.Reader
	if path == "" || path == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	return json.NewDecoder(r).Decode(dst)
}

func writeJSON(path string, value any) error {
	var w io.Writer
	var close func() error
	if path == "" || path == "-" {
		w = os.Stdout
		close = func() error { return nil }
	} else {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		w = f
		close = f.Close
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		_ = close()
		return err
	}
	return close()
}

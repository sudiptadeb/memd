package account

import (
	"context"
	"testing"
)

// TestRCSettingsRoundTrip: no stored value means "not found" (callers then
// default the feature to on), and stored true/false values are honoured and
// overwritable in place.
func TestRCSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := openOIDCTestStore(t)

	if _, ok, err := store.GetRCSettings(ctx); err != nil {
		t.Fatalf("GetRCSettings on fresh db: %v", err)
	} else if ok {
		t.Fatal("fresh db: want no stored rc setting")
	}

	if err := store.SaveRCSettings(ctx, RCSettings{Enabled: false}); err != nil {
		t.Fatalf("SaveRCSettings(false): %v", err)
	}
	cfg, ok, err := store.GetRCSettings(ctx)
	if err != nil || !ok {
		t.Fatalf("GetRCSettings after save: cfg=%+v ok=%v err=%v", cfg, ok, err)
	}
	if cfg.Enabled {
		t.Fatal("stored disabled setting came back enabled")
	}

	// Overwrite in place (the upsert path) and read back.
	if err := store.SaveRCSettings(ctx, RCSettings{Enabled: true}); err != nil {
		t.Fatalf("SaveRCSettings(true): %v", err)
	}
	cfg, ok, err = store.GetRCSettings(ctx)
	if err != nil || !ok || !cfg.Enabled {
		t.Fatalf("re-saved setting: cfg=%+v ok=%v err=%v, want enabled", cfg, ok, err)
	}

	// The rc key must not disturb the OIDC blob sharing the table.
	if _, ok, err := store.GetOIDCSettings(ctx); err != nil {
		t.Fatalf("GetOIDCSettings: %v", err)
	} else if ok {
		t.Fatal("rc setting bled into the oidc key")
	}
}

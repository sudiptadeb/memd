package registry

import (
	"testing"

	"github.com/sudiptadeb/memd/server/internal/config"
)

func TestRotateConnector_ReplacesToken(t *testing.T) {
	r := NewEphemeral()
	c, err := r.AddConnector(config.Connector{Name: "claude", DirectoryIDs: []string{"x"}, Write: true})
	if err != nil {
		t.Fatalf("AddConnector: %v", err)
	}
	oldToken := c.Token

	rot, err := r.RotateConnector(c.ID)
	if err != nil {
		t.Fatalf("RotateConnector: %v", err)
	}
	if rot.Token == "" || rot.Token == oldToken {
		t.Fatalf("token did not change: old=%q new=%q", oldToken, rot.Token)
	}
	if rot.Name != c.Name || rot.Write != c.Write || len(rot.DirectoryIDs) != 1 || rot.DirectoryIDs[0] != "x" {
		t.Fatalf("non-token fields mutated: %+v", rot)
	}
}

func TestRotateConnector_OldTokenStopsResolving(t *testing.T) {
	r := NewEphemeral()
	c, _ := r.AddConnector(config.Connector{Name: "claude", DirectoryIDs: []string{"x"}})
	oldToken := c.Token

	rot, err := r.RotateConnector(c.ID)
	if err != nil {
		t.Fatalf("RotateConnector: %v", err)
	}
	if got := r.ConnectorByToken(oldToken); got != nil {
		t.Fatalf("old token should not resolve, got %+v", got)
	}
	if got := r.ConnectorByToken(rot.Token); got == nil || got.ID != c.ID {
		t.Fatalf("new token should resolve to the same connector, got %+v", got)
	}
}

func TestRotateConnector_UnknownID(t *testing.T) {
	r := NewEphemeral()
	if _, err := r.RotateConnector("does-not-exist"); err == nil {
		t.Fatalf("rotating an unknown id should fail")
	}
}

func TestUpdateConnector_ChangesNameDirsWrite_PreservesTokenAndID(t *testing.T) {
	r := NewEphemeral()
	r.cfg.Directories = []config.Directory{
		{ID: "d1", Name: "one"},
		{ID: "d2", Name: "two"},
	}
	c, _ := r.AddConnector(config.Connector{Name: "claude", DirectoryIDs: []string{"d1"}, Write: false})
	oldToken := c.Token
	oldID := c.ID

	updated, err := r.UpdateConnector(c.ID, "claude-code", []string{"d1", "d2"}, true)
	if err != nil {
		t.Fatalf("UpdateConnector: %v", err)
	}
	if updated.Name != "claude-code" {
		t.Fatalf("Name = %q, want claude-code", updated.Name)
	}
	if len(updated.DirectoryIDs) != 2 || updated.DirectoryIDs[0] != "d1" || updated.DirectoryIDs[1] != "d2" {
		t.Fatalf("DirectoryIDs = %v, want [d1 d2]", updated.DirectoryIDs)
	}
	if !updated.Write {
		t.Fatalf("Write should be true")
	}
	if updated.Token != oldToken {
		t.Fatalf("Token mutated: old=%q new=%q", oldToken, updated.Token)
	}
	if updated.ID != oldID {
		t.Fatalf("ID mutated: old=%q new=%q", oldID, updated.ID)
	}
}

func TestUpdateConnector_RejectsBadInput(t *testing.T) {
	r := NewEphemeral()
	r.cfg.Directories = []config.Directory{{ID: "d1", Name: "one"}}
	c, _ := r.AddConnector(config.Connector{Name: "claude", DirectoryIDs: []string{"d1"}})

	cases := []struct {
		name string
		id   string
		nm   string
		dirs []string
	}{
		{"unknown id", "does-not-exist", "x", []string{"d1"}},
		{"empty name", c.ID, "", []string{"d1"}},
		{"empty dirs", c.ID, "x", nil},
		{"unknown directory id", c.ID, "x", []string{"d-nope"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.UpdateConnector(tc.id, tc.nm, tc.dirs, true); err == nil {
				t.Fatalf("UpdateConnector(%q, %q, %v) should fail", tc.id, tc.nm, tc.dirs)
			}
		})
	}
}

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

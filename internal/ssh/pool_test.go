package ssh

import (
	"testing"

	"github.com/buckit-io/bm/internal/domain"
)

func TestPoolKeyChangesWhenPortChanges(t *testing.T) {
	creds := Resolved{AuthMethod: domain.AuthPassword, User: "ops", Password: "secret", Sudo: true}
	k1 := poolKey("draft-h1", domain.HostRef{ID: "h1", Hostname: "localhost", Port: 22}, creds)
	k2 := poolKey("draft-h1", domain.HostRef{ID: "h1", Hostname: "localhost", Port: 2201}, creds)
	if k1 == k2 {
		t.Fatal("pool key should change when host port changes")
	}
}

func TestPoolKeyChangesWhenCredsChange(t *testing.T) {
	creds1 := Resolved{AuthMethod: domain.AuthPassword, User: "ops", Password: "secret1", Sudo: true}
	creds2 := Resolved{AuthMethod: domain.AuthPassword, User: "ops", Password: "secret2", Sudo: true}
	k1 := poolKey("draft-h1", domain.HostRef{ID: "h1", Hostname: "localhost", Port: 22}, creds1)
	k2 := poolKey("draft-h1", domain.HostRef{ID: "h1", Hostname: "localhost", Port: 22}, creds2)
	if k1 == k2 {
		t.Fatal("pool key should change when credentials change")
	}
}

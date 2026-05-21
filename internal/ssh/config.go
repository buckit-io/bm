// Package ssh wraps golang.org/x/crypto/ssh with the per-cluster client cache,
// per-host credential override, and Run/RunStream/Probe helpers every later
// milestone's executor depends on.
package ssh

import (
	"errors"
	"fmt"

	"github.com/buckit-io/bm/internal/domain"
)

// Resolved is a fully-merged credential set ready to feed to Dial. No pointers,
// no inheritance — just the values to use for a single host.
type Resolved struct {
	AuthMethod    domain.AuthMethod
	User          string
	KeyPath       string
	KeyPassphrase string
	Password      string
	Sudo          bool
}

// Merge applies override on top of cluster defaults. A nil override returns
// the cluster creds unchanged. Per-field semantics: a non-nil pointer wins;
// nil inherits from the cluster default.
func Merge(cluster domain.SshCreds, override *domain.SshOverrides) Resolved {
	r := Resolved{
		AuthMethod:    cluster.AuthMethod,
		User:          cluster.User,
		KeyPath:       cluster.KeyPath,
		KeyPassphrase: cluster.KeyPassphrase,
		Password:      cluster.Password,
		Sudo:          cluster.Sudo,
	}
	if override == nil {
		return r
	}
	if override.AuthMethod != nil {
		r.AuthMethod = *override.AuthMethod
	}
	if override.User != nil {
		r.User = *override.User
	}
	if override.KeyPath != nil {
		r.KeyPath = *override.KeyPath
	}
	if override.KeyPassphrase != nil {
		r.KeyPassphrase = *override.KeyPassphrase
	}
	if override.Password != nil {
		r.Password = *override.Password
	}
	if override.Sudo != nil {
		r.Sudo = *override.Sudo
	}
	return r
}

// Validate reports whether r has enough info to attempt a dial. Returns nil
// when the creds are coherent.
func (r Resolved) Validate() error {
	if r.User == "" {
		return errors.New("ssh: user required")
	}
	switch r.AuthMethod {
	case domain.AuthKey:
		if r.KeyPath == "" {
			return errors.New("ssh: keyPath required for key auth")
		}
	case domain.AuthPassword:
		if r.Password == "" {
			return errors.New("ssh: password required for password auth")
		}
	case domain.AuthAgent:
		// agent auth requires SSH_AUTH_SOCK at dial time; checked there.
	default:
		return fmt.Errorf("ssh: unsupported authMethod %q", r.AuthMethod)
	}
	return nil
}

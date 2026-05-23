// Package credentials holds the shared validation rules for the cluster
// root user / root password. Both the cluster_deploy executor and the
// rotate_root_creds executor pull from here so the deploy time and rotation
// time rules can't drift. The TypeScript equivalent lives at
// web/src/lib/credentials.ts.
package credentials

import (
	"errors"
	"fmt"
)

const (
	// RootPasswordMinLen is the inclusive minimum length, in bytes, of an
	// acceptable root password.
	RootPasswordMinLen = 8
	// RootPasswordMaxLen is the inclusive maximum length, in bytes, of an
	// acceptable root password. MinIO accepts longer values but 40 is a
	// pragmatic ceiling that fits comfortably in an env file and matches
	// what the UI advertises.
	RootPasswordMaxLen = 40
	// RootUserMinLen is the inclusive minimum length, in bytes, of an
	// acceptable root username.
	RootUserMinLen = 3
)

// ValidateRootPassword enforces the password rules shared by deploy and
// rotate_root_creds: length between RootPasswordMinLen and
// RootPasswordMaxLen (inclusive) and only printable ASCII bytes
// (0x21-0x7e) — that is, no spaces, no control characters, and no
// non-ASCII codepoints.
func ValidateRootPassword(password string) error {
	if len(password) < RootPasswordMinLen || len(password) > RootPasswordMaxLen {
		return fmt.Errorf("root password must be %d-%d characters", RootPasswordMinLen, RootPasswordMaxLen)
	}
	for _, r := range password {
		if r < 0x21 || r > 0x7e {
			return errors.New("root password must use printable ASCII characters without spaces")
		}
	}
	return nil
}

// ValidateRootUser enforces the username rules: at least RootUserMinLen
// characters and only ASCII letters, digits, underscore, or dash.
func ValidateRootUser(user string) error {
	if len(user) < RootUserMinLen {
		return fmt.Errorf("root user must be at least %d characters", RootUserMinLen)
	}
	for _, r := range user {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return errors.New("root user may only contain letters, digits, underscore, or dash")
		}
	}
	return nil
}

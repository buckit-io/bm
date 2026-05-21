// Package config resolves on-disk paths and bootstraps the data-encryption key
// used by internal/store for the encrypted bbolt buckets.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	dataKeyLen    = 32
	dataKeyEnvVar = "BM_DATA_KEY"
	dirPerm       = 0o700
	filePerm      = 0o600
)

// Paths captures every on-disk location bm cares about.
type Paths struct {
	Dir        string
	DBFile     string
	DataKey    string
	AliasFile  string
	TasksDir   string
}

// Resolve returns the standard path set rooted at the platform's config dir,
// or at override if non-empty. The directory is created (mode 0700) if missing.
func Resolve(override string) (Paths, error) {
	root := override
	if root == "" {
		var err error
		root, err = defaultRoot()
		if err != nil {
			return Paths{}, err
		}
	}
	if err := os.MkdirAll(root, dirPerm); err != nil {
		return Paths{}, fmt.Errorf("create config dir %s: %w", root, err)
	}
	return Paths{
		Dir:       root,
		DBFile:    filepath.Join(root, "bm.db"),
		DataKey:   filepath.Join(root, "data.key"),
		AliasFile: filepath.Join(root, "config.json"),
		TasksDir:  filepath.Join(root, "tasks"),
	}, nil
}

func defaultRoot() (string, error) {
	if runtime.GOOS == "windows" {
		if v := os.Getenv("APPDATA"); v != "" {
			return filepath.Join(v, "bm"), nil
		}
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "bm"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "bm"), nil
}

// LoadDataKey returns a 32-byte data-encryption key, sourced in priority order:
//  1. $BM_DATA_KEY (hex-encoded 32 bytes)
//  2. existing key file at p.DataKey
//  3. a freshly generated key written to p.DataKey mode 0600
//
// The boolean return is true when a new key was generated and persisted; the
// caller logs once so the operator notices a brand-new key has been minted.
func LoadDataKey(p Paths) ([]byte, bool, error) {
	if env := strings.TrimSpace(os.Getenv(dataKeyEnvVar)); env != "" {
		key, err := hex.DecodeString(env)
		if err != nil {
			return nil, false, fmt.Errorf("decode %s: %w", dataKeyEnvVar, err)
		}
		if len(key) != dataKeyLen {
			return nil, false, fmt.Errorf("%s must be %d bytes hex-encoded (got %d)", dataKeyEnvVar, dataKeyLen, len(key))
		}
		return key, false, nil
	}

	switch key, err := readKeyFile(p.DataKey); {
	case err == nil:
		return key, false, nil
	case errors.Is(err, os.ErrNotExist):
		// fall through to generation
	default:
		return nil, false, err
	}

	key := make([]byte, dataKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, false, fmt.Errorf("generate data key: %w", err)
	}
	if err := os.WriteFile(p.DataKey, []byte(hex.EncodeToString(key)+"\n"), filePerm); err != nil {
		return nil, false, fmt.Errorf("write data key: %w", err)
	}
	return key, true, nil
}

func readKeyFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(data))
	key, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("data key %s is not valid hex: %w", path, err)
	}
	if len(key) != dataKeyLen {
		return nil, fmt.Errorf("data key %s has wrong length (%d, want %d)", path, len(key), dataKeyLen)
	}
	return key, nil
}

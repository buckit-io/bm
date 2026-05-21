package ssh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/buckit-io/bm/internal/domain"
)

const (
	defaultPort = 22
	dialTimeout = 10 * time.Second
)

// Dial opens a fresh SSH connection to host using the merged credentials.
// Honors ctx for the dial timeout. Returns the raw ssh.Client; callers that
// want pooling should go through Pool.
//
// TODO(m4-tofu): host key verification. M3 uses InsecureIgnoreHostKey to keep
// the test surface small. M4 pins host keys on first connect into a
// cluster_ssh_known_hosts bbolt bucket and rejects mismatches.
func Dial(ctx context.Context, host domain.HostRef, creds Resolved) (*ssh.Client, error) {
	if err := creds.Validate(); err != nil {
		return nil, err
	}
	port := host.Port
	if port == 0 {
		port = defaultPort
	}

	cfg, err := buildClientConfig(creds)
	if err != nil {
		return nil, err
	}
	cfg.Timeout = dialTimeout

	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	d := net.Dialer{Timeout: dialTimeout}
	addr := net.JoinHostPort(host.Hostname, strconv.Itoa(port))
	conn, err := d.DialContext(dctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial tcp %s: %w", addr, err)
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ssh handshake %s: %w", addr, err)
	}
	return ssh.NewClient(c, chans, reqs), nil
}

func buildClientConfig(creds Resolved) (*ssh.ClientConfig, error) {
	cfg := &ssh.ClientConfig{
		User:            creds.User,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO(m4-tofu)
	}

	switch creds.AuthMethod {
	case domain.AuthKey:
		signer, err := loadKey(creds.KeyPath, creds.KeyPassphrase)
		if err != nil {
			return nil, err
		}
		cfg.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	case domain.AuthPassword:
		cfg.Auth = []ssh.AuthMethod{ssh.Password(creds.Password)}
	case domain.AuthAgent:
		signers, err := agentSigners()
		if err != nil {
			return nil, err
		}
		cfg.Auth = []ssh.AuthMethod{ssh.PublicKeys(signers...)}
	default:
		return nil, fmt.Errorf("ssh: unsupported authMethod %q", creds.AuthMethod)
	}
	return cfg, nil
}

func loadKey(path, passphrase string) (ssh.Signer, error) {
	resolved, err := expandUser(path)
	if err != nil {
		return nil, err
	}
	pem, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read key %s: %w", resolved, err)
	}
	if passphrase != "" {
		return ssh.ParsePrivateKeyWithPassphrase(pem, []byte(passphrase))
	}
	return ssh.ParsePrivateKey(pem)
}

func expandUser(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}

func agentSigners() ([]ssh.Signer, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, errors.New("ssh: SSH_AUTH_SOCK not set; ssh-agent unavailable")
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("ssh: dial agent: %w", err)
	}
	signers, err := agent.NewClient(conn).Signers()
	if err != nil {
		return nil, fmt.Errorf("ssh: agent signers: %w", err)
	}
	if len(signers) == 0 {
		return nil, errors.New("ssh: agent has no keys loaded")
	}
	return signers, nil
}

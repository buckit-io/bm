package ssh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/buckit-io/bm/internal/domain"
)

const idleTimeout = 5 * time.Minute

// Dialer is the dial function the pool calls when a cached entry is missing
// or stale. Production uses Dial; tests inject a fake dialer to avoid spinning
// up an SSH server.
type Dialer func(ctx context.Context, host domain.HostRef, creds Resolved) (*ssh.Client, error)

// Pool caches per-(clusterId, hostId) SSH clients. Connections idle for more
// than idleTimeout are closed; the next Get re-dials transparently.
type Pool struct {
	dial Dialer

	mu      sync.Mutex
	entries map[string]*entry
	closed  bool
}

type entry struct {
	client   *ssh.Client
	lastUsed time.Time
}

// NewPool returns a pool wired to dial. Pass nil to use the default Dial.
func NewPool(dial Dialer) *Pool {
	if dial == nil {
		dial = Dial
	}
	return &Pool{
		dial:    dial,
		entries: map[string]*entry{},
	}
}

// Get returns a connected client for (clusterID, host). Reuses a cached entry
// when fresh; re-dials otherwise. Safe for concurrent callers.
func (p *Pool) Get(ctx context.Context, clusterID string, host domain.HostRef, creds Resolved) (*ssh.Client, error) {
	key := poolKey(clusterID, host, creds)

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("ssh: pool closed")
	}
	if e, ok := p.entries[key]; ok {
		if time.Since(e.lastUsed) < idleTimeout && isAlive(e.client) {
			e.lastUsed = time.Now()
			c := e.client
			p.mu.Unlock()
			return c, nil
		}
		// Stale: evict before re-dial.
		_ = e.client.Close()
		delete(p.entries, key)
	}
	p.mu.Unlock()

	client, err := p.dial(ctx, host, creds)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		_ = client.Close()
		return nil, fmt.Errorf("ssh: pool closed")
	}
	p.entries[key] = &entry{client: client, lastUsed: time.Now()}
	return client, nil
}

// Drop closes and removes the cached client for (clusterID, hostID). Useful
// when the operator rotates credentials.
func (p *Pool) Drop(clusterID, hostID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	prefix := poolPrefix(clusterID, hostID)
	for key, e := range p.entries {
		if strings.HasPrefix(key, prefix) {
			_ = e.client.Close()
			delete(p.entries, key)
		}
	}
}

// Close tears down every cached client. Pool refuses further work.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.entries {
		_ = e.client.Close()
	}
	p.entries = map[string]*entry{}
	p.closed = true
}

// SweepIdle closes connections idle for more than idleTimeout. Callable from a
// caller-owned ticker; not started automatically because there's nothing else
// in the package that needs a goroutine and we'd rather not leak one in tests.
func (p *Pool) SweepIdle() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for k, e := range p.entries {
		if now.Sub(e.lastUsed) >= idleTimeout {
			_ = e.client.Close()
			delete(p.entries, k)
		}
	}
}

func poolKey(clusterID string, host domain.HostRef, creds Resolved) string {
	return poolPrefix(clusterID, host.ID) +
		host.Hostname + "\x00" +
		strconv.Itoa(host.Port) + "\x00" +
		credsFingerprint(creds)
}

func poolPrefix(clusterID, hostID string) string {
	return clusterID + "\x00" + hostID + "\x00"
}

func credsFingerprint(creds Resolved) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		string(creds.AuthMethod),
		creds.User,
		creds.KeyPath,
		creds.KeyPassphrase,
		creds.Password,
		strconv.FormatBool(creds.Sudo),
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// isAlive does a cheap keepalive check. Returns true if the client appears
// usable. Failures here trigger a re-dial in Get.
func isAlive(c *ssh.Client) bool {
	if c == nil {
		return false
	}
	_, _, err := c.SendRequest("keepalive@bm", true, nil)
	return err == nil
}

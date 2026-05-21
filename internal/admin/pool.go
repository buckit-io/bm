package admin

import (
	"sync"

	"github.com/buckit-io/bm/internal/domain"
)

// Pool caches per-cluster admin clients. Constructed once at process start,
// shared across all callers (refresh loops, discovery, executors).
type Pool struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

// NewPool returns an empty Pool.
func NewPool() *Pool {
	return &Pool{clients: map[string]*Client{}}
}

// Get returns the cached client for clusterID or builds one from creds.
// Subsequent Get calls for the same clusterID return the cached instance.
// Use Drop when credentials rotate so the next Get re-dials with fresh creds.
func (p *Pool) Get(clusterID string, creds domain.AdminCreds) (*Client, error) {
	p.mu.RLock()
	c, ok := p.clients[clusterID]
	p.mu.RUnlock()
	if ok {
		return c, nil
	}
	built, err := New(creds)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.clients[clusterID]; ok {
		return existing, nil
	}
	p.clients[clusterID] = built
	return built, nil
}

// Drop forgets the cached client for clusterID. Safe to call when none exists.
func (p *Pool) Drop(clusterID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.clients, clusterID)
}

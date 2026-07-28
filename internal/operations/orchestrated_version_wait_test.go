package operations

import (
	"context"
	"testing"
	"time"

	"github.com/buckit-io/bm/internal/domain"
)

func TestWaitServerVersionsReached_WaitsForDelayedPeer(t *testing.T) {
	target := time.Date(2026, time.July, 21, 17, 57, 33, 0, time.UTC)
	previous := target.Add(-72 * time.Hour).Format("2006-01-02T15-04-05Z")
	current := target.Format("2006-01-02T15-04-05Z")
	calls := 0

	err := waitServerVersionsReached(
		context.Background(),
		func(context.Context) (*domain.ServerInfo, error) {
			calls++
			secondVersion := previous
			if calls >= 2 {
				secondVersion = current
			}
			return &domain.ServerInfo{Servers: []domain.ServerInfoServer{
				{Endpoint: "node1:9000", Version: current},
				{Endpoint: "node2:9000", Version: secondVersion},
			}}, nil
		},
		target,
		"RELEASE."+current,
		WaitOptions{Timeout: time.Second, Tick: time.Millisecond},
	)
	if err != nil {
		t.Fatalf("waitServerVersionsReached() error = %v", err)
	}
	if calls < 2 {
		t.Fatalf("ServerInfo calls = %d, want at least 2", calls)
	}
}

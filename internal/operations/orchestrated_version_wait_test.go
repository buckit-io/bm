package operations

import (
	"context"
	"errors"
	"strings"
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
		2,
		WaitOptions{Timeout: time.Second, Tick: time.Millisecond},
		nil,
	)
	if err != nil {
		t.Fatalf("waitServerVersionsReached() error = %v", err)
	}
	if calls < 2 {
		t.Fatalf("ServerInfo calls = %d, want at least 2", calls)
	}
}

func TestWaitServerVersionsReached_TimesOutOnStuckPeer(t *testing.T) {
	target := time.Date(2026, time.July, 21, 17, 57, 33, 0, time.UTC)
	previous := target.Add(-72 * time.Hour).Format("2006-01-02T15-04-05Z")
	calls := 0

	err := waitServerVersionsReached(
		context.Background(),
		func(context.Context) (*domain.ServerInfo, error) {
			calls++
			return &domain.ServerInfo{Servers: []domain.ServerInfoServer{
				{Endpoint: "node1:9000", Version: target.Format("2006-01-02T15-04-05Z")},
				{Endpoint: "node2:9000", Version: previous},
			}}, nil
		},
		target,
		"RELEASE."+target.Format("2006-01-02T15-04-05Z"),
		2,
		WaitOptions{Timeout: 30 * time.Millisecond, Tick: time.Millisecond},
		nil,
	)
	if err == nil {
		t.Fatal("waitServerVersionsReached() error = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "node2:9000") || !strings.Contains(err.Error(), "still reports") {
		t.Fatalf("timeout error = %q, want stuck peer detail", err)
	}
	if calls < 2 {
		t.Fatalf("ServerInfo calls = %d, want retries", calls)
	}
}

func TestWaitServerVersionsReached_RetriesTransientFetchError(t *testing.T) {
	target := time.Date(2026, time.July, 21, 17, 57, 33, 0, time.UTC)
	calls := 0

	err := waitServerVersionsReached(
		context.Background(),
		func(context.Context) (*domain.ServerInfo, error) {
			calls++
			if calls == 1 {
				return nil, errors.New("connection refused")
			}
			return &domain.ServerInfo{Servers: []domain.ServerInfoServer{{
				Endpoint: "node1:9000",
				Version:  target.Format("2006-01-02T15-04-05Z"),
			}}}, nil
		},
		target,
		"RELEASE."+target.Format("2006-01-02T15-04-05Z"),
		1,
		WaitOptions{Timeout: time.Second, Tick: time.Millisecond},
		nil,
	)
	if err != nil {
		t.Fatalf("waitServerVersionsReached() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("ServerInfo calls = %d, want 2", calls)
	}
}

func TestWaitServerVersionsReached_TimesOutWhenPeerIsMissing(t *testing.T) {
	target := time.Date(2026, time.July, 21, 17, 57, 33, 0, time.UTC)

	err := waitServerVersionsReached(
		context.Background(),
		func(context.Context) (*domain.ServerInfo, error) {
			return &domain.ServerInfo{Servers: []domain.ServerInfoServer{{
				Endpoint: "node1:9000",
				Version:  target.Format("2006-01-02T15-04-05Z"),
			}}}, nil
		},
		target,
		"RELEASE."+target.Format("2006-01-02T15-04-05Z"),
		2,
		WaitOptions{Timeout: 30 * time.Millisecond, Tick: time.Millisecond},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "1 of 2 servers") {
		t.Fatalf("waitServerVersionsReached() error = %v, want missing peer detail", err)
	}
}

func TestWaitServerVersionsReached_HonorsCancellation(t *testing.T) {
	target := time.Date(2026, time.July, 21, 17, 57, 33, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := waitServerVersionsReached(
		ctx,
		func(context.Context) (*domain.ServerInfo, error) {
			cancel()
			return &domain.ServerInfo{Servers: []domain.ServerInfoServer{{
				Endpoint: "node1:9000",
				Version:  target.Add(-time.Hour).Format("2006-01-02T15-04-05Z"),
			}}}, nil
		},
		target,
		"RELEASE."+target.Format("2006-01-02T15-04-05Z"),
		1,
		WaitOptions{Timeout: time.Second, Tick: time.Millisecond},
		nil,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitServerVersionsReached() error = %v, want context.Canceled", err)
	}
}

func TestWaitServerVersionsReached_RejectsUncomparableVersion(t *testing.T) {
	target := time.Date(2026, time.July, 21, 17, 57, 33, 0, time.UTC)
	err := waitServerVersionsReached(
		context.Background(),
		func(context.Context) (*domain.ServerInfo, error) {
			return &domain.ServerInfo{Servers: []domain.ServerInfoServer{{
				Endpoint: "node1:9000",
				Version:  "unparseable",
			}}}, nil
		},
		target,
		"RELEASE."+target.Format("2006-01-02T15-04-05Z"),
		1,
		WaitOptions{Timeout: time.Second, Tick: time.Millisecond},
		nil,
	)
	if !errors.Is(err, errUncomparableServerVersion) {
		t.Fatalf("waitServerVersionsReached() error = %v, want uncomparable version", err)
	}
}

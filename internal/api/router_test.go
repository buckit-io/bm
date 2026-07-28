package api

import (
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"

	bmassets "github.com/buckit-io/bm"
)

func TestAssertLoopback(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		addr    string
		wantErr bool
	}{
		{name: "empty host", addr: ":9443"},
		{name: "ipv4 loopback", addr: "127.0.0.1:9443"},
		{name: "ipv6 loopback", addr: "[::1]:9443"},
		{name: "localhost", addr: "localhost:9443"},
		{name: "wildcard", addr: "0.0.0.0:9443", wantErr: true},
		{name: "host network", addr: "192.168.1.10:9443", wantErr: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := AssertLoopback(tc.addr)
			if tc.wantErr && err == nil {
				t.Fatalf("AssertLoopback(%q): want error, got nil", tc.addr)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("AssertLoopback(%q): want nil, got %v", tc.addr, err)
			}
		})
	}
}

func TestEmbeddedStaticHandlerServesJavaScriptAsset(t *testing.T) {
	handler, ok := embeddedStaticHandler()
	if !ok {
		t.Fatal("embeddedStaticHandler() unavailable")
	}

	uiFS, err := bmassets.WebDist()
	if err != nil {
		t.Fatalf("WebDist(): %v", err)
	}
	entries, err := fs.ReadDir(uiFS, "assets")
	if err != nil {
		t.Fatalf("ReadDir(assets): %v", err)
	}
	var asset string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".js") {
			asset = entry.Name()
			break
		}
	}
	if asset == "" {
		t.Fatal("embedded UI has no JavaScript asset")
	}

	req := httptest.NewRequest("GET", "/assets/"+asset, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if got := res.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") {
		t.Fatalf("Content-Type = %q, want JavaScript asset", got)
	}
}

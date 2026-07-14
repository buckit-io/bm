package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPostManagerShutdownAccepted(t *testing.T) {
	called := make(chan struct{}, 1)
	handler := New(Options{
		Shutdown: func() {
			called <- struct{}{}
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/manager/shutdown", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusAccepted, rr.Body.String())
	}

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("shutdown was not called")
	}
}

func TestPostManagerShutdownUnavailable(t *testing.T) {
	handler := New(Options{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/manager/shutdown", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusServiceUnavailable, rr.Body.String())
	}
}

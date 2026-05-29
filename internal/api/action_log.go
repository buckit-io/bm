package api

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/buckit-io/bm/internal/domain"
)

func logAction(r *http.Request, action string, kv ...any) {
	reqID := ""
	if r != nil {
		reqID = middleware.GetReqID(r.Context())
	}
	parts := []string{"action=" + action}
	if reqID != "" {
		parts = append(parts, "req_id="+reqID)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		key := fmt.Sprint(kv[i])
		val := strings.TrimSpace(fmt.Sprint(kv[i+1]))
		if val == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%q", key, val))
	}
	fmt.Fprintln(os.Stderr, strings.Join(parts, " "))
}

func logPreflightResults(r *http.Request, action string, results []domain.PreflightResult) {
	blockingFails := 0
	warnings := 0
	for _, row := range results {
		switch row.Result {
		case domain.PreflightFail:
			if row.Severity == domain.PreflightBlocking {
				blockingFails++
			} else {
				warnings++
			}
		case domain.PreflightWarn:
			warnings++
		}
	}
	logAction(r, action, "checks", len(results), "blocking_fails", blockingFails, "warnings", warnings)
	for _, row := range results {
		if row.Result == domain.PreflightPass || row.Result == domain.PreflightSkipped {
			continue
		}
		logAction(r, action+".check", "id", row.ID, "label", row.Label, "severity", row.Severity, "result", row.Result, "detail", row.Detail)
		for _, hs := range row.HostStatuses {
			if hs.Status == domain.PreflightPass {
				continue
			}
			logAction(r, action+".host", "id", row.ID, "host", hs.Hostname, "status", hs.Status, "message", hs.Message)
		}
	}
}

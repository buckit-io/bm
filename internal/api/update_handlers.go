package api

import (
	"net/http"

	"github.com/buckit-io/bm/internal/update"
)

func getManagerUpdate(updater *update.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if updater == nil {
			writeError(w, http.StatusServiceUnavailable, "no_updater", "update service not configured")
			return
		}
		status, err := updater.Check(r.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, "update_check_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, status)
	}
}

func postManagerUpdateApply(updater *update.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if updater == nil {
			writeError(w, http.StatusServiceUnavailable, "no_updater", "update service not configured")
			return
		}
		result, err := updater.Apply(r.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, "update_apply_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

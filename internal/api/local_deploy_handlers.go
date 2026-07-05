package api

import (
	"encoding/json"
	"net/http"

	"github.com/buckit-io/bm/internal/localdeploy"
)

func prepareLocalDeployment() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req localdeploy.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		resp, err := localdeploy.Prepare(r.Context(), req, localdeploy.Options{})
		if err != nil {
			writeError(w, http.StatusBadRequest, "local_prepare_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func previewLocalDeployment() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req localdeploy.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		resp, err := localdeploy.Preview(req, localdeploy.Options{})
		if err != nil {
			writeError(w, http.StatusBadRequest, "local_preview_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

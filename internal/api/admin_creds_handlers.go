package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/domain"
)

type adminCredsView struct {
	URL              string `json:"url"`
	AccessKey        string `json:"accessKey"`
	Insecure         bool   `json:"insecure"`
	SecretConfigured bool   `json:"secretConfigured"`
}

func getAdminCreds(repo *clusteradmin.Repo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			writeError(w, http.StatusServiceUnavailable, "no_repo", "admin creds repo not configured")
			return
		}
		creds, err := repo.Get(r.Context(), chi.URLParam(r, "id"))
		if errors.Is(err, clusteradmin.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "admin creds not set")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, adminCredsView{
			URL:              creds.URL,
			AccessKey:        creds.AccessKey,
			Insecure:         creds.Insecure,
			SecretConfigured: creds.SecretKey != "",
		})
	}
}

func putAdminCreds(repo *clusteradmin.Repo, pool *admin.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil {
			writeError(w, http.StatusServiceUnavailable, "no_repo", "admin creds repo not configured")
			return
		}
		clusterID := chi.URLParam(r, "id")
		var creds domain.AdminCreds
		if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		creds.URL = strings.TrimSpace(creds.URL)
		creds.AccessKey = strings.TrimSpace(creds.AccessKey)
		if err := validateAdminCredsInput(creds); err != nil {
			writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
			return
		}
		if err := repo.Put(r.Context(), clusterID, creds); err != nil {
			writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
			return
		}
		if pool != nil {
			pool.Drop(clusterID)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func validateAdminCredsInput(creds domain.AdminCreds) error {
	if creds.URL == "" {
		return errors.New("url required")
	}
	u, err := url.Parse(creds.URL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errors.New("url must be an absolute HTTP or HTTPS URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("url must use http or https")
	}
	if creds.AccessKey == "" {
		return errors.New("accessKey required")
	}
	if creds.SecretKey == "" {
		return errors.New("secretKey required")
	}
	return nil
}

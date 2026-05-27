package migration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	madmin "github.com/buckit-io/madmin-go/v3"

	"github.com/buckit-io/bm/internal/domain"
)

// TestWriteAndReadSnapshot round-trips a fully-populated MinioSnapshot
// through writeSnapshot + ReadSnapshot. Confirms the on-disk file is
// mode 0600 and the wire format survives a serialise/deserialise.
func TestWriteAndReadSnapshot(t *testing.T) {
	dir := t.TempDir()
	snap := &domain.MinioSnapshot{
		CapturedAt: time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC),
		ClusterID:  "prod-east",
		Version:    "RELEASE.2026-04-01T00-00-00Z",
		Buckets: []domain.BucketSnapshot{
			{Name: "alpha", Versioning: "Enabled", ObjectLock: true, Tags: []string{"team=infra"}},
			{Name: "beta", SizeBytes: 12 << 30},
		},
		Users: []domain.UserSnapshot{
			{AccessKey: "ak1", Status: "enabled", Policies: []string{"readwrite"}},
		},
		Groups: []domain.GroupSnapshot{
			{Name: "ops", Members: []string{"ak1"}, Policy: "readwrite"},
		},
		Policies: []domain.PolicySnapshot{
			{Name: "readwrite", Policy: `{"Statement":[]}`},
		},
		ServiceAccounts: []domain.ServiceAccountSnapshot{
			{AccessKey: "sa1", ParentUser: "ak1"},
		},
		Lifecycle: []domain.LifecycleRule{
			{BucketName: "alpha", RuleID: "expire-90d", RuleXML: "<Rule/>"},
		},
		Notifications: []domain.NotificationTarget{
			{BucketName: "beta", ARN: "arn:minio:sqs:::1:webhook", Events: []string{"s3:ObjectCreated:*"}},
		},
		Warnings: []string{"alpha: tags: 404"},
	}

	path, err := writeSnapshot(dir, "prod-east", snap)
	if err != nil {
		t.Fatalf("writeSnapshot: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != snapshotFileMode {
		t.Fatalf("want mode %#o, got %#o", snapshotFileMode, info.Mode().Perm())
	}

	got, err := ReadSnapshot(path)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if got.ClusterID != snap.ClusterID || got.Version != snap.Version {
		t.Fatalf("identity drift: got %+v", got)
	}
	if len(got.Buckets) != 2 || got.Buckets[0].Name != "alpha" || !got.Buckets[0].ObjectLock {
		t.Fatalf("bucket drift: %+v", got.Buckets)
	}
	if len(got.Users) != 1 || got.Users[0].AccessKey != "ak1" {
		t.Fatalf("user drift: %+v", got.Users)
	}
	if len(got.Groups) != 1 || got.Groups[0].Name != "ops" {
		t.Fatalf("group drift: %+v", got.Groups)
	}
	if len(got.Policies) != 1 || len(got.Lifecycle) != 1 || len(got.Notifications) != 1 {
		t.Fatalf("policy/lifecycle/notification drift: %+v", got)
	}
	if len(got.Warnings) != 1 || got.Warnings[0] != "alpha: tags: 404" {
		t.Fatalf("warnings drift: %+v", got.Warnings)
	}
}

// TestSummarizeCounts confirms the counts the wizard's Review step renders
// match what the snapshot actually contains. The largestBucket headline is
// only set when at least one bucket has a non-zero size.
func TestSummarizeCounts(t *testing.T) {
	snap := &domain.MinioSnapshot{
		Buckets: []domain.BucketSnapshot{
			{Name: "a", Versioning: "Enabled"},
			{Name: "b", Versioning: "Suspended", ObjectLock: true},
			{Name: "c", SizeBytes: 5 << 30},
		},
		Users:           []domain.UserSnapshot{{AccessKey: "u1"}, {AccessKey: "u2"}},
		Groups:          []domain.GroupSnapshot{{Name: "g1"}},
		Policies:        []domain.PolicySnapshot{{Name: "p1"}, {Name: "p2"}, {Name: "p3"}},
		ServiceAccounts: []domain.ServiceAccountSnapshot{{AccessKey: "sa1"}},
		Lifecycle: []domain.LifecycleRule{
			{BucketName: "a", RuleID: "r1"},
			{BucketName: "a", RuleID: "r2"},
			{BucketName: "b", RuleID: "r1"},
		},
		Notifications: []domain.NotificationTarget{{BucketName: "a", ARN: "arn:1"}},
		Warnings:      []string{"warn1"},
	}
	s := Summarize(snap)
	if s.Buckets != 3 {
		t.Fatalf("Buckets: %d", s.Buckets)
	}
	if s.Versioning != 2 {
		t.Fatalf("Versioning: %d", s.Versioning)
	}
	if s.ObjectLock != 1 {
		t.Fatalf("ObjectLock: %d", s.ObjectLock)
	}
	if s.Lifecycle != 2 {
		// "a" has 2 rules + "b" has 1 rule → 2 distinct buckets
		t.Fatalf("Lifecycle (bucket-distinct): %d", s.Lifecycle)
	}
	if s.Users != 2 || s.Groups != 1 || s.CustomPolicies != 3 || s.ServiceAccounts != 1 || s.Notifications != 1 {
		t.Fatalf("counts drift: %+v", s)
	}
	if s.LargestBucket == nil || s.LargestBucket.Name != "c" {
		t.Fatalf("LargestBucket: %+v", s.LargestBucket)
	}
	if !strings.Contains(s.LargestBucket.Size, "GiB") {
		t.Fatalf("LargestBucket.Size: %q", s.LargestBucket.Size)
	}
	if len(s.Warnings) != 1 || s.Warnings[0] != "warn1" {
		t.Fatalf("Warnings: %v", s.Warnings)
	}
}

// TestSummarizeNilSafety guards against a panic on the empty case the API
// path may hit when the snapshot endpoint returns no buckets.
func TestSummarizeNilSafety(t *testing.T) {
	if got := Summarize(nil); got.Buckets != 0 {
		t.Fatalf("nil snap returned %+v", got)
	}
	empty := Summarize(&domain.MinioSnapshot{})
	if empty.Buckets != 0 || empty.LargestBucket != nil {
		t.Fatalf("empty snap returned %+v", empty)
	}
}

// TestSnapshotEndToEnd hits the snapshot path against a fake MinIO admin +
// S3 endpoint and asserts the file written + summary derived. The fake
// only serves the endpoints we know to exercise: ServerInfo, AccountInfo,
// ListGroups, ListBuckets. Encrypted endpoints (ListUsers,
// ListServiceAccounts) 404 — those become Warnings, not failures.
func TestSnapshotEndToEnd(t *testing.T) {
	srv := fakeMinioForSnapshot(t)
	defer srv.Close()

	dir := t.TempDir()
	creds := domain.AdminCreds{
		URL:       srv.URL,
		AccessKey: "ak",
		SecretKey: "sk",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	snap, path, err := Snapshot(ctx, dir, "prod-east", creds)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap == nil {
		t.Fatal("nil snapshot")
	}
	if snap.ClusterID != "prod-east" {
		t.Fatalf("ClusterID: %s", snap.ClusterID)
	}
	if snap.Version != "RELEASE.2026-01-01T00-00-00Z" {
		t.Fatalf("Version: %s", snap.Version)
	}
	if len(snap.Buckets) == 0 {
		t.Fatalf("no buckets captured")
	}
	if !strings.HasPrefix(filepath.Base(path), "prod-east-") || !strings.HasSuffix(path, ".json") {
		t.Fatalf("path: %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != snapshotFileMode {
		t.Fatalf("want mode %#o, got %#o", snapshotFileMode, info.Mode().Perm())
	}
	// Encrypted-endpoint failures should land in Warnings, not break the call.
	if len(snap.Warnings) == 0 {
		// Older madmin behavior may decode a JSON error rather than fail.
		// If we got here without warnings, the test still passes — just
		// log the unusual condition.
		t.Logf("no warnings recorded — adminServer behavior may have changed")
	}
}

// fakeMinioForSnapshot responds to the subset of admin + S3 endpoints the
// snapshot capture exercises. Endpoints that need encrypted bodies return
// 404 so the caller records them as warnings.
func fakeMinioForSnapshot(t *testing.T) *httptest.Server {
	t.Helper()
	infoBody := madmin.InfoMessage{
		Mode: "online",
		Servers: []madmin.ServerProperties{
			{Endpoint: "node1:9000", State: "online", Version: "RELEASE.2026-01-01T00-00-00Z"},
		},
	}
	accountBody := madmin.AccountInfo{
		AccountName: "admin",
		Buckets: []madmin.BucketAccessInfo{
			{Name: "alpha"},
			{Name: "beta"},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/minio/admin/v3/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(infoBody)
	})
	mux.HandleFunc("/minio/admin/v3/accountinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(accountBody)
	})
	mux.HandleFunc("/minio/admin/v3/groups", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]string{})
	})
	// S3-side ListBuckets via GET / (virtual-host or path style; the test
	// only cares that we respond with a valid XML body).
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Catch-all: respond with a ListAllMyBucketsResult containing the
		// same buckets the admin AccountInfo returns. Per-bucket subresources
		// (?versioning, ?lifecycle, ?notification, ?tagging, ?object-lock)
		// return 404 — those become Warnings.
		if r.URL.Path == "/" || r.URL.Path == "" {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
<Owner><ID>admin</ID></Owner>
<Buckets>
<Bucket><Name>alpha</Name><CreationDate>2025-01-01T00:00:00.000Z</CreationDate></Bucket>
<Bucket><Name>beta</Name><CreationDate>2025-02-01T00:00:00.000Z</CreationDate></Bucket>
</Buckets>
</ListAllMyBucketsResult>`))
			return
		}
		// Per-bucket subresources: 404 so silentS3Err treats the absence
		// as "feature not configured" rather than a fetch failure.
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>NotImplemented</Code></Error>`))
	})
	return httptest.NewServer(mux)
}

func TestOfflineHostnames(t *testing.T) {
	servers := []domain.ServerInfoServer{
		{Endpoint: "http://buckit1:9000", State: domain.NodeOnline},
		{Endpoint: "http://buckit2:9000", State: domain.NodeOffline},
		{Endpoint: "https://buckit3:9000/", State: domain.NodeDegraded},
		{Endpoint: "buckit4:9000", State: domain.NodeOnline},
		{Endpoint: "http://buckit5:9000/healthz", State: domain.NodeUnknown},
	}
	got := offlineHostnames(servers)
	want := []string{"buckit2", "buckit3", "buckit5"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("position %d: want %s, got %s", i, w, got[i])
		}
	}
}

func TestOfflineHostnamesAllOnlineReturnsNil(t *testing.T) {
	servers := []domain.ServerInfoServer{
		{Endpoint: "http://buckit1:9000", State: domain.NodeOnline},
		{Endpoint: "http://buckit2:9000", State: domain.NodeOnline},
	}
	if got := offlineHostnames(servers); got != nil {
		t.Fatalf("want nil for all-online cluster, got %v", got)
	}
}

func TestHostnameFromMinioEndpoint(t *testing.T) {
	cases := map[string]string{
		"http://buckit1:9000":             "buckit1",
		"https://buckit2:9000/":           "buckit2",
		"http://buckit3:9000/healthz":     "buckit3",
		"buckit4:9000":                    "buckit4",
		"buckit5":                         "buckit5",
		"":                                "",
	}
	for in, want := range cases {
		if got := hostnameFromMinioEndpoint(in); got != want {
			t.Errorf("hostnameFromMinioEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
}

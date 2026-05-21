package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/domain"
)

const snapshotFileMode = 0o600

// Snapshot captures the supplied cluster's MinIO state into a
// domain.MinioSnapshot and writes it to disk under snapshotsDir.
//
// The on-disk format is wire-stable across bm versions — the cutover and
// rollback executors read it back unchanged. New fields are added with
// `omitempty` JSON tags so older snapshots stay decodable.
//
// Soft per-bucket failures are recorded in snap.Warnings rather than aborting
// the whole capture; older MinIO releases 404 lifecycle/notifications/etc.
// when nothing is configured.
func Snapshot(ctx context.Context, snapshotsDir string, clusterID string, creds domain.AdminCreds) (*domain.MinioSnapshot, string, error) {
	if clusterID == "" {
		return nil, "", errors.New("migration: clusterID required")
	}
	adm, err := admin.New(creds)
	if err != nil {
		return nil, "", fmt.Errorf("snapshot: build admin client: %w", err)
	}
	s3, err := admin.NewS3(creds)
	if err != nil {
		return nil, "", fmt.Errorf("snapshot: build s3 client: %w", err)
	}

	info, err := adm.ServerInfo(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("snapshot: ServerInfo: %w", err)
	}

	snap := &domain.MinioSnapshot{
		CapturedAt: time.Now().UTC(),
		ClusterID:  clusterID,
		Version:    info.Version,
	}

	// ---- Buckets (S3 ListBuckets gives us creation timestamps).
	buckets, err := s3.ListBuckets(ctx)
	if err != nil {
		// If the S3 endpoint refuses, fall back to the admin AccountInfo
		// list so we at least record bucket names.
		account, accErr := adm.AccountInfo(ctx)
		if accErr != nil {
			return nil, "", fmt.Errorf("snapshot: list buckets: %w", err)
		}
		buckets = make([]domain.BucketSnapshot, 0, len(account.Buckets))
		for _, name := range account.Buckets {
			buckets = append(buckets, domain.BucketSnapshot{Name: name})
		}
		snap.Warnings = append(snap.Warnings, fmt.Sprintf("s3 list-buckets failed, used admin account-info: %v", err))
	}

	// Per-bucket enrichment + lifecycle + notifications. Each call is a
	// separate request; we time-box the loop so a slow cluster doesn't hang
	// the snapshot indefinitely.
	for i := range buckets {
		warns := s3.EnrichBucket(ctx, &buckets[i])
		snap.Warnings = append(snap.Warnings, warns...)

		rules, lWarns, lErr := s3.BucketLifecycle(ctx, buckets[i].Name)
		if lErr != nil {
			snap.Warnings = append(snap.Warnings, fmt.Sprintf("%s: lifecycle: %v", buckets[i].Name, lErr))
		} else {
			snap.Lifecycle = append(snap.Lifecycle, rules...)
			snap.Warnings = append(snap.Warnings, lWarns...)
		}

		nots, nWarns, nErr := s3.BucketNotifications(ctx, buckets[i].Name)
		if nErr != nil {
			snap.Warnings = append(snap.Warnings, fmt.Sprintf("%s: notifications: %v", buckets[i].Name, nErr))
		} else {
			snap.Notifications = append(snap.Notifications, nots...)
			snap.Warnings = append(snap.Warnings, nWarns...)
		}
	}
	// Sort by name for deterministic on-disk output (regression-friendly tests).
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Name < buckets[j].Name })
	snap.Buckets = buckets

	// ---- IAM users / groups / canned policies / service accounts.
	if users, uErr := adm.ListUsers(ctx); uErr == nil {
		sort.Slice(users, func(i, j int) bool { return users[i].AccessKey < users[j].AccessKey })
		snap.Users = users
		if sas, sErr := adm.ListServiceAccounts(ctx, users); sErr == nil {
			sort.Slice(sas, func(i, j int) bool { return sas[i].AccessKey < sas[j].AccessKey })
			snap.ServiceAccounts = sas
		} else {
			snap.Warnings = append(snap.Warnings, fmt.Sprintf("list service accounts: %v", sErr))
		}
	} else {
		snap.Warnings = append(snap.Warnings, fmt.Sprintf("list users: %v", uErr))
	}

	if groups, gErr := adm.ListGroups(ctx); gErr == nil {
		sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
		snap.Groups = groups
	} else {
		snap.Warnings = append(snap.Warnings, fmt.Sprintf("list groups: %v", gErr))
	}

	if policies, pErr := adm.ListCannedPolicies(ctx); pErr == nil {
		sort.Slice(policies, func(i, j int) bool { return policies[i].Name < policies[j].Name })
		snap.Policies = policies
	} else {
		snap.Warnings = append(snap.Warnings, fmt.Sprintf("list policies: %v", pErr))
	}

	path, err := writeSnapshot(snapshotsDir, clusterID, snap)
	if err != nil {
		return nil, "", err
	}
	return snap, path, nil
}

// writeSnapshot persists snap to dir/<clusterId>-<ts>.json mode 0600.
func writeSnapshot(dir, clusterID string, snap *domain.MinioSnapshot) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("snapshot: mkdir %s: %w", dir, err)
	}
	body, err := json.MarshalIndent(snap, "", "    ")
	if err != nil {
		return "", err
	}
	stamp := snap.CapturedAt.Format("20060102T150405Z")
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.json", clusterID, stamp))
	if err := os.WriteFile(path, body, snapshotFileMode); err != nil {
		return "", fmt.Errorf("snapshot: write %s: %w", path, err)
	}
	return path, nil
}

// ReadSnapshot loads a previously-written snapshot file. The cutover and
// rollback executors call this to confirm the on-disk file is intact before
// touching any host.
func ReadSnapshot(path string) (*domain.MinioSnapshot, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("snapshot: read %s: %w", path, err)
	}
	var snap domain.MinioSnapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		return nil, fmt.Errorf("snapshot: decode %s: %w", path, err)
	}
	return &snap, nil
}

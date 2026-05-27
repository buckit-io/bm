package migration

import (
	"fmt"

	"github.com/buckit-io/bm/internal/domain"
)

// Summarize derives the wizard-facing counts/headlines shape from a full
// snapshot. The Review step in the migrate wizard renders this exactly; the
// persisted MinioSnapshot keeps full fidelity for cutover/rollback.
func Summarize(snap *domain.MinioSnapshot) domain.MinioSnapshotSummary {
	if snap == nil {
		return domain.MinioSnapshotSummary{Warnings: []string{}}
	}
	// Seed with []string{} (not nil) so a snapshot with zero warnings still
	// marshals to JSON [] — the wire contract is non-nullable string[].
	out := domain.MinioSnapshotSummary{
		Buckets:         len(snap.Buckets),
		Users:           len(snap.Users),
		Groups:          len(snap.Groups),
		CustomPolicies:  len(snap.Policies),
		ServiceAccounts: len(snap.ServiceAccounts),
		Notifications:   len(snap.Notifications),
		Warnings:        append([]string{}, snap.Warnings...),
		OfflineHosts:    append([]string(nil), snap.OfflineHosts...),
	}
	// Per-bucket counters: versioning, lifecycle, object-lock. Lifecycle is
	// counted as bucket-distinct (one bucket with 4 rules counts once) so
	// the wizard renders "2 buckets with lifecycle" not "8 rules".
	versioned := 0
	objectLocked := 0
	bucketsWithLifecycle := map[string]struct{}{}
	for _, b := range snap.Buckets {
		switch b.Versioning {
		case "Enabled", "Suspended":
			versioned++
		}
		if b.ObjectLock {
			objectLocked++
		}
	}
	for _, r := range snap.Lifecycle {
		bucketsWithLifecycle[r.BucketName] = struct{}{}
	}
	out.Versioning = versioned
	out.Lifecycle = len(bucketsWithLifecycle)
	out.ObjectLock = objectLocked
	// Bucket policies are recorded as part of snap.Policies above; the
	// wizard's `policies` field is the bucket-policy count which we don't
	// fetch separately yet. Surface zero with a warning so the operator
	// knows it's not yet captured.
	out.Policies = 0
	// Replication targets are also not yet captured.
	out.ReplicationTargets = 0

	if largest := largestBucket(snap); largest != nil {
		out.LargestBucket = largest
	}
	return out
}

// largestBucket picks the bucket with the highest SizeBytes. Returns nil
// when no bucket has a non-zero size (older MinIO + S3 ListBuckets don't
// surface size; snapshot capture leaves SizeBytes at 0 in that case).
func largestBucket(snap *domain.MinioSnapshot) *domain.BucketHeadline {
	var pick *domain.BucketSnapshot
	for i := range snap.Buckets {
		b := &snap.Buckets[i]
		if pick == nil || b.SizeBytes > pick.SizeBytes {
			pick = b
		}
	}
	if pick == nil || pick.SizeBytes == 0 {
		return nil
	}
	return &domain.BucketHeadline{
		Name: pick.Name,
		Size: humanSize(pick.SizeBytes),
	}
}

// humanSize returns a short human-readable size ("12.4 GiB"). Stays in this
// package so we don't pull a third-party humanize dep just for snapshots.
func humanSize(n int64) string {
	const (
		KiB int64 = 1024
		MiB       = 1024 * KiB
		GiB       = 1024 * MiB
		TiB       = 1024 * GiB
		PiB       = 1024 * TiB
	)
	switch {
	case n >= PiB:
		return fmt.Sprintf("%.1f PiB", float64(n)/float64(PiB))
	case n >= TiB:
		return fmt.Sprintf("%.1f TiB", float64(n)/float64(TiB))
	case n >= GiB:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(GiB))
	case n >= MiB:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(MiB))
	case n >= KiB:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(KiB))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

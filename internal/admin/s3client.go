package admin

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"

	minio "github.com/buckit-io/minio-go/v7"
	"github.com/buckit-io/minio-go/v7/pkg/credentials"

	"github.com/buckit-io/bm/internal/domain"
)

// S3Client is a thin facade over a minio-go *Client. It exposes the bucket-
// level read methods the migration snapshot needs (lifecycle, notifications,
// versioning, object lock, tags). Lives next to admin.Client so the two
// share parseEndpoint and the keepaliveBudget conventions.
type S3Client struct {
	c *minio.Client
}

// NewS3 builds an S3Client from the same AdminCreds the admin client uses.
// The S3 endpoint is the cluster's HTTP/HTTPS address — operators typically
// share it with the admin endpoint.
func NewS3(creds domain.AdminCreds) (*S3Client, error) {
	if creds.URL == "" {
		return nil, errors.New("admin: s3 url is required")
	}
	endpoint, secure, err := parseEndpoint(creds.URL)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport
	if secure && creds.Insecure {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // operator opt-in
		transport = tr
	}
	cli, err := minio.New(endpoint, &minio.Options{
		Creds:     credentials.NewStaticV4(creds.AccessKey, creds.SecretKey, ""),
		Secure:    secure,
		Transport: transport,
	})
	if err != nil {
		return nil, fmt.Errorf("admin: build s3 client: %w", err)
	}
	return &S3Client{c: cli}, nil
}

// ListBuckets returns every bucket the supplied creds can see, including the
// creation timestamp from the S3 ListBuckets response.
func (s *S3Client) ListBuckets(ctx context.Context) ([]domain.BucketSnapshot, error) {
	if s == nil || s.c == nil {
		return nil, errors.New("admin: nil s3 client")
	}
	infos, err := s.c.ListBuckets(ctx)
	if err != nil {
		return nil, classifyError(err)
	}
	out := make([]domain.BucketSnapshot, 0, len(infos))
	for _, b := range infos {
		out = append(out, domain.BucketSnapshot{
			Name:         b.Name,
			CreationDate: b.CreationDate,
		})
	}
	return out, nil
}

// SmokeTest confirms the cluster accepts a basic create/write/read/delete
// cycle using the supplied bucket/object names and payload.
func (s *S3Client) SmokeTest(ctx context.Context, bucket, object string, body []byte) error {
	if s == nil || s.c == nil {
		return errors.New("admin: nil s3 client")
	}
	if err := s.c.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		return classifyError(err)
	}
	cleanupBucket := true
	defer func() {
		if cleanupBucket {
			_ = s.c.RemoveBucket(context.Background(), bucket)
		}
	}()

	reader := bytes.NewReader(body)
	if _, err := s.c.PutObject(ctx, bucket, object, reader, int64(len(body)), minio.PutObjectOptions{}); err != nil {
		return classifyError(err)
	}
	defer func() {
		_ = s.c.RemoveObject(context.Background(), bucket, object, minio.RemoveObjectOptions{})
	}()

	got, err := s.c.GetObject(ctx, bucket, object, minio.GetObjectOptions{})
	if err != nil {
		return classifyError(err)
	}
	defer got.Close()
	gotBody, err := io.ReadAll(got)
	if err != nil {
		return classifyError(err)
	}
	if !bytes.Equal(gotBody, body) {
		return errors.New("admin: smoke object body mismatch")
	}

	if err := s.c.RemoveObject(ctx, bucket, object, minio.RemoveObjectOptions{}); err != nil {
		return classifyError(err)
	}
	if err := s.c.RemoveBucket(ctx, bucket); err != nil {
		return classifyError(err)
	}
	cleanupBucket = false
	return nil
}

// EnrichBucket fills in versioning / object-lock / tags for one bucket.
// Soft-fails per field — a 404 / NotImplemented from older MinIO versions
// records a Warning and leaves the field zero. Caller appends warnings to
// the snapshot.
func (s *S3Client) EnrichBucket(ctx context.Context, b *domain.BucketSnapshot) []string {
	if s == nil || s.c == nil || b == nil {
		return nil
	}
	var warnings []string
	if v, err := s.c.GetBucketVersioning(ctx, b.Name); err == nil {
		b.Versioning = v.Status // "Enabled" / "Suspended" / ""
	} else if !silentS3Err(err) {
		warnings = append(warnings, fmt.Sprintf("%s: versioning: %v", b.Name, err))
	}
	if mode, _, _, _, err := s.c.GetObjectLockConfig(ctx, b.Name); err == nil {
		b.ObjectLock = mode != ""
	} else if !silentS3Err(err) {
		warnings = append(warnings, fmt.Sprintf("%s: object-lock: %v", b.Name, err))
	}
	if tagSet, err := s.c.GetBucketTagging(ctx, b.Name); err == nil && tagSet != nil {
		for k, v := range tagSet.ToMap() {
			b.Tags = append(b.Tags, k+"="+v)
		}
	} else if err != nil && !silentS3Err(err) {
		warnings = append(warnings, fmt.Sprintf("%s: tags: %v", b.Name, err))
	}
	return warnings
}

// BucketLifecycle returns the lifecycle rules for a bucket as a list of
// LifecycleRule entries with the rule body re-encoded as XML. Empty list +
// nil error when the bucket has no rules. silentS3Err'd codes are surfaced
// as warnings via the second return.
func (s *S3Client) BucketLifecycle(ctx context.Context, bucket string) ([]domain.LifecycleRule, []string, error) {
	if s == nil || s.c == nil {
		return nil, nil, errors.New("admin: nil s3 client")
	}
	cfg, err := s.c.GetBucketLifecycle(ctx, bucket)
	if err != nil {
		if silentS3Err(err) {
			return nil, nil, nil
		}
		return nil, []string{fmt.Sprintf("%s: lifecycle: %v", bucket, err)}, nil
	}
	if cfg == nil || len(cfg.Rules) == 0 {
		return nil, nil, nil
	}
	out := make([]domain.LifecycleRule, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		body, mErr := xml.Marshal(r)
		if mErr != nil {
			return nil, nil, fmt.Errorf("admin: marshal lifecycle rule: %w", mErr)
		}
		out = append(out, domain.LifecycleRule{
			BucketName: bucket,
			RuleID:     r.ID,
			RuleXML:    string(body),
		})
	}
	return out, nil, nil
}

// BucketNotifications returns the notification targets for a bucket.
func (s *S3Client) BucketNotifications(ctx context.Context, bucket string) ([]domain.NotificationTarget, []string, error) {
	if s == nil || s.c == nil {
		return nil, nil, errors.New("admin: nil s3 client")
	}
	cfg, err := s.c.GetBucketNotification(ctx, bucket)
	if err != nil {
		if silentS3Err(err) {
			return nil, nil, nil
		}
		return nil, []string{fmt.Sprintf("%s: notifications: %v", bucket, err)}, nil
	}
	out := []domain.NotificationTarget{}
	for _, q := range cfg.QueueConfigs {
		out = append(out, domain.NotificationTarget{
			BucketName: bucket,
			ARN:        q.Queue,
			Events:     stringifyEvents(q.Events),
		})
	}
	for _, t := range cfg.TopicConfigs {
		out = append(out, domain.NotificationTarget{
			BucketName: bucket,
			ARN:        t.Topic,
			Events:     stringifyEvents(t.Events),
		})
	}
	for _, l := range cfg.LambdaConfigs {
		out = append(out, domain.NotificationTarget{
			BucketName: bucket,
			ARN:        l.Lambda,
			Events:     stringifyEvents(l.Events),
		})
	}
	return out, nil, nil
}

// silentS3Err reports whether the error is one we don't bother surfacing.
// Older MinIO releases 404 lifecycle/notification/object-lock when nothing
// is configured; treat those as "feature absent" not "fetch failed".
func silentS3Err(err error) bool {
	if err == nil {
		return true
	}
	resp := minio.ToErrorResponse(err)
	switch resp.Code {
	case "":
		// ToErrorResponse returns zero on plain network errors; let the
		// caller surface that as a warning.
		return false
	case "NoSuchLifecycleConfiguration",
		"NoSuchBucketPolicy",
		"NoSuchTagSet",
		"ServerSideEncryptionConfigurationNotFoundError",
		"ObjectLockConfigurationNotFoundError",
		"NotImplemented":
		return true
	default:
		return false
	}
}

// stringifyEvents converts the minio-go event-type slice into bm's plain []string.
type stringer interface{ String() string }

func stringifyEvents(events any) []string {
	v, ok := events.([]stringer)
	if ok {
		out := make([]string, 0, len(v))
		for _, e := range v {
			out = append(out, e.String())
		}
		return out
	}
	// minio-go's NotificationEvent has String() but the slice type doesn't
	// satisfy []stringer at compile time. Fall back to reflect-free string-format.
	return []string{fmt.Sprintf("%v", events)}
}

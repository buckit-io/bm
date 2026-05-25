package admin

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	madmin "github.com/buckit-io/madmin-go/v3"

	"github.com/buckit-io/bm/internal/domain"
)

// dialTimeout bounds the TCP connect to the admin endpoint. Without this,
// http.DefaultTransport falls back to the kernel's retransmit behaviour on a
// dead host, which means admin calls only fail once the outer request context
// expires (~15s). 3s is comfortable for LAN/WAN clusters while still failing
// fast when the endpoint is down.
const dialTimeout = 3 * time.Second

// tlsHandshakeTimeout caps the TLS handshake. Default is 10s — too generous
// for the refresh path, which budgets ~15s total. Brings TLS-stall failures
// (mis-issued cert, half-up service) within a single refresh cycle.
const tlsHandshakeTimeout = 5 * time.Second

// responseHeaderTimeout caps the wait between request-sent and first byte
// of the response headers. The Go default is 0 (no limit), so a wedged
// admin endpoint that accepts the request but never replies blocks until
// ctx cancels. Common when a TLS terminator stays up while the upstream
// service is stopped — TCP + TLS succeed against the proxy, but nothing
// answers behind it. 5s is well above a healthy ServerInfo's response time.
const responseHeaderTimeout = 5 * time.Second

// Client is a thin facade over a madmin.AdminClient.
type Client struct {
	adm *madmin.AdminClient
}

type ServerUpdateStatus struct {
	DryRun  bool
	Results []ServerUpdateResult
}

type ServerUpdateResult struct {
	Host           string
	CurrentVersion string
	UpdatedVersion string
	Error          string
}

// New builds a Client from cred-pack. The URL may be scheme-qualified
// (http:// or https://); when omitted, https:// is assumed. creds.Insecure
// disables certificate verification — required for self-signed admin
// endpoints. Returns a wrapped error when the URL is malformed.
func New(creds domain.AdminCreds) (*Client, error) {
	if creds.URL == "" {
		return nil, errors.New("admin: url is required")
	}
	endpoint, secure, err := parseEndpoint(creds.URL)
	if err != nil {
		return nil, err
	}
	// Always clone the default transport so we can attach a bounded dialer
	// without mutating the package global.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	tr.DialContext = dialer.DialContext
	tr.TLSHandshakeTimeout = tlsHandshakeTimeout
	tr.ResponseHeaderTimeout = responseHeaderTimeout
	if secure && creds.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // operator opt-in
	}
	transport := http.RoundTripper(tr)
	adm, err := madmin.NewWithOptions(endpoint, &madmin.Options{
		Creds:     credsForOptions(creds.AccessKey, creds.SecretKey),
		Secure:    secure,
		Transport: transport,
	})
	if err != nil {
		return nil, fmt.Errorf("admin: build client: %w", err)
	}
	return &Client{adm: adm}, nil
}

// ServerInfo fetches /minio/admin/v3/info and maps the response into the
// domain-typed shape the rest of bm uses.
func (c *Client) ServerInfo(ctx context.Context) (*domain.ServerInfo, error) {
	if c == nil || c.adm == nil {
		return nil, errors.New("admin: nil client")
	}
	msg, err := c.adm.ServerInfo(ctx)
	if err != nil {
		return nil, classifyError(err)
	}
	return mapServerInfo(&msg), nil
}

// ServiceRestart issues an admin "restart cluster" request. Returns once the
// admin API has acknowledged the request — the actual host restarts happen
// async and callers should poll ServerInfo to confirm completion.
func (c *Client) ServiceRestart(ctx context.Context) error {
	if c == nil || c.adm == nil {
		return errors.New("admin: nil client")
	}
	if err := c.adm.ServiceRestartV2(ctx); err != nil {
		if isExpectedServiceInterruption(err) {
			return nil
		}
		return classifyError(err)
	}
	return nil
}

// ServiceStop stops the cluster. Once this returns, the admin API typically
// becomes unreachable; callers should mark the cluster row as unreachable and
// fall back to SSH-orchestrated `start_cluster` to bring it back up.
func (c *Client) ServiceStop(ctx context.Context) error {
	if c == nil || c.adm == nil {
		return errors.New("admin: nil client")
	}
	if err := c.adm.ServiceStopV2(ctx); err != nil {
		if isExpectedServiceInterruption(err) {
			return nil
		}
		return classifyError(err)
	}
	return nil
}

// ServiceFreeze blocks S3 API writes (reads continue). Used as a precursor to
// migration cutover or maintenance windows.
func (c *Client) ServiceFreeze(ctx context.Context) error {
	if c == nil || c.adm == nil {
		return errors.New("admin: nil client")
	}
	if err := c.adm.ServiceFreezeV2(ctx); err != nil {
		return classifyError(err)
	}
	return nil
}

// ServiceUnfreeze undoes ServiceFreeze.
func (c *Client) ServiceUnfreeze(ctx context.Context) error {
	if c == nil || c.adm == nil {
		return errors.New("admin: nil client")
	}
	if err := c.adm.ServiceUnfreezeV2(ctx); err != nil {
		return classifyError(err)
	}
	return nil
}

// ServerUpdate issues the native Admin API cluster update flow. An empty
// updateURL lets the server use its configured default release channel.
func (c *Client) ServerUpdate(ctx context.Context, updateURL string) (ServerUpdateStatus, error) {
	if c == nil || c.adm == nil {
		return ServerUpdateStatus{}, errors.New("admin: nil client")
	}
	resp, err := c.adm.ServerUpdateV2(ctx, madmin.ServerUpdateOpts{
		UpdateURL: updateURL,
		DryRun:    false,
	})
	if err != nil {
		return ServerUpdateStatus{}, classifyError(err)
	}
	out := ServerUpdateStatus{
		DryRun:  resp.DryRun,
		Results: make([]ServerUpdateResult, 0, len(resp.Results)),
	}
	for _, r := range resp.Results {
		out.Results = append(out.Results, ServerUpdateResult{
			Host:           r.Host,
			CurrentVersion: r.CurrentVersion,
			UpdatedVersion: r.UpdatedVersion,
			Error:          r.Err,
		})
	}
	return out, nil
}

// AccountInfo fetches /minio/admin/v3/info-account.
func (c *Client) AccountInfo(ctx context.Context) (*domain.AccountInfo, error) {
	if c == nil || c.adm == nil {
		return nil, errors.New("admin: nil client")
	}
	info, err := c.adm.AccountInfo(ctx, madmin.AccountOpts{})
	if err != nil {
		return nil, classifyError(err)
	}
	out := &domain.AccountInfo{
		AccountName: info.AccountName,
		Buckets:     make([]string, 0, len(info.Buckets)),
	}
	for _, b := range info.Buckets {
		out.Buckets = append(out.Buckets, b.Name)
	}
	return out, nil
}

// parseEndpoint splits creds.URL into a host[:port] string + TLS flag.
// Accepts: "https://host:9000", "http://host", "host:9000", "host".
func parseEndpoint(raw string) (endpoint string, secure bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, errors.New("admin: empty url")
	}
	if !strings.Contains(raw, "://") {
		// Default to https:// when scheme is omitted.
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, fmt.Errorf("admin: parse url: %w", err)
	}
	switch u.Scheme {
	case "http":
		secure = false
	case "https":
		secure = true
	default:
		return "", false, fmt.Errorf("admin: unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", false, errors.New("admin: missing host")
	}
	return u.Host, secure, nil
}

// classifyError maps a madmin/HTTP error into one of the well-known bm error
// types. Callers further wrap into ImportError when needed.
func classifyError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "no such host"),
		strings.Contains(lower, "timeout"):
		return &Error{Kind: ErrUnreachable, Cause: err}
	case strings.Contains(lower, "access denied"),
		strings.Contains(lower, "signaturedoesnotmatch"),
		strings.Contains(lower, "invalidaccesskeyid"),
		strings.Contains(lower, "forbidden"),
		strings.Contains(lower, "unauthorized"):
		return &Error{Kind: ErrAuth, Cause: err}
	default:
		return &Error{Kind: ErrOther, Cause: err}
	}
}

func isExpectedServiceInterruption(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "connection reset by peer") ||
		strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "unexpected eof") ||
		strings.HasSuffix(lower, ": eof") ||
		lower == "eof"
}

// keepaliveBudget caps the longest single admin call to avoid hanging the
// refresh loop when one cluster is wedged. Callers pass their own timeout via
// ctx; this is a belt-and-braces upper bound.
const keepaliveBudget = 30 * time.Second

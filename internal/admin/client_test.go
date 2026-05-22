package admin

import (
	"errors"
	"strings"
	"testing"

	"github.com/buckit-io/bm/internal/domain"
)

func TestParseEndpoint(t *testing.T) {
	cases := []struct {
		raw    string
		host   string
		secure bool
		wantOk bool
	}{
		{"https://prod-east:9000", "prod-east:9000", true, true},
		{"http://localhost:9000", "localhost:9000", false, true},
		{"prod-east:9000", "prod-east:9000", true, true}, // scheme defaulted
		{"prod-east", "prod-east", true, true},
		{"  https://example.com  ", "example.com", true, true}, // trimmed
		{"ftp://nope", "", false, false},
		{"", "", false, false},
		{"https://", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			host, secure, err := parseEndpoint(tc.raw)
			if (err == nil) != tc.wantOk {
				t.Fatalf("ok=%v err=%v", tc.wantOk, err)
			}
			if err != nil {
				return
			}
			if host != tc.host || secure != tc.secure {
				t.Fatalf("got host=%s secure=%v, want %s/%v", host, secure, tc.host, tc.secure)
			}
		})
	}
}

func TestNewInsecureFlag(t *testing.T) {
	// Just exercise the path that builds the client; we can't observe the
	// transport easily, but the call should succeed and return a non-nil
	// Client for an https URL with Insecure=true.
	c, err := New(domain.AdminCreds{
		URL:       "https://localhost:9000",
		AccessKey: "ak",
		SecretKey: "sk",
		Insecure:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c == nil || c.adm == nil {
		t.Fatal("nil client")
	}
}

func TestClassifyError(t *testing.T) {
	cases := []struct {
		in   error
		kind ErrorKind
	}{
		{errors.New("dial tcp: connection refused"), ErrUnreachable},
		{errors.New("dial tcp: no such host"), ErrUnreachable},
		{errors.New("context deadline exceeded: timeout"), ErrUnreachable},
		{errors.New("SignatureDoesNotMatch"), ErrAuth},
		{errors.New("Access Denied"), ErrAuth},
		{errors.New("400 InvalidAccessKeyId"), ErrAuth},
		{errors.New("internal server error"), ErrOther},
	}
	for _, tc := range cases {
		t.Run(tc.in.Error(), func(t *testing.T) {
			wrapped := classifyError(tc.in)
			var ae *Error
			if !errors.As(wrapped, &ae) {
				t.Fatalf("not an *Error: %v", wrapped)
			}
			if ae.Kind != tc.kind {
				t.Fatalf("want kind %s, got %s", tc.kind, ae.Kind)
			}
			// Round-trip the kind through Error() for sanity.
			if !strings.Contains(ae.Error(), string(tc.kind)) {
				t.Fatalf("Error() missing kind: %s", ae.Error())
			}
		})
	}
}

func TestExpectedServiceInterruption(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "reset", err: errors.New("read tcp 127.0.0.1:1->127.0.0.1:2: read: connection reset by peer"), want: true},
		{name: "broken pipe", err: errors.New("write tcp: broken pipe"), want: true},
		{name: "eof", err: errors.New("EOF"), want: true},
		{name: "unexpected eof", err: errors.New("unexpected EOF"), want: true},
		{name: "timeout", err: errors.New("context deadline exceeded"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isExpectedServiceInterruption(tc.err); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

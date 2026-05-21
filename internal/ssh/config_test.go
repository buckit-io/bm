package ssh

import (
	"testing"

	"github.com/buckit-io/bm/internal/domain"
)

func TestMergeNilOverrideReturnsCluster(t *testing.T) {
	cluster := domain.SshCreds{
		AuthMethod: domain.AuthKey, User: "buckit", KeyPath: "~/.ssh/id_ed25519", Sudo: true,
	}
	r := Merge(cluster, nil)
	if r.User != "buckit" || r.KeyPath != "~/.ssh/id_ed25519" || !r.Sudo {
		t.Fatalf("merge changed cluster defaults: %+v", r)
	}
}

func TestMergeOverridesPerField(t *testing.T) {
	cluster := domain.SshCreds{AuthMethod: domain.AuthKey, User: "buckit", KeyPath: "/k1", Sudo: true}
	user := "ops"
	sudo := false
	method := domain.AuthPassword
	password := "secret"
	r := Merge(cluster, &domain.SshOverrides{
		AuthMethod: &method,
		User:       &user,
		Password:   &password,
		Sudo:       &sudo,
	})
	if r.User != "ops" || r.AuthMethod != domain.AuthPassword || r.Password != "secret" || r.Sudo {
		t.Fatalf("override did not apply: %+v", r)
	}
	if r.KeyPath != "/k1" {
		t.Fatalf("keyPath should inherit when not overridden: %+v", r)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		r    Resolved
		want bool // want error
	}{
		{"missing user", Resolved{AuthMethod: domain.AuthKey, KeyPath: "/k"}, true},
		{"key missing path", Resolved{AuthMethod: domain.AuthKey, User: "u"}, true},
		{"password missing pw", Resolved{AuthMethod: domain.AuthPassword, User: "u"}, true},
		{"key ok", Resolved{AuthMethod: domain.AuthKey, User: "u", KeyPath: "/k"}, false},
		{"password ok", Resolved{AuthMethod: domain.AuthPassword, User: "u", Password: "p"}, false},
		{"agent ok", Resolved{AuthMethod: domain.AuthAgent, User: "u"}, false},
		{"bogus method", Resolved{AuthMethod: "weird", User: "u"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.r.Validate()
			if (err != nil) != tc.want {
				t.Fatalf("want err=%v, got %v", tc.want, err)
			}
		})
	}
}

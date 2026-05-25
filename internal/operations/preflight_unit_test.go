package operations

import (
	"strings"
	"testing"

	"github.com/buckit-io/bm/internal/domain"
)

func TestParseLoadStateLoaded(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		loaded bool
	}{
		{"loaded", "loaded", true},
		{"loaded trailing newline", "loaded\n", true},
		{"loaded leading whitespace", "  loaded ", true},
		{"not-found", "not-found", false},
		{"masked", "masked", false},
		{"error state", "error", false},
		{"empty (systemctl missing)", "", false},
		{"whitespace only", "   \n\t", false},
		{"unexpected token", "Loaded", false}, // case-sensitive on purpose
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseLoadStateLoaded(tc.input); got != tc.loaded {
				t.Fatalf("parseLoadStateLoaded(%q) = %v, want %v", tc.input, got, tc.loaded)
			}
		})
	}
}

func TestLoadStateProbeCommand(t *testing.T) {
	cmd := loadStateProbeCommand("buckit.service")
	// We want a single-line snippet that runs systemctl show with the
	// LoadState property and always exits 0 (the `|| true`). The probe
	// must quote the unit name through shellQuote so an attacker-controlled
	// engine string can't inject extra args.
	for _, want := range []string{
		`systemctl show -p LoadState --value 'buckit.service'`,
		`|| true`,
		`printf`,
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("loadStateProbeCommand missing %q\nfull:\n%s", want, cmd)
		}
	}
}

func TestLoadStateProbeCommandQuotesAdversarialUnit(t *testing.T) {
	// shellQuote should defang anything weird in the unit name. We
	// don't expect ClusterEngine to ever produce these values, but the
	// helper takes a string so cheap insurance is cheap insurance.
	cmd := loadStateProbeCommand("foo; rm -rf /")
	if !strings.Contains(cmd, `'foo; rm -rf /'`) {
		t.Fatalf("loadStateProbeCommand did not single-quote the unit:\n%s", cmd)
	}
}

func TestUnitMissingError_BuckitHintMentionsRecoveryOps(t *testing.T) {
	err := unitMissingError("buckit.service", []string{"node-a"}, domain.EngineBuckit)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "buckit.service is not installed on node-a") {
		t.Fatalf("missing host phrase: %s", msg)
	}
	if !strings.Contains(msg, "Upgrade cluster via Admin API") {
		t.Fatalf("buckit hint should suggest Admin API upgrade: %s", msg)
	}
	if !strings.Contains(msg, "Redeploy software") {
		t.Fatalf("buckit hint should suggest Redeploy software: %s", msg)
	}
}

func TestUnitMissingError_MinIOHintIsDifferent(t *testing.T) {
	err := unitMissingError("minio.service", []string{"m1", "m2"}, domain.EngineMinio)
	msg := err.Error()
	if !strings.Contains(msg, "minio.service is not installed on m1, m2") {
		t.Fatalf("missing host list not formatted: %s", msg)
	}
	if strings.Contains(msg, "Upgrade cluster via Admin API") {
		t.Fatalf("MinIO clusters should NOT be told to use the Buckit-only Admin API upgrade: %s", msg)
	}
	if !strings.Contains(msg, "reinstall MinIO") {
		t.Fatalf("MinIO hint should suggest reinstalling: %s", msg)
	}
}

func TestUnitMissingError_MultipleHostsCommaSeparated(t *testing.T) {
	err := unitMissingError("buckit.service",
		[]string{"node-a", "node-b", "node-c"}, domain.EngineBuckit)
	if !strings.Contains(err.Error(), "node-a, node-b, node-c") {
		t.Fatalf("expected comma-separated hostname list, got: %s", err)
	}
}

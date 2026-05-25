package deploy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/buckit-io/bm/internal/domain"
)

func TestRenderConfigEnvSwitchesToHttpsWhenTLSEnabled(t *testing.T) {
	params := DeployParams{
		Credentials: domain.Credentials{RootUser: "root", RootPassword: "secret"},
		API:         domain.APIPorts{Port: 9000, ConsolePort: 9001},
		Region:      "us-east-1",
		Hosts: []domain.HostRow{
			{Hostname: "node1.example.com"},
			{Hostname: "node2.example.com"},
		},
		Topology: domain.Topology{SelectedMounts: []string{"/data/drive0", "/data/drive1"}},
		TLS:      domain.TLSConfig{Mode: domain.TLSBYO},
	}

	got := renderConfigEnv(params, domain.HostRow{Hostname: "node1.example.com"})
	if !strings.Contains(got, `MINIO_VOLUMES="https://node{1...2}.example.com:9000/data/drive{0...1}/buckit"`) {
		t.Fatalf("expected https volumes:\n%s", got)
	}
	if !strings.Contains(got, "--certs-dir "+CertsDir) {
		t.Fatalf("expected --certs-dir flag in MINIO_OPTS:\n%s", got)
	}
	if !strings.Contains(got, `MINIO_SERVER_URL="https://node1.example.com:9000"`) {
		t.Fatalf("expected https server URL:\n%s", got)
	}
}

func TestRenderConfigEnvStaysHttpWhenTLSOff(t *testing.T) {
	params := DeployParams{
		Credentials: domain.Credentials{RootUser: "root", RootPassword: "secret"},
		API:         domain.APIPorts{Port: 9000, ConsolePort: 9001},
		Region:      "us-east-1",
		Hosts:       []domain.HostRow{{Hostname: "node1"}},
		Topology:    domain.Topology{SelectedMounts: []string{"/data/drive0"}},
	}
	got := renderConfigEnv(params, domain.HostRow{Hostname: "node1"})
	if strings.Contains(got, "https://") {
		t.Fatalf("did not expect https when TLS off:\n%s", got)
	}
	if strings.Contains(got, "--certs-dir") {
		t.Fatalf("did not expect --certs-dir when TLS off:\n%s", got)
	}
}

func TestValidateTLSAcceptsValidCertWithMatchingSANs(t *testing.T) {
	certPEM, keyPEM := genTestCert(t, []string{"node1.example.com", "node2.example.com"}, time.Now().Add(time.Hour))
	p := okParams()
	p.TLS = domain.TLSConfig{Mode: domain.TLSBYO, CertPEM: certPEM, KeyPEM: keyPEM}
	p.Hosts = []domain.HostRow{
		{ID: "h1", Hostname: "node1.example.com"},
		{ID: "h2", Hostname: "node2.example.com"},
	}
	if err := p.validateTLS(); err != nil {
		t.Fatalf("validateTLS() = %v, want nil", err)
	}
}

func TestValidateTLSRejectsExpiredCert(t *testing.T) {
	certPEM, keyPEM := genTestCert(t, []string{"node1.example.com"}, time.Now().Add(-time.Hour))
	p := okParams()
	p.TLS = domain.TLSConfig{Mode: domain.TLSBYO, CertPEM: certPEM, KeyPEM: keyPEM}
	p.Hosts = []domain.HostRow{{ID: "h1", Hostname: "node1.example.com"}}
	err := p.validateTLS()
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("validateTLS() = %v, want expired error", err)
	}
}

func TestValidateTLSRejectsHostNotCoveredBySANs(t *testing.T) {
	certPEM, keyPEM := genTestCert(t, []string{"node1.example.com"}, time.Now().Add(time.Hour))
	p := okParams()
	p.TLS = domain.TLSConfig{Mode: domain.TLSBYO, CertPEM: certPEM, KeyPEM: keyPEM}
	p.Hosts = []domain.HostRow{
		{ID: "h1", Hostname: "node1.example.com"},
		{ID: "h2", Hostname: "node99.example.com"},
	}
	err := p.validateTLS()
	if err == nil || !strings.Contains(err.Error(), "node99") {
		t.Fatalf("validateTLS() = %v, want SAN coverage error for node99", err)
	}
}

func TestValidateTLSRejectsKeyMismatch(t *testing.T) {
	certPEM, _ := genTestCert(t, []string{"node1.example.com"}, time.Now().Add(time.Hour))
	_, otherKey := genTestCert(t, []string{"node1.example.com"}, time.Now().Add(time.Hour))
	p := okParams()
	p.TLS = domain.TLSConfig{Mode: domain.TLSBYO, CertPEM: certPEM, KeyPEM: otherKey}
	p.Hosts = []domain.HostRow{{ID: "h1", Hostname: "node1.example.com"}}
	err := p.validateTLS()
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("validateTLS() = %v, want key-mismatch error", err)
	}
}

func TestValidateTLSSkipsWhenOff(t *testing.T) {
	p := okParams()
	// CertPEM/KeyPEM left empty but Mode=off — should not fail.
	if err := p.validateTLS(); err != nil {
		t.Fatalf("validateTLS() with TLS off = %v, want nil", err)
	}
}

func TestValidateTLSRejectsHttpServerURLWhenTLSOn(t *testing.T) {
	certPEM, keyPEM := genTestCert(t, []string{"node1.example.com"}, time.Now().Add(time.Hour))
	p := okParams()
	p.TLS = domain.TLSConfig{Mode: domain.TLSBYO, CertPEM: certPEM, KeyPEM: keyPEM}
	p.Hosts = []domain.HostRow{{ID: "h1", Hostname: "node1.example.com"}}
	p.ServerURL = "http://lb.example.com:9000"
	err := p.validateTLS()
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("validateTLS() = %v, want https-required error", err)
	}
}

func TestParseLeafCertHandlesIntermediateBeforeLeaf(t *testing.T) {
	// Build a real CA + a leaf signed by it, concatenate intermediate-first
	// (the chain ordering some CA portals emit). parseLeafCert should return
	// the leaf, not the intermediate.
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey (ca): %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Example Intermediate CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create ca cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse ca cert: %v", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey (leaf): %v", err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "node1.example.com"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"node1.example.com"},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}

	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	leafPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}))
	combined := caPEM + leafPEM // intermediate-first ordering

	got, err := parseLeafCert(combined)
	if err != nil {
		t.Fatalf("parseLeafCert: %v", err)
	}
	if err := got.VerifyHostname("node1.example.com"); err != nil {
		t.Fatalf("parseLeafCert picked the wrong cert (got SAN check err %v)", err)
	}
}

func TestParsePrivateKeySkipsLeadingNonKeyBlock(t *testing.T) {
	// Generate a real cert+key, then prepend a "BAG ATTRIBUTES" block (keytool
	// style) to the key PEM. parsePrivateKey should skip the leading block.
	certPEM, keyPEM := genTestCert(t, []string{"node1.example.com"}, time.Now().Add(time.Hour))
	leadingNonKey := "-----BEGIN BAG ATTRIBUTES-----\nQmFnTmFtZTogc29tZS1iYWcK\n-----END BAG ATTRIBUTES-----\n"
	wrappedKey := leadingNonKey + keyPEM

	p := okParams()
	p.TLS = domain.TLSConfig{Mode: domain.TLSBYO, CertPEM: certPEM, KeyPEM: wrappedKey}
	p.Hosts = []domain.HostRow{{ID: "h1", Hostname: "node1.example.com"}}
	if err := p.validateTLS(); err != nil {
		t.Fatalf("validateTLS() with leading BAG ATTRIBUTES = %v, want nil", err)
	}
}

func TestFromDraftNormalizesCRLFInPEM(t *testing.T) {
	crlfCert := "-----BEGIN CERTIFICATE-----\r\nABCD\r\n-----END CERTIFICATE-----\r\n"
	crlfKey := "-----BEGIN PRIVATE KEY-----\r\nEFGH\r\n-----END PRIVATE KEY-----\r\n"
	crlfCA := "-----BEGIN CERTIFICATE-----\r\nIJKL\r\n-----END CERTIFICATE-----\r\n"

	d := domain.NewClusterDraft{
		Name: "c",
		TLS: domain.TLSConfig{
			Mode:        domain.TLSBYO,
			CertPEM:     crlfCert,
			KeyPEM:      crlfKey,
			CABundlePEM: crlfCA,
		},
		Hosts: []domain.HostRow{{Hostname: "node1"}},
	}
	p := FromDraft(d)
	if strings.Contains(p.TLS.CertPEM, "\r") {
		t.Fatalf("CertPEM still contains \\r: %q", p.TLS.CertPEM)
	}
	if strings.Contains(p.TLS.KeyPEM, "\r") {
		t.Fatalf("KeyPEM still contains \\r: %q", p.TLS.KeyPEM)
	}
	if strings.Contains(p.TLS.CABundlePEM, "\r") {
		t.Fatalf("CABundlePEM still contains \\r: %q", p.TLS.CABundlePEM)
	}
}

// TestValidateTLSAcceptsPKCS8KeyUnderECHeader covers the common case where a
// tool emits PKCS#8 DER bytes wrapped in an "EC PRIVATE KEY" PEM header.
func TestValidateTLSAcceptsPKCS8KeyUnderECHeader(t *testing.T) {
	certPEM, _ := genTestCert(t, []string{"node1.example.com"}, time.Now().Add(time.Hour))

	// Generate a P-256 key and marshal it as PKCS#8, then label the PEM block
	// as "EC PRIVATE KEY" (the mislabeling that triggered the bug).
	rawKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	// Parse the cert so we can build a matching cert+key pair.
	certBlock, _ := pem.Decode([]byte(certPEM))
	leaf, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	// Re-sign a cert with rawKey's public key so the pair matches.
	tmpl := &x509.Certificate{
		SerialNumber: leaf.SerialNumber,
		Subject:      leaf.Subject,
		NotBefore:    leaf.NotBefore,
		NotAfter:     leaf.NotAfter,
		DNSNames:     leaf.DNSNames,
		IPAddresses:  leaf.IPAddresses,
		KeyUsage:     leaf.KeyUsage,
		ExtKeyUsage:  leaf.ExtKeyUsage,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &rawKey.PublicKey, rawKey)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	matchedCertPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))

	pkcs8DER, err := x509.MarshalPKCS8PrivateKey(rawKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	// Mislabel: PKCS#8 bytes but "EC PRIVATE KEY" header.
	mislabeledKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: pkcs8DER}))

	p := okParams()
	p.TLS = domain.TLSConfig{Mode: domain.TLSBYO, CertPEM: matchedCertPEM, KeyPEM: mislabeledKeyPEM}
	p.Hosts = []domain.HostRow{{ID: "h1", Hostname: "node1.example.com"}}
	if err := p.validateTLS(); err != nil {
		t.Fatalf("validateTLS() with PKCS8-under-EC-header = %v, want nil", err)
	}
}

// TestValidateTLSAcceptsPKCS8KeyUnderRSAHeader covers the same mislabeling for
// RSA keys — "RSA PRIVATE KEY" header wrapping PKCS#8 DER.
func TestValidateTLSAcceptsPKCS8KeyUnderRSAHeader(t *testing.T) {
	// Use a small RSA key (1024-bit) to keep the test fast.
	rawKey, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // test only
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "test-rsa"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"node1.example.com"},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &rawKey.PublicKey, rawKey)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))

	pkcs8DER, err := x509.MarshalPKCS8PrivateKey(rawKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	// Mislabel: PKCS#8 bytes but "RSA PRIVATE KEY" header.
	mislabeledKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: pkcs8DER}))

	p := okParams()
	p.TLS = domain.TLSConfig{Mode: domain.TLSBYO, CertPEM: certPEM, KeyPEM: mislabeledKeyPEM}
	p.Hosts = []domain.HostRow{{ID: "h1", Hostname: "node1.example.com"}}
	if err := p.validateTLS(); err != nil {
		t.Fatalf("validateTLS() with PKCS8-under-RSA-header = %v, want nil", err)
	}
}

// okParams returns a DeployParams that passes everything except possibly TLS.
// Tests overlay the TLS field before calling validateTLS.
func okParams() DeployParams {
	return DeployParams{
		Name:        "c",
		Credentials: domain.Credentials{RootUser: "root", RootPassword: "secret"},
		API:         domain.APIPorts{Port: 9000, ConsolePort: 9001},
		Region:      "us-east-1",
		Hosts:       []domain.HostRow{{ID: "h1", Hostname: "node1.example.com"}},
		Topology:    domain.Topology{SelectedMounts: []string{"/data/d0"}},
	}
}

// genTestCert creates a self-signed P-256 cert with the given DNS SANs and
// notAfter, returns PEM-encoded cert + key. Used to exercise validateTLS.
func genTestCert(t *testing.T, dnsSANs []string, notAfter time.Time) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     notAfter,
		DNSNames:     dnsSANs,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return string(certPEM), string(keyPEM)
}

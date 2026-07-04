package deploy

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/buckit-io/bm/internal/domain"
)

// CertsDir is where the install pipeline drops the cert material. MinIO's
// --certs-dir flag points here; the deploy passes the flag explicitly so the
// path doesn't depend on the systemd unit's User= home directory.
const CertsDir = "/etc/minio/certs"

// CertPath is the leaf-cert path MinIO reads.
const CertPath = CertsDir + "/public.crt"

// KeyPath is the private-key path MinIO reads.
const KeyPath = CertsDir + "/private.key"

// CABundlePath is where an optional operator-supplied CA bundle is dropped.
// Living under CAs/ matches MinIO's convention for additional trust roots.
const CABundlePath = CertsDir + "/CAs/bm-ca-bundle.crt"

// validateTLS enforces that BYO TLS material parses, the key pairs with
// the leaf cert, the cert isn't already expired, and every cluster host
// is covered by a SAN. Off-mode is a no-op. Called from DeployParams.Validate.
func (p DeployParams) validateTLS() error {
	if !p.TLS.Enabled() {
		return nil
	}
	// Defense-in-depth: the wizard's Next button already enforces this, but a
	// direct API caller could otherwise deploy with TLS on + an http:// server
	// URL — MinIO would then advertise an http URL it never serves, breaking
	// presigned URLs and console redirects silently.
	if u := strings.TrimSpace(p.ServerURL); u != "" {
		if !strings.HasPrefix(strings.ToLower(u), "https://") {
			return errors.New("deploy: serverUrl must use https when TLS is enabled")
		}
	}
	leaf, err := parseLeafCert(p.TLS.CertPEM)
	if err != nil {
		return fmt.Errorf("deploy: tls cert: %w", err)
	}
	if err := parsePrivateKey(p.TLS.KeyPEM, leaf); err != nil {
		return fmt.Errorf("deploy: tls key: %w", err)
	}
	if p.TLS.CABundlePEM != "" {
		if err := parseCABundle(p.TLS.CABundlePEM); err != nil {
			return fmt.Errorf("deploy: tls ca bundle: %w", err)
		}
	}
	now := time.Now()
	if now.After(leaf.NotAfter) {
		return fmt.Errorf("deploy: tls cert: expired %s", leaf.NotAfter.Format(time.RFC3339))
	}
	if now.Before(leaf.NotBefore) {
		return fmt.Errorf("deploy: tls cert: not yet valid (notBefore %s)", leaf.NotBefore.Format(time.RFC3339))
	}
	for _, h := range p.Hosts {
		host := strings.TrimSpace(h.Hostname)
		if host == "" {
			continue
		}
		if err := leaf.VerifyHostname(host); err != nil {
			// Go 1.15+ ignores CN and requires SANs; surface a clearer hint.
			if strings.Contains(err.Error(), "legacy Common Name") {
				return fmt.Errorf("deploy: tls cert does not cover host %q: certificate has no Subject Alternative Names (SANs) — CN-only certs are rejected by Go 1.15+; regenerate the cert with subjectAltName = DNS:%s", host, host)
			}
			return fmt.Errorf("deploy: tls cert does not cover host %q: %w", host, err)
		}
	}
	return nil
}

// LeafCert returns the parsed leaf certificate from the operator-supplied
// PEM. Returns nil with a nil error when TLS is disabled. Exported for
// callers (e.g. the post-deploy health probe) that need to trust the cert.
func (p DeployParams) LeafCert() (*x509.Certificate, error) {
	if !p.TLS.Enabled() {
		return nil, nil
	}
	return parseLeafCert(p.TLS.CertPEM)
}

// ValidateTLSConfig validates standalone TLS material against the supplied
// hostnames. It is used by non-systemd local deployment preparation, where
// there is no full DeployParams value but the same certificate parsing and
// key-pair checks should apply.
func ValidateTLSConfig(tls domain.TLSConfig, hosts []string) error {
	p := DeployParams{TLS: tls}
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		p.Hosts = append(p.Hosts, domain.HostRow{Hostname: h})
	}
	return p.validateTLS()
}

// parseLeafCert returns the leaf certificate from a PEM that may also contain
// intermediates. It tolerates both leaf-first and intermediate-first orderings
// (some CA portals emit chains root-first) by picking the cert that is not the
// issuer of any other cert in the bundle — i.e. the end-entity.
func parseLeafCert(pemBytes string) (*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := []byte(pemBytes)
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("parse: %w", err)
			}
			certs = append(certs, cert)
		}
		rest = next
	}
	if len(certs) == 0 {
		return nil, errors.New("no PEM CERTIFICATE block found")
	}
	if len(certs) == 1 {
		return certs[0], nil
	}
	// Multiple certs: the leaf is the one that did NOT issue any other cert
	// in the bundle. Intermediates and roots are issuers of at least one peer.
	for _, c := range certs {
		issuesAnother := false
		for _, other := range certs {
			if c == other {
				continue
			}
			if bytes.Equal(c.RawSubject, other.RawIssuer) {
				issuesAnother = true
				break
			}
		}
		if !issuesAnother {
			return c, nil
		}
	}
	// No clear leaf (e.g. a cross-signed pair). Fall back to the first parsed
	// cert — preserves the previous behavior for unusual inputs.
	return certs[0], nil
}

func parsePrivateKey(pemBytes string, leaf *x509.Certificate) error {
	// Some tools (keytool exports, certain cert-manager outputs, combined
	// .pem files) prepend non-key PEM blocks like "BAG ATTRIBUTES" or a
	// leading certificate. Loop past those until we find a key block.
	var block *pem.Block
	rest := []byte(pemBytes)
	for {
		b, next := pem.Decode(rest)
		if b == nil {
			break
		}
		switch b.Type {
		case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
			block = b
		}
		if block != nil {
			break
		}
		rest = next
	}
	if block == nil {
		return errors.New("no PEM PRIVATE KEY block found")
	}
	var key any
	var err error
	switch block.Type {
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		// Some tools (openssl genpkey, cloud CLIs) emit PKCS#8 DER under a
		// "RSA PRIVATE KEY" header. Try PKCS#1 first; fall back to PKCS#8.
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			if k, err8 := x509.ParsePKCS8PrivateKey(block.Bytes); err8 == nil {
				key, err = k, nil
			}
		}
	case "EC PRIVATE KEY":
		// Same mismatch can happen with EC keys.
		key, err = x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			if k, err8 := x509.ParsePKCS8PrivateKey(block.Bytes); err8 == nil {
				key, err = k, nil
			}
		}
	default:
		return fmt.Errorf("unsupported PEM block type %q", block.Type)
	}
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if !keyMatchesCert(key, leaf) {
		return errors.New("private key does not match certificate")
	}
	return nil
}

func keyMatchesCert(key any, leaf *x509.Certificate) bool {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		pub, ok := leaf.PublicKey.(*rsa.PublicKey)
		return ok && pub.N.Cmp(k.N) == 0 && pub.E == k.E
	case *ecdsa.PrivateKey:
		pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
		return ok && pub.X.Cmp(k.X) == 0 && pub.Y.Cmp(k.Y) == 0
	case ed25519.PrivateKey:
		pub, ok := leaf.PublicKey.(ed25519.PublicKey)
		if !ok {
			return false
		}
		return string(pub) == string(k.Public().(ed25519.PublicKey))
	default:
		return false
	}
}

func parseCABundle(pemBytes string) error {
	rest := []byte(pemBytes)
	found := false
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return fmt.Errorf("unexpected PEM block type %q in CA bundle", block.Type)
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return err
		}
		found = true
		rest = next
	}
	if !found {
		return errors.New("CA bundle contains no certificates")
	}
	return nil
}

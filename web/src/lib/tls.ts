// Client-side PEM sanity for the deploy-new-cluster wizard's TLS step.
// We don't parse DER here — the Go backend's DeployParams.Validate is the
// source of truth (it x509-parses, checks key↔cert pairing, expiry, and
// SAN coverage of every host before any file lands on a node). The
// helpers below only catch obvious paste mistakes so the operator gets
// instant feedback instead of waiting for preflight.

const CERT_BLOCK = /-----BEGIN CERTIFICATE-----[\s\S]*?-----END CERTIFICATE-----/;

// MinIO supports PKCS#1 ("RSA PRIVATE KEY"), PKCS#8 ("PRIVATE KEY"), and
// SEC1 EC ("EC PRIVATE KEY") private keys.
const KEY_BLOCK = /-----BEGIN (?:RSA |EC )?PRIVATE KEY-----[\s\S]*?-----END (?:RSA |EC )?PRIVATE KEY-----/;

export function looksLikePemCert(text: string): boolean {
  return CERT_BLOCK.test(text);
}

export function looksLikePemKey(text: string): boolean {
  return KEY_BLOCK.test(text);
}

// Async file reader for the "Load from file…" buttons in the TLS UI.
// Resolves to the file's text contents or rejects with the FileReader error.
export function readFileAsText(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ""));
    reader.onerror = () => reject(reader.error ?? new Error("file read failed"));
    reader.readAsText(file);
  });
}

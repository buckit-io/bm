package deploy

import "testing"

func TestSupportedVersionsImmutable(t *testing.T) {
	a := SupportedVersions()
	if a == nil {
		t.Skip("GitHub unreachable — skipping version catalog test")
	}
	if len(a) == 0 {
		t.Fatal("expected at least one supported version")
	}
	// Mutating the copy must not leak back into the package state.
	a[0].Tag = "tampered"
	b := SupportedVersions()
	if b[0].Tag == "tampered" {
		t.Fatal("SupportedVersions returned a shared slice")
	}
}

func TestVersionByTag(t *testing.T) {
	versions := SupportedVersions()
	if versions == nil {
		t.Skip("GitHub unreachable — skipping VersionByTag test")
	}
	// The first release in the catalog should have a tag and an RPM URL.
	first := versions[0]
	if v := VersionByTag(first.Tag); v == nil || v.RpmURL == "" {
		t.Fatalf("%s: want non-nil with URL, got %+v", first.Tag, v)
	}
	if v := VersionByTag("does-not-exist"); v != nil {
		t.Fatalf("missing tag should return nil, got %+v", v)
	}
}

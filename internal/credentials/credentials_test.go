package credentials

import "testing"

func TestValidateRootPasswordAcceptsTypicalSecrets(t *testing.T) {
	cases := []string{
		"hunter22",
		"S3cret!P@ss-9001",
		"~`!@#$%^&*()_+-={}|[]\\:\";'<>?,./",
	}
	for _, c := range cases {
		if err := ValidateRootPassword(c); err != nil {
			t.Fatalf("ValidateRootPassword(%q) returned %v, want nil", c, err)
		}
	}
}

func TestValidateRootPasswordRejectsLengthViolations(t *testing.T) {
	cases := []string{
		"",
		"short",
		"01234567890123456789012345678901234567890", // 41 chars
	}
	for _, c := range cases {
		if err := ValidateRootPassword(c); err == nil {
			t.Fatalf("ValidateRootPassword(%q) = nil, expected length error", c)
		}
	}
}

func TestValidateRootPasswordRejectsNonPrintable(t *testing.T) {
	cases := []string{
		"has space",
		"has\ttab",
		"contains\x00null",
		"hÉllo123",
	}
	for _, c := range cases {
		if err := ValidateRootPassword(c); err == nil {
			t.Fatalf("ValidateRootPassword(%q) = nil, expected printable-ASCII error", c)
		}
	}
}

func TestValidateRootUserAcceptsTypicalNames(t *testing.T) {
	for _, c := range []string{"admin", "root_1", "ops-user", "ABC"} {
		if err := ValidateRootUser(c); err != nil {
			t.Fatalf("ValidateRootUser(%q) returned %v, want nil", c, err)
		}
	}
}

func TestValidateRootUserRejectsBadInputs(t *testing.T) {
	for _, c := range []string{"", "ab", "a b", "name!", "naïve"} {
		if err := ValidateRootUser(c); err == nil {
			t.Fatalf("ValidateRootUser(%q) = nil, expected error", c)
		}
	}
}

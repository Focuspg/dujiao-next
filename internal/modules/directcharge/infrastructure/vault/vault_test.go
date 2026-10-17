package vault

import (
	"fmt"
	"strings"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	v, err := New("vault-key-0123456789", "fingerprint-key-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	ct, ver, err := v.Seal(Secret{Mode: "session", Session: "secret-session-value", PreflightToken: "pt"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ct, "secret-session-value") {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := v.Open(ct, ver)
	if err != nil || got.Session != "secret-session-value" || got.PreflightToken != "pt" {
		t.Fatalf("open: %+v %v", got, err)
	}
	if _, err := v.Open(ct, 99); err == nil {
		t.Fatal("unknown key version must fail")
	}
	other, _ := New("another-vault-key-xyz", "fingerprint-key-0123456789")
	if _, err := other.Open(ct, ver); err == nil {
		t.Fatal("wrong key must fail")
	}
	if s := fmt.Sprintf("%v %#v %s", got, got, got); strings.Contains(s, "secret-session-value") {
		t.Fatalf("secret leaked when formatted: %s", s)
	}
}

func TestNewRejectsWeakOrSharedKeys(t *testing.T) {
	cases := [][2]string{{"", "fingerprint-key-0123456789"}, {"short", "fingerprint-key-0123456789"}, {"vault-key-0123456789", ""}, {"same-key-0123456789", "same-key-0123456789"}}
	for _, c := range cases {
		if _, err := New(c[0], c[1]); err == nil {
			t.Fatalf("expected error for %q/%q", c[0], c[1])
		}
	}
}

func TestFingerprintIsStableAndNormalized(t *testing.T) {
	v, _ := New("vault-key-0123456789", "fingerprint-key-0123456789")
	a := v.Fingerprint("gpt", "User@Example.com ")
	if a == "" || a != v.Fingerprint("GPT", "user@example.com") {
		t.Fatal("fingerprint must be normalized")
	}
	if a == v.Fingerprint("claude", "user@example.com") {
		t.Fatal("different products must differ")
	}
	if v.Fingerprint("gpt", "  ") != "" {
		t.Fatal("empty account must yield empty fingerprint")
	}
	if strings.Contains(a, "example") {
		t.Fatal("fingerprint must not contain account")
	}
}

func TestMaskAccount(t *testing.T) {
	cases := map[string]string{"user@example.com": "u***@example.com", "abcdefgh": "ab***gh", "abc": "a***", "": ""}
	for in, want := range cases {
		if got := MaskAccount(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}

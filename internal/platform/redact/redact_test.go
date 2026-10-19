package redact

import (
	"strings"
	"testing"
)

func TestURLRemovesPassword(t *testing.T) {
	raw := "postgres://alice:supersecret@db.example.com:5432/app?sslmode=require"
	got := URL(raw)

	if strings.Contains(got, "supersecret") {
		t.Fatalf("redacted URL still contains the password: %s", got)
	}
	if !strings.Contains(got, "alice@db.example.com") {
		t.Fatalf("redacted URL lost useful connection information: %s", got)
	}
}

func TestURLWithoutPasswordIsUnchanged(t *testing.T) {
	raw := "redis://cache.example.com:6379"
	if got := URL(raw); got != raw {
		t.Fatalf("URL = %q, want %q", got, raw)
	}
}

func TestInvalidURLIsNotEchoed(t *testing.T) {
	raw := "://this contains a secret"
	if got := URL(raw); strings.Contains(got, "secret") {
		t.Fatalf("invalid URL leaked its contents: %s", got)
	}
}

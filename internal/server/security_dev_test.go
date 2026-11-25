//go:build !production

package server

import (
	"strings"
	"testing"
)

func TestEffectiveCSPDevMode(t *testing.T) {
	t.Setenv("FRONTEND_DEVSERVER_URL", "")
	if got := effectiveCSP(); got != cspPolicy {
		t.Fatalf("expected strict cspPolicy when FRONTEND_DEVSERVER_URL is empty, got %q", got)
	}

	// Non-loopback URLs must fail closed to strict CSP
	for _, bad := range []string{
		"http://evil.example:9245",
		"javascript:alert(1)",
		"http://localhost",
		"ftp://127.0.0.1:9245",
	} {
		t.Setenv("FRONTEND_DEVSERVER_URL", bad)
		if got := effectiveCSP(); got != cspPolicy {
			t.Fatalf("expected strict cspPolicy for %q, got %q", bad, got)
		}
	}

	// Valid loopback dev server relaxes ONLY connect-src for that exact port
	t.Setenv("FRONTEND_DEVSERVER_URL", "http://localhost:9245")
	got := effectiveCSP()
	if !strings.Contains(got, "script-src 'self';") {
		t.Fatalf("script-src must stay strict in dev mode, got %q", got)
	}
	for _, want := range []string{
		"connect-src 'self' ",
		"ws://127.0.0.1:9245",
		"ws://localhost:9245",
		"http://127.0.0.1:9245",
		"http://localhost:9245",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected dev CSP to contain %q, got %q", want, got)
		}
	}
}

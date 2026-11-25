//go:build !production

package server

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// effectiveCSP returns the strict production CSP unless Wails dev mode is
// actively proxying a loopback FRONTEND_DEVSERVER_URL (Vite HMR).
func effectiveCSP() string {
	raw := strings.TrimSpace(os.Getenv("FRONTEND_DEVSERVER_URL"))
	if raw == "" {
		return cspPolicy
	}
	u, err := url.Parse(raw)
	if err != nil {
		return cspPolicy
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return cspPolicy
	}
	host := strings.ToLower(u.Hostname())
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return cspPolicy
	}
	port := u.Port()
	if port == "" || !isDigits(port) {
		return cspPolicy
	}
	wsScheme := "ws"
	if scheme == "https" {
		wsScheme = "wss"
	}
	extraConnect := fmt.Sprintf(
		"connect-src 'self' %s://127.0.0.1:%s %s://localhost:%s %s://127.0.0.1:%s %s://localhost:%s",
		wsScheme, port, wsScheme, port, scheme, port, scheme, port,
	)
	return strings.Replace(cspPolicy, "connect-src 'self'", extraConnect, 1)
}

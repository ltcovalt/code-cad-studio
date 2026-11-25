//go:build production

package server

// effectiveCSP always enforces the strict production CSP in production builds.
func effectiveCSP() string {
	return cspPolicy
}

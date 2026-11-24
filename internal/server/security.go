package server

import (
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const (
	cspPolicy        = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; font-src 'self' data:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'"
	maxJSONBodyBytes = 2 << 20 // 2 MiB
)

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func extractHostname(rawHost string) (string, bool) {
	raw := strings.ToLower(strings.TrimSpace(rawHost))
	if raw == "" {
		return "", false
	}
	if strings.HasPrefix(raw, "[") {
		if h, port, err := net.SplitHostPort(raw); err == nil {
			if h == "" || !isDigits(port) {
				return "", false
			}
			return h, true
		}
		if strings.HasSuffix(raw, "]") {
			inner := raw[1 : len(raw)-1]
			if inner != "" && net.ParseIP(inner) != nil {
				return inner, true
			}
		}
		return "", false
	}
	if ip := net.ParseIP(raw); ip != nil {
		return raw, true
	}
	if strings.Contains(raw, ":") {
		h, port, err := net.SplitHostPort(raw)
		if err != nil || h == "" || !isDigits(port) {
			return "", false
		}
		return h, true
	}
	return raw, true
}

func isAllowedHost(hostname string, allowWailsInternal bool) bool {
	h := strings.ToLower(strings.TrimSpace(hostname))
	if h == "" {
		return false
	}
	switch h {
	case "localhost", "127.0.0.1", "::1", "wails.localhost":
		return true
	case "wails":
		return allowWailsInternal
	}
	if extra := strings.TrimSpace(os.Getenv("CODE_CAD_ALLOWED_HOSTS")); extra != "" {
		for _, item := range strings.Split(extra, ",") {
			cand, ok := extractHostname(item)
			if ok && cand == h {
				return true
			}
		}
	}
	return false
}

func isAllowedOrigin(origin string, allowWailsInternal bool) bool {
	trimmed := strings.TrimSpace(origin)
	if trimmed == "" || strings.EqualFold(trimmed, "null") {
		return false
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	origHost := strings.ToLower(u.Hostname())
	switch scheme {
	case "http", "https":
		return origHost != "" && isAllowedHost(origHost, allowWailsInternal)
	case "wails":
		if !allowWailsInternal {
			return false
		}
		return origHost == "" || origHost == "localhost" || origHost == "wails.localhost" || origHost == "wails"
	default:
		return false
	}
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (s *Server) securityMiddleware(next http.Handler, allowWailsInternal bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", cspPolicy)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/events" {
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
		}

		host, hostOK := extractHostname(r.Host)
		if !hostOK || !isAllowedHost(host, allowWailsInternal) {
			writeError(w, http.StatusForbidden, "forbidden_host", "Forbidden Host header")
			return
		}

		if isMutatingMethod(r.Method) {
			if sfs := strings.TrimSpace(strings.ToLower(r.Header.Get("Sec-Fetch-Site"))); sfs != "" {
				if sfs != "same-origin" && sfs != "none" {
					writeError(w, http.StatusForbidden, "forbidden_origin", "Cross-site requests are forbidden")
					return
				}
			}
			if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
				if !isAllowedOrigin(origin, allowWailsInternal) {
					writeError(w, http.StatusForbidden, "forbidden_origin", "Forbidden Origin header")
					return
				}
			}
		}

		if strings.HasPrefix(r.URL.Path, "/api/") &&
			(r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch) {
			ct := strings.TrimSpace(r.Header.Get("Content-Type"))
			if ct == "" {
				writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
				return
			}
			mediaType, _, err := mime.ParseMediaType(ct)
			if err != nil || strings.ToLower(mediaType) != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

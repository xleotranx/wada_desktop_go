package server

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// HostAllowed validates an HTTP Host header against localhost, literal IPs, and
// configured extra patterns (Vite-style: leading "." matches subdomains, "true" allows all).
func HostAllowed(host string, extra []string) bool {
	if host == "" {
		return false
	}
	for _, entry := range extra {
		if strings.TrimSpace(entry) == "true" {
			return true
		}
	}
	hostname := stripPort(host)
	if hostname == "" {
		return false
	}
	lower := strings.ToLower(hostname)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return true
	}
	if net.ParseIP(hostname) != nil {
		return true
	}
	for _, raw := range extra {
		pattern := strings.ToLower(strings.TrimSpace(raw))
		if pattern == "" {
			continue
		}
		if strings.HasPrefix(pattern, ".") {
			if lower == pattern[1:] || strings.HasSuffix(lower, pattern) {
				return true
			}
			continue
		}
		if lower == pattern {
			return true
		}
	}
	return false
}

// OriginAllowed checks browser Origin headers on WebSocket upgrades using the
// same host allowlist rules when an Origin header is supplied.
func OriginAllowed(r *http.Request, extra []string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return HostAllowed(u.Host, extra)
}

func stripPort(hostport string) string {
	hostport = strings.TrimSpace(hostport)
	if strings.HasPrefix(hostport, "[") {
		if h, _, err := net.SplitHostPort(hostport); err == nil {
			return h
		}
		return strings.Trim(hostport, "[]")
	}
	if strings.Count(hostport, ":") == 1 {
		if h, _, err := net.SplitHostPort(hostport); err == nil {
			return h
		}
	}
	return hostport
}

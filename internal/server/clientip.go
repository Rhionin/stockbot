package server

import (
	"net"
	"net/http"
	"strings"
)

const (
	clientIPHeader = "X-Pantry-Client-IP"
	entryHeader    = "X-Pantry-Entry"
	entryPublic    = "public"
)

// visitorIP is the client address Caddy forwarded. CF-Connecting-IP is the
// Cloudflare visitor. X-Forwarded-For is the first address in that list when
// trustProxy is true. X-Pantry-Client-IP remains for a proxy that still sets
// it. A value that is not one IP is ignored so a forged list cannot rotate
// the rate limit.
func visitorIP(r *http.Request, trustProxy bool) string {
	if r == nil {
		return ""
	}
	if ip := oneIP(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	if trustProxy {
		if ip := oneIP(firstForwarded(r.Header.Get("X-Forwarded-For"))); ip != "" {
			return ip
		}
	}
	return oneIP(r.Header.Get(clientIPHeader))
}

func firstForwarded(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	first, _, _ := strings.Cut(raw, ",")
	return strings.TrimSpace(first)
}

func oneIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, " \t,") || net.ParseIP(raw) == nil {
		return ""
	}
	return raw
}

func clientKey(r *http.Request) string {
	ip := visitorIP(r, true)
	if ip == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip = host
	}
	return "ip:" + ip
}

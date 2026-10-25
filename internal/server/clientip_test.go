package server

import (
	"net/http/httptest"
	"testing"
)

func TestVisitorIPTrustProxy(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.10, 198.51.100.1")
	req.RemoteAddr = "10.0.0.5:1234"

	if got := visitorIP(req, true); got != "203.0.113.10" {
		t.Fatalf("trustProxy true XFF = %q", got)
	}
	if got := visitorIP(req, false); got != "" {
		t.Fatalf("trustProxy false XFF ignored = %q", got)
	}
	req.Header.Set("X-Pantry-Client-IP", "203.0.113.20")
	if got := visitorIP(req, false); got != "203.0.113.20" {
		t.Fatalf("client header = %q", got)
	}
}

func TestClientKeyUsesRemoteAddrHostPort(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.0.2.44:51515"
	if got := clientKey(req); got != "ip:192.0.2.44" {
		t.Fatalf("clientKey = %q", got)
	}
}

func TestVisitorIPFallsBackToClientHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Pantry-Client-IP", "198.51.100.99")
	if got := visitorIP(req, false); got != "198.51.100.99" {
		t.Fatalf("visitorIP = %q", got)
	}
}

func TestVisitorIPPrefersCloudflareConnectingIP(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("CF-Connecting-IP", "203.0.113.55")
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	if got := visitorIP(req, true); got != "203.0.113.55" {
		t.Fatalf("visitorIP = %q", got)
	}
}

func TestVisitorIPNilRequest(t *testing.T) {
	if got := visitorIP(nil, true); got != "" {
		t.Fatalf("visitorIP(nil) = %q", got)
	}
}

func TestClientKeyBareRemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.0.2.77"
	if got := clientKey(req); got != "ip:192.0.2.77" {
		t.Fatalf("clientKey = %q", got)
	}
}

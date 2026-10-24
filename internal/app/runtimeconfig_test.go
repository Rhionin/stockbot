package app

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envFrom(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func TestLoadRuntimeConfigDefaults(t *testing.T) {
	cfg, err := LoadRuntimeConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppBaseURL != "" || cfg.PublicHost != "" {
		t.Fatalf("unset base URL = %q host %q", cfg.AppBaseURL, cfg.PublicHost)
	}
	if cfg.AppName != DefaultAppName || DefaultAppName != "Stockbot" {
		t.Fatalf("app name = %q", cfg.AppName)
	}
	if DefaultAppBaseURL != "https://stockbot.rhionin.com" {
		t.Fatalf("documented Pi origin = %q", DefaultAppBaseURL)
	}
	if cfg.LANPeople != LANPeopleRedirect || cfg.ExpoMinVersion != DefaultExpoMinVersion {
		t.Fatalf("lan %q expo %q", cfg.LANPeople, cfg.ExpoMinVersion)
	}
	if cfg.EmailFrom != "" || cfg.CookieDomain != "" {
		t.Fatalf("mail %q domain %q", cfg.EmailFrom, cfg.CookieDomain)
	}
	if !cfg.PublicListenerEnabled() || cfg.PublicListenAddr() != DefaultPublicListenAddr {
		t.Fatalf("public listener %+v", cfg)
	}
	cookie := cfg.SessionCookie()
	if cookie.Name != DefaultSessionCookieName || cookie.Domain != "" || cookie.Path != "/" || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie %+v", cookie)
	}
	listeners := cfg.Listeners()
	if len(listeners) != 1 || listeners[0].Addr != DefaultLANListenAddr || listeners[0].Entry != EntryLAN {
		t.Fatalf("listeners %+v", listeners)
	}
	if cfg.AbsoluteURL("/invite/abc") != "" || cfg.AllowsOrigin("https://pantry.example") {
		t.Fatal("an unset base URL built a link or allowed an origin")
	}
	if !cfg.ExpoUpgradeRequired("expo/0.9.0") {
		t.Fatal("0.9.0 was not below the default minimum")
	}
}

func TestLoadRuntimeConfigPiOriginAndListeners(t *testing.T) {
	cfg, err := LoadRuntimeConfig(envFrom(map[string]string{
		"APP_BASE_URL":        DefaultAppBaseURL + "/",
		"PUBLIC_HOST":         "Stockbot.rhionin.com.",
		"APP_NAME":            "  Northwind  ",
		"EMAIL_FROM":          "notices@pantry.example",
		"EXPO_MIN_VERSION":    "1.2",
		"LAN_PEOPLE":          "SIGNIN",
		"SESSION_COOKIE_NAME": DefaultSessionCookieName,
		"COOKIE_DOMAIN":       ".rhionin.com",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppBaseURL != DefaultAppBaseURL || cfg.PublicHost != "stockbot.rhionin.com" {
		t.Fatalf("origin %q host %q", cfg.AppBaseURL, cfg.PublicHost)
	}
	if cfg.AppName != "Northwind" || cfg.EmailFrom != "notices@pantry.example" || cfg.LANPeople != LANPeopleSignIn || cfg.ExpoMinVersion != "1.2" {
		t.Fatalf("identity %+v", cfg)
	}
	cookie := cfg.SessionCookie()
	if cookie.Name != "stockbot_session" || cookie.Domain != "rhionin.com" || !cookie.Secure {
		t.Fatalf("cookie %+v", cookie)
	}
	listeners := cfg.Listeners()
	if len(listeners) != 2 || listeners[0].Entry != EntryLAN || listeners[0].Addr != ":8080" || listeners[1].Entry != EntryPublic || listeners[1].Addr != ":8081" {
		t.Fatalf("listeners %+v", listeners)
	}
	if !cfg.AllowsOrigin("HTTPS://STOCKBOT.RHIONIN.COM") || cfg.AllowsOrigin("https://evil.example") {
		t.Fatal("origin check did not use the canonical public origin")
	}
	if cfg.AbsoluteURL("invite/code") != DefaultAppBaseURL+"/invite/code" {
		t.Fatalf("link = %q", cfg.AbsoluteURL("invite/code"))
	}
	if cfg.ExpoUpgradeRequired("expo/1.2.0") || cfg.ExpoUpgradeRequired("expo/1.2.0-beta") || cfg.ExpoUpgradeRequired("web/1.0.0") || cfg.ExpoUpgradeRequired("") {
		t.Fatal("clients at the minimum, a web client, or a missing header were gated")
	}
	if !cfg.ExpoUpgradeRequired("expo/1.1.9") || !cfg.ExpoUpgradeRequired("expo/nope") || !cfg.ExpoUpgradeRequired("expo/") {
		t.Fatal("an old or unreadable Expo client was allowed")
	}
}

func TestLoadRuntimeConfigLANOnlySkipsPublicListener(t *testing.T) {
	cfg, err := LoadRuntimeConfig(envFrom(map[string]string{
		"PUBLIC_LISTEN_ADDR": "off",
		"ADDR":               "127.0.0.1:9090",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicListenerEnabled() || cfg.PublicListenAddr() != "" {
		t.Fatalf("public listener stayed on: %+v", cfg)
	}
	listeners := cfg.Listeners()
	if len(listeners) != 1 || listeners[0].Addr != "127.0.0.1:9090" || listeners[0].Entry != EntryLAN {
		t.Fatalf("listeners %+v", listeners)
	}
}

func TestLoadRuntimeConfigLANListenAddrOverridesADDR(t *testing.T) {
	cfg, err := LoadRuntimeConfig(envFrom(map[string]string{
		"ADDR":                ":9090",
		"LAN_LISTEN_ADDR":     "127.0.0.1:9091",
		"APP_BASE_URL":        "https://pantry.example:8443",
		"PUBLIC_LISTEN_ADDR":  ":9443",
		"SESSION_COOKIE_NAME": "pantry_session",
		"COOKIE_DOMAIN":       "pantry.example",
		"PUBLIC_HOST":         "https://pantry.example",
	}))
	if err != nil {
		t.Fatal(err)
	}
	cookie := cfg.SessionCookie()
	if cookie.Name != "pantry_session" || cookie.Domain != "pantry.example" || !cookie.Secure {
		t.Fatalf("cookie %+v", cookie)
	}
	listeners := cfg.Listeners()
	if cfg.AppBaseURL != "https://pantry.example:8443" || cfg.PublicHost != "pantry.example" {
		t.Fatalf("origin %q host %q", cfg.AppBaseURL, cfg.PublicHost)
	}
	if len(listeners) != 2 || listeners[0].Addr != "127.0.0.1:9091" || listeners[1].Addr != ":9443" {
		t.Fatalf("listeners %+v", listeners)
	}
}

func TestLoadRuntimeConfigRejects(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "public listener off", env: map[string]string{"APP_BASE_URL": "https://pantry.example", "PUBLIC_LISTEN_ADDR": "disabled"}, want: "public listener is off"},
		{name: "same listener", env: map[string]string{"APP_BASE_URL": "https://pantry.example", "LAN_LISTEN_ADDR": ":8080", "PUBLIC_LISTEN_ADDR": "0.0.0.0:8080"}, want: "different addresses"},
		{name: "path in base URL", env: map[string]string{"APP_BASE_URL": "https://pantry.example/invite"}, want: "no path"},
		{name: "user in base URL", env: map[string]string{"APP_BASE_URL": "https://ada@pantry.example"}, want: "no path"},
		{name: "bad scheme", env: map[string]string{"APP_BASE_URL": "ftp://pantry.example"}, want: "http or https"},
		{name: "host mismatch", env: map[string]string{"APP_BASE_URL": "https://pantry.example", "PUBLIC_HOST": "other.example"}, want: "PUBLIC_HOST must match"},
		{name: "bad lan people", env: map[string]string{"LAN_PEOPLE": "open"}, want: "redirect or signin"},
		{name: "bad expo version", env: map[string]string{"EXPO_MIN_VERSION": "latest"}, want: "1.0.0"},
		{name: "bad cookie domain", env: map[string]string{"COOKIE_DOMAIN": "https://pantry.example"}, want: "hostname"},
		{name: "domain is not the public host", env: map[string]string{"APP_BASE_URL": "https://pantry.example", "COOKIE_DOMAIN": "other.example"}, want: "parent"},
		{name: "cookie name", env: map[string]string{"SESSION_COOKIE_NAME": "session=1"}, want: "cookie token"},
		{name: "host prefix with domain leftover", env: map[string]string{"COOKIE_DOMAIN": "pantry.example", "SESSION_COOKIE_NAME": "__Host-"}, want: "__Host-"},
		{name: "app name", env: map[string]string{"APP_NAME": "North\nwind"}, want: "line breaks"},
		{name: "email", env: map[string]string{"EMAIL_FROM": "not-an-email"}, want: "email address"},
		{name: "lan address", env: map[string]string{"LAN_LISTEN_ADDR": "8080"}, want: "host:port"},
		{name: "public address", env: map[string]string{"PUBLIC_LISTEN_ADDR": "http://pantry.example"}, want: "host:port"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadRuntimeConfig(envFrom(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestEntryFromContext(t *testing.T) {
	if _, ok := EntryFrom(nil); ok {
		t.Fatal("nil context had an entry")
	}
	ctx := WithEntry(nil, EntryPublic)
	got, ok := EntryFrom(ctx)
	if !ok || got != EntryPublic {
		t.Fatalf("entry = %q ok %v", got, ok)
	}
	if _, ok := EntryFrom(WithEntry(ctx, Entry("other"))); ok {
		t.Fatal("an unknown entry was accepted")
	}
}

func TestLoadRuntimeConfigStripsDefaultPorts(t *testing.T) {
	cfg, err := LoadRuntimeConfig(envFrom(map[string]string{
		"APP_BASE_URL":       "https://pantry.example:443",
		"LAN_LISTEN_ADDR":    "[::1]:9090",
		"PUBLIC_LISTEN_ADDR": "[::1]:9091",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppBaseURL != "https://pantry.example" || cfg.PublicHost != "pantry.example" {
		t.Fatalf("origin %q host %q", cfg.AppBaseURL, cfg.PublicHost)
	}
	listeners := cfg.Listeners()
	if len(listeners) != 2 || listeners[0].Addr != "[::1]:9090" || listeners[1].Addr != "[::1]:9091" {
		t.Fatalf("listeners %+v", listeners)
	}
	if !cfg.AllowsOrigin("https://pantry.example") {
		t.Fatal("origin without the default port was rejected")
	}
}

func TestExpoVersionTreatsMissingPartsAsZero(t *testing.T) {
	cfg, err := LoadRuntimeConfig(envFrom(map[string]string{"EXPO_MIN_VERSION": "1.0.0"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExpoUpgradeRequired("expo/1") || cfg.ExpoUpgradeRequired("expo/1.0") {
		t.Fatal("1 and 1.0 were treated as older than 1.0.0")
	}
	if !cfg.ExpoUpgradeRequired("expo/0") {
		t.Fatal("0 was not below 1.0.0")
	}
}

func TestProductionGoHasNoHardCodedPublicOrigin(t *testing.T) {
	root := filepath.Join("..", "..")
	var hits []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.HasSuffix(path, filepath.Join("internal", "app", "runtimeconfig.go")) {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(body), "stockbot.rhionin.com") {
			hits = append(hits, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 0 {
		t.Fatalf("public origin is hard-coded outside config defaults: %s", strings.Join(hits, ", "))
	}
}

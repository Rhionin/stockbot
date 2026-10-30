package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestPublicProxyIsOptIn locks the deploy contract for internet access:
// a normal compose up publishes Stockbot on loopback only, and the public
// profile is the only thing that publishes 80/443 in front of it.
func TestPublicProxyIsOptIn(t *testing.T) {
	composePath := filepath.Join("..", "..", "deploy", "docker-compose.yml")
	data, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("read compose file: %v", err)
	}

	var compose struct {
		Services map[string]struct {
			Image       string            `yaml:"image"`
			Profiles    []string          `yaml:"profiles"`
			Ports       []string          `yaml:"ports"`
			Environment map[string]string `yaml:"environment"`
			Volumes     []string          `yaml:"volumes"`
			SecurityOpt []string          `yaml:"security_opt"`
			CapDrop     []string          `yaml:"cap_drop"`
			ReadOnly    bool              `yaml:"read_only"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &compose); err != nil {
		t.Fatalf("parse compose file: %v", err)
	}

	pantry, ok := compose.Services["pantry"]
	if !ok {
		t.Fatal("pantry service missing")
	}
	if len(pantry.Profiles) != 0 {
		t.Fatalf("pantry must stay in the default project, got profiles %v", pantry.Profiles)
	}
	if !hasContainerPort(pantry.Ports, "8080") {
		t.Fatalf("pantry must still publish the container port 8080, got %v", pantry.Ports)
	}
	if len(pantry.Ports) != 1 || !strings.HasPrefix(pantry.Ports[0], "127.0.0.1:") {
		t.Fatalf("pantry must publish on loopback, got %v", pantry.Ports)
	}
	if !hasExactPort(pantry.SecurityOpt, "no-new-privileges:true") {
		t.Fatalf("pantry security_opt = %v", pantry.SecurityOpt)
	}
	if !hasExactPort(pantry.CapDrop, "ALL") {
		t.Fatalf("pantry cap_drop = %v", pantry.CapDrop)
	}
	if !pantry.ReadOnly {
		t.Fatal("pantry root filesystem must be read-only; the database volume stays writable")
	}

	caddy, ok := compose.Services["caddy"]
	if !ok {
		t.Fatal("caddy service missing")
	}
	if len(caddy.Profiles) != 1 || caddy.Profiles[0] != "public" {
		t.Fatalf("caddy must be opt-in via the public profile, got %v", caddy.Profiles)
	}
	// The compose files are the pin. A version bump only has to change those
	// files; this test checks the tag is explicit and that both files agree.
	if !pinnedCaddyImage(caddy.Image) {
		t.Fatalf("caddy image = %q, want a pinned caddy:<version> tag", caddy.Image)
	}
	for _, want := range []string{"80:80", "443:443", "443:443/udp"} {
		if !hasExactPort(caddy.Ports, want) {
			t.Fatalf("caddy ports %v missing %q", caddy.Ports, want)
		}
	}
	if caddy.Environment["PUBLIC_HOST"] == "" || caddy.Environment["ACME_EMAIL"] == "" {
		t.Fatal("caddy must receive PUBLIC_HOST and ACME_EMAIL from the deployment .env")
	}
	if hasExactPort(caddy.Volumes, "./auth.caddy:/etc/caddy/auth.caddy:ro") {
		t.Fatal("caddy must not mount auth.caddy; the login page reads the hash inside pantry")
	}
	if pantry.Environment["PANTRY_AUTH_FILE"] != "" {
		t.Fatalf("PANTRY_AUTH_FILE must be unset after household-password retirement, got %q", pantry.Environment["PANTRY_AUTH_FILE"])
	}
	if !strings.Contains(pantry.Environment["PANTRY_SESSION_SECRET"], "PANTRY_SESSION_SECRET") {
		t.Fatalf("pantry must receive PANTRY_SESSION_SECRET from .env, got %q", pantry.Environment["PANTRY_SESSION_SECRET"])
	}
	for _, key := range []string{"VAPID_PUBLIC_KEY", "VAPID_PRIVATE_KEY", "VAPID_SUBJECT", "PUBLIC_HOST", "LEGACY_PUBLIC_HOST", "SENTRY_DSN", "SENTRY_ENVIRONMENT", "SENTRY_TUNNEL_PROJECT_IDS", "SENTRY_TUNNEL_PUBLIC_KEYS", "APP_BASE_URL", "APP_NAME", "SESSION_COOKIE_NAME", "COOKIE_DOMAIN", "EMAIL_FROM", "EXPO_MIN_VERSION", "ADDR"} {
		if !strings.Contains(pantry.Environment[key], key) {
			t.Fatalf("pantry must receive %s from .env, got %q", key, pantry.Environment[key])
		}
	}
	if hasExactPort(pantry.Volumes, "./auth:/etc/pantry/auth:ro") {
		t.Fatalf("pantry must not mount the retired household password directory, got %v", pantry.Volumes)
	}
	if !hasExactPort(pantry.Volumes, "./setup:/run/setup") {
		t.Fatalf("pantry must mount the setup directory read-write, got %v", pantry.Volumes)
	}
	for _, vol := range pantry.Volumes {
		if strings.Contains(vol, "/run/setup") && strings.HasSuffix(vol, ":ro") {
			t.Fatalf("setup mount must be read-write, got %q", vol)
		}
	}
	for _, port := range pantry.Ports {
		if strings.Contains(port, "8081") {
			t.Fatalf("pantry must not publish 8081, got %v", pantry.Ports)
		}
	}
	appBaseExpr := pantry.Environment["APP_BASE_URL"]
	if !strings.Contains(appBaseExpr, "${PUBLIC_HOST:+https://${PUBLIC_HOST}}") {
		t.Fatalf("APP_BASE_URL = %q, want the PUBLIC_HOST fallback", appBaseExpr)
	}
	if got := expandCompose(t, appBaseExpr, nil); got != "" {
		t.Fatalf("APP_BASE_URL with PUBLIC_HOST unset = %q, want empty", got)
	}
	if got := expandCompose(t, appBaseExpr, map[string]string{"PUBLIC_HOST": "stockbot.rhionin.com"}); got != "https://stockbot.rhionin.com" {
		t.Fatalf("APP_BASE_URL from PUBLIC_HOST = %q", got)
	}
	if got := expandCompose(t, appBaseExpr, map[string]string{"APP_BASE_URL": "https://operator.example", "PUBLIC_HOST": "stockbot.rhionin.com"}); got != "https://operator.example" {
		t.Fatalf("a set APP_BASE_URL expanded to %q", got)
	}
	if !hasExactPort(caddy.SecurityOpt, "no-new-privileges:true") {
		t.Fatalf("caddy security_opt = %v", caddy.SecurityOpt)
	}

	caddyfile, err := os.ReadFile(filepath.Join("..", "..", "deploy", "Caddyfile"))
	if err != nil {
		t.Fatalf("read Caddyfile: %v", err)
	}
	text := string(caddyfile)
	for _, want := range []string{
		"{$PUBLIC_HOST}",
		"{$ACME_EMAIL}",
		"X-Pantry-Client-IP {client_ip}",
		"header_up -X-Pantry-Entry",
		"header_up X-Forwarded-For {client_ip}",
		"header_up X-Forwarded-Proto https",
		"path /api/telemetry /api/telemetry/client",
		"path /brand/logo.png /terms /privacy",
		"path /api/events",
		"Do not add other API paths to this matcher.",
		"This is not an API exception.",
		"reverse_proxy pantry:8080",
		"flush_interval -1",
		"route {",
		"Strict-Transport-Security",
		"X-Content-Type-Options",
		"camera=(self)",
		"admin off",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Caddyfile missing %q", want)
		}
	}
	if strings.Contains(text, "import auth.caddy") || strings.Contains(text, "basic_auth") {
		t.Fatal("Caddyfile must not challenge the browser with basic auth")
	}
	if strings.Contains(text, "pantry:8081") || strings.Contains(text, "header_up X-Pantry-Entry ") || strings.Contains(text, "X-Pantry-Entry public") {
		t.Fatal("Caddyfile must proxy to pantry:8080 and must not stamp an entry header")
	}
	if strings.Count(text, "reverse_proxy") != strings.Count(text, "reverse_proxy pantry:8080") {
		t.Fatal("every Caddyfile reverse_proxy must target pantry:8080")
	}
	const householdSite = "# Everything else is the household site."
	authAt := strings.Index(text, householdSite)
	if authAt < 0 {
		t.Fatal("Caddyfile must mark the household site handle")
	}
	// Only these exact paths are reachable before the household login.
	// Anything else, including /favicon.ico, /health, and the rest of /brand,
	// stays in the login handle.
	publicMatchers := pathMatchers(text[:authAt])
	wantPublic := []string{
		"path /api/telemetry /api/telemetry/client",
		"path /api/build",
		"path /brand/logo.png /terms /privacy",
		"path /api/deploy-hook",
	}
	if strings.Join(publicMatchers, "\n") != strings.Join(wantPublic, "\n") {
		t.Fatalf("public path matchers = %#v, want %#v", publicMatchers, wantPublic)
	}
	for _, matcher := range publicMatchers {
		if strings.Contains(matcher, "*") {
			t.Fatalf("public matcher %q is a prefix", matcher)
		}
	}
	telemetryMatcher := publicMatchers[0]
	for _, legal := range []string{"/brand/", "/terms", "/privacy"} {
		if strings.Contains(telemetryMatcher, legal) {
			t.Fatalf("telemetry matcher must not include %s", legal)
		}
	}
	if !strings.Contains(text, "max_size 1MB") {
		t.Fatal("Caddyfile must cap request bodies at 1MB")
	}
	deviceAt := strings.Index(text, "handle /api/device/*")
	if deviceAt < 0 || deviceAt > authAt {
		t.Fatal("Caddyfile must handle /api/device/* before the household site")
	}
	deviceBlock := text[deviceAt:authAt]
	if !strings.Contains(deviceBlock, "max_size 64KB") {
		t.Fatal("Caddyfile device handle must cap request bodies at 64KB")
	}
	if !strings.Contains(deviceBlock, "X-Pantry-Client-IP {client_ip}") || !strings.Contains(deviceBlock, "reverse_proxy pantry:8080") || !strings.Contains(deviceBlock, "header_up -X-Pantry-Entry") {
		t.Fatal("Caddyfile device handle must proxy to pantry:8080, strip the entry header, and pass the visitor address")
	}
	if strings.Contains(deviceBlock, "header_up X-Pantry-Entry ") {
		t.Fatal("Caddyfile device handle must not stamp an entry header")
	}
	authedMatchers := pathMatchers(text[authAt:])
	if strings.Join(authedMatchers, "\n") != "path /api/events\npath /sw.js" {
		t.Fatalf("path matchers after the login handle = %#v, want /api/events and /sw.js", authedMatchers)
	}
	beforeAuth := text[:authAt]
	for _, closed := range []string{"/api/inventory", "/api/scans", "/api/events", "/health", "/favicon.ico"} {
		if strings.Contains(beforeAuth, closed) {
			t.Fatalf("path %s is outside the login handle", closed)
		}
	}

	envExample, err := os.ReadFile(filepath.Join("..", "..", "deploy", ".env.example"))
	if err != nil {
		t.Fatalf("read .env.example: %v", err)
	}
	envText := string(envExample)
	if !strings.Contains(envText, "\nPUBLIC_HOST=\n") || !strings.Contains(envText, "\nACME_EMAIL=\n") {
		t.Fatal(".env.example must leave PUBLIC_HOST and ACME_EMAIL empty so install stays LAN-only")
	}
	if !strings.Contains(envText, "\nLEGACY_PUBLIC_HOST=\n") {
		t.Fatal(".env.example must leave LEGACY_PUBLIC_HOST empty so a new install does not redirect")
	}
	if !strings.Contains(envText, "\nPANTRY_SESSION_SECRET=\n") {
		t.Fatal(".env.example must leave PANTRY_SESSION_SECRET empty so setup can fill it once")
	}
	if strings.Contains(envText, "BASIC_AUTH_PASSWORD") || strings.Contains(envText, "BASIC_AUTH_USER") {
		t.Fatal(".env.example must not document the retired shared household password")
	}
	if !strings.Contains(envText, "\nVAPID_PUBLIC_KEY=\n") || !strings.Contains(envText, "\nVAPID_PRIVATE_KEY=\n") || !strings.Contains(envText, "\nVAPID_SUBJECT=\n") {
		t.Fatal(".env.example must leave VAPID keys empty so setup can fill them once")
	}
	if !strings.Contains(envText, "\nSENTRY_DSN=\n") || !strings.Contains(envText, "\nSENTRY_ENVIRONMENT=\n") || !strings.Contains(envText, "\nSENTRY_TUNNEL_PROJECT_IDS=\n") || !strings.Contains(envText, "\nSENTRY_TUNNEL_PUBLIC_KEYS=\n") {
		t.Fatal(".env.example must leave Sentry values empty")
	}
	if !strings.Contains(envText, "\nBACKUP_BEFORE_DEPLOY=required\n") {
		t.Fatal(".env.example must default BACKUP_BEFORE_DEPLOY to required")
	}

	setup, err := os.ReadFile(filepath.Join("..", "..", "deploy", "setup.sh"))
	if err != nil {
		t.Fatalf("read setup.sh: %v", err)
	}
	setupText := string(setup)
	for _, want := range []string{
		"cmd_apply",
		"cmd_publish",
		"cmd_unpublish",
		"cmd_firewall",
		"--profile public",
		"runtime-setup.sh",
		"ensure_session_secret",
		"pull pantry",
		"retire_lan_host_helpers",
		"127.0.0.1",
		"${1:-apply}",
		"Home Wi-Fi cannot open https://",
		"does not hairpin NAT",
	} {
		if !strings.Contains(setupText, want) {
			t.Fatalf("setup.sh missing %q", want)
		}
	}
	if strings.Contains(setupText, "write_auth_caddy") || strings.Contains(setupText, "hash-password") {
		t.Fatal("setup.sh must not generate auth.caddy or hash a shared password")
	}

	if strings.Contains(envText, "PANTRY_LAN_FIREWALL") || strings.Contains(envText, "PANTRY_LAN_IPV4") || strings.Contains(envText, "PANTRY_SPLIT_DNS") || strings.Contains(envText, "LAN_PEOPLE") || strings.Contains(envText, "PUBLIC_LISTEN_ADDR") || strings.Contains(envText, "LAN_LISTEN_ADDR") {
		t.Fatal(".env.example must not document the removed LAN listener settings")
	}

	updater, err := os.ReadFile(filepath.Join("..", "..", "deploy", "systemd", "pantry-update.sh"))
	if err != nil {
		t.Fatalf("read pantry-update.sh: %v", err)
	}
	updaterText := string(updater)
	if !strings.Contains(updaterText, "--profile public") || !strings.Contains(updaterText, "PUBLIC_HOST") {
		t.Fatal("automatic updates must include the public profile when PUBLIC_HOST is set")
	}
	if strings.Contains(updaterText, "auth.caddy") {
		t.Fatal("automatic updates must not depend on auth.caddy")
	}
	if !strings.Contains(updaterText, "runtime-setup.sh") || !strings.Contains(updaterText, "ensure_session_secret") {
		t.Fatal("automatic updates must keep the session secret via runtime-setup.sh")
	}
	if !strings.Contains(updaterText, "refresh_host_scripts_from_image") || !strings.Contains(updaterText, "/usr/share/stockbot/host-bundle") {
		t.Fatal("automatic updates must refresh host scripts from the image bundle")
	}

	ignore, err := os.ReadFile(filepath.Join("..", "..", ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(ignore), "deploy/auth.caddy") {
		t.Fatal(".gitignore must ignore deploy/auth.caddy so a generated hash is not committed")
	}

	readme, err := os.ReadFile(filepath.Join("..", "..", "deploy", "README.md"))
	if err != nil {
		t.Fatalf("read deploy README: %v", err)
	}
	readmeText := string(readme)
	for _, want := range []string{
		"https://stockbot.rhionin.com",
		"127.0.0.1",
		"Cloudflare Tunnel",
	} {
		if !strings.Contains(readmeText, want) {
			t.Fatalf("deploy README missing %q", want)
		}
	}
	if strings.Contains(readmeText, "http://pantry.local:8080") || strings.Contains(readmeText, "pantry-lan-only") || strings.Contains(readmeText, "pantry-mdns") {
		t.Fatal("deploy README must not tell operators to use the removed LAN address or units")
	}
	if !strings.Contains(text, "Do not publish") || !strings.Contains(text, "port 8080") {
		t.Fatal("Caddyfile must keep the warning that port 8080 stays off the Pi's other interfaces")
	}
	if strings.Contains(text, "trusted_proxies") {
		t.Fatal("certificate Caddyfile must not trust forwarded headers; that is tunnel mode only")
	}

	tunnelCaddy, err := os.ReadFile(filepath.Join("..", "..", "deploy", "Caddyfile.tunnel"))
	if err != nil {
		t.Fatalf("read Caddyfile.tunnel: %v", err)
	}
	tunnelText := string(tunnelCaddy)
	acmeRoutes := routeBody(text)
	tunnelRoutes := routeBody(tunnelText)
	if acmeRoutes == "" || acmeRoutes != tunnelRoutes {
		t.Fatal("certificate and tunnel Caddyfiles must share the same route block")
	}
	// Pantry sends these on /sw.js. A second copy from Caddy is joined with
	// the app's, and the combined Service-Worker-Allowed value is not a scope.
	// Comment lines may name the headers; a directive must not set them.
	for _, line := range strings.Split(acmeRoutes, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, forbidden := range []string{"Service-Worker-Allowed", "header Cache-Control"} {
			if strings.Contains(line, forbidden) {
				t.Fatalf("Caddy route block sets %q; Pantry sends Cache-Control and Service-Worker-Allowed once", forbidden)
			}
		}
	}
	for _, want := range []string{
		"http://{$PUBLIC_HOST}",
		"admin off",
		"trusted_proxies static 10.77.77.2/32",
		"client_ip_headers Cf-Connecting-Ip",
		"reverse_proxy pantry:8080",
		"X-Pantry-Client-IP {client_ip}",
		"header_up -X-Pantry-Entry",
		"header_up X-Forwarded-Proto https",
		"(security_headers)",
	} {
		if !strings.Contains(tunnelText, want) {
			t.Fatalf("Caddyfile.tunnel missing %q", want)
		}
	}
	if strings.Contains(tunnelText, "pantry:8081") || strings.Contains(tunnelText, "header_up X-Pantry-Entry ") || strings.Contains(tunnelText, "X-Pantry-Entry public") {
		t.Fatal("Caddyfile.tunnel must proxy to pantry:8080 and must not stamp an entry header")
	}
	if strings.Count(tunnelText, "reverse_proxy") != strings.Count(tunnelText, "reverse_proxy pantry:8080") {
		t.Fatal("every Caddyfile.tunnel reverse_proxy must target pantry:8080")
	}
	if strings.Contains(tunnelText, "{$ACME_EMAIL}") || strings.Contains(tunnelText, "email ") {
		t.Fatal("tunnel Caddyfile must not request a certificate")
	}
	if !strings.Contains(tunnelText, "max_size 1MB") {
		t.Fatal("Caddyfile.tunnel must cap request bodies at 1MB")
	}

	tunnelComposeRaw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker-compose.tunnel.yml"))
	if err != nil {
		t.Fatalf("read tunnel compose: %v", err)
	}
	var tunnelCompose struct {
		Services map[string]struct {
			Image         string            `yaml:"image"`
			Profiles      []string          `yaml:"profiles"`
			Ports         []string          `yaml:"ports"`
			Command       []string          `yaml:"command"`
			Environment   map[string]string `yaml:"environment"`
			ContainerName string            `yaml:"container_name"`
			Networks      map[string]struct {
				IPv4Address string   `yaml:"ipv4_address"`
				Aliases     []string `yaml:"aliases"`
			} `yaml:"networks"`
		} `yaml:"services"`
		Networks map[string]struct {
			IPAM struct {
				Config []struct {
					Subnet  string `yaml:"subnet"`
					IPRange string `yaml:"ip_range"`
					Gateway string `yaml:"gateway"`
				} `yaml:"config"`
			} `yaml:"ipam"`
		} `yaml:"networks"`
	}
	if err := yaml.Unmarshal(tunnelComposeRaw, &tunnelCompose); err != nil {
		t.Fatalf("parse tunnel compose: %v", err)
	}
	caddyTunnel, ok := tunnelCompose.Services["caddy-tunnel"]
	if !ok {
		t.Fatal("caddy-tunnel service missing")
	}
	if caddyTunnel.Image != caddy.Image {
		t.Fatalf("caddy-tunnel image = %q, certificate compose pins %q", caddyTunnel.Image, caddy.Image)
	}
	if len(caddyTunnel.Profiles) != 1 || caddyTunnel.Profiles[0] != "tunnel" {
		t.Fatalf("caddy-tunnel profiles = %v", caddyTunnel.Profiles)
	}
	if len(caddyTunnel.Ports) != 0 {
		t.Fatalf("caddy-tunnel must not publish host ports, got %v", caddyTunnel.Ports)
	}
	if caddyTunnel.ContainerName != "pantry-caddy" {
		t.Fatalf("caddy-tunnel container = %q", caddyTunnel.ContainerName)
	}
	cloudflared, ok := tunnelCompose.Services["cloudflared"]
	if !ok {
		t.Fatal("cloudflared service missing")
	}
	if cloudflared.Image != "cloudflare/cloudflared:2026.10.3" {
		t.Fatalf("cloudflared image = %q", cloudflared.Image)
	}
	if len(cloudflared.Profiles) != 1 || cloudflared.Profiles[0] != "tunnel" {
		t.Fatalf("cloudflared profiles = %v", cloudflared.Profiles)
	}
	if len(cloudflared.Ports) != 0 {
		t.Fatalf("cloudflared must not publish host ports, got %v", cloudflared.Ports)
	}
	if strings.Join(cloudflared.Command, " ") != "tunnel run" {
		t.Fatalf("cloudflared command = %#v, want tunnel run", cloudflared.Command)
	}
	if !strings.Contains(cloudflared.Environment["TUNNEL_TOKEN"], "CLOUDFLARE_TUNNEL_TOKEN") {
		t.Fatalf("cloudflared token env = %q", cloudflared.Environment["TUNNEL_TOKEN"])
	}
	if cloudflared.ContainerName != "pantry-cloudflared" {
		t.Fatalf("cloudflared container = %q", cloudflared.ContainerName)
	}
	caddyNet, ok := caddyTunnel.Networks["cf-tunnel"]
	if !ok {
		t.Fatal("caddy-tunnel must join cf-tunnel")
	}
	if caddyNet.IPv4Address != "10.77.77.3" {
		t.Fatalf("caddy-tunnel address = %q, want 10.77.77.3", caddyNet.IPv4Address)
	}
	if !hasExactPort(caddyNet.Aliases, "caddy") {
		t.Fatalf("caddy-tunnel aliases = %v, want caddy", caddyNet.Aliases)
	}
	cfNet, ok := cloudflared.Networks["cf-tunnel"]
	if !ok || cfNet.IPv4Address != "10.77.77.2" {
		t.Fatalf("cloudflared cf-tunnel = %+v, want 10.77.77.2", cfNet)
	}
	for name, svc := range tunnelCompose.Services {
		net, joined := svc.Networks["cf-tunnel"]
		if !joined {
			continue
		}
		if net.IPv4Address == "10.77.77.2" && name != "cloudflared" {
			t.Fatalf("%s must not use 10.77.77.2", name)
		}
		if net.IPv4Address == "" {
			t.Fatalf("%s joins cf-tunnel without a fixed address", name)
		}
	}
	ipam := tunnelCompose.Networks["cf-tunnel"].IPAM.Config
	if len(ipam) != 1 || ipam[0].Subnet != "10.77.77.0/29" || ipam[0].IPRange != "10.77.77.4/30" || ipam[0].Gateway != "10.77.77.1" {
		t.Fatalf("cf-tunnel ipam = %+v", ipam)
	}
	tunnelYAML := string(tunnelComposeRaw)
	if !strings.Contains(tunnelYAML, "http://caddy:80") {
		t.Fatal("tunnel compose must document the public-hostname service URL")
	}
	if strings.Contains(tunnelYAML, "Caddyfile.legacy") {
		t.Fatal("tunnel compose must not mount removed legacy Caddy snippets")
	}
	if hasExactPort(caddy.Volumes, "./Caddyfile.legacy:/etc/caddy/Caddyfile.legacy:ro") {
		t.Fatalf("caddy volumes must not mount removed legacy snippets: %v", caddy.Volumes)
	}
	if _, ok := tunnelCompose.Services["caddy"]; ok {
		t.Fatal("tunnel compose must not redefine the certificate caddy service")
	}

	for _, want := range []string{
		"--tunnel",
		"CLOUDFLARE_TUNNEL_TOKEN",
		"docker-compose.tunnel.yml",
		"http://caddy:80",
		"publish_mode_from_env",
		"drop_stale_cloudflared",
		"runtime-setup.sh",
	} {
		if !strings.Contains(setupText, want) {
			t.Fatalf("setup.sh missing %q", want)
		}
	}
	if !strings.Contains(envText, "\nPUBLISH_MODE=\n") || !strings.Contains(envText, "\nCLOUDFLARE_TUNNEL_TOKEN=\n") {
		t.Fatal(".env.example must leave PUBLISH_MODE and CLOUDFLARE_TUNNEL_TOKEN empty")
	}
	if !strings.Contains(updaterText, "docker-compose.tunnel.yml") || !strings.Contains(updaterText, "--profile tunnel") {
		t.Fatal("automatic updates must select the tunnel profile when the token is set")
	}
	if !strings.Contains(updaterText, "drop_stale_cloudflared") {
		t.Fatal("automatic updates must drop a cloudflared container left Created before tunnel up")
	}
	for _, want := range []string{
		"CLOUDFLARE_TUNNEL_TOKEN",
		"sudo ./setup.sh publish --tunnel",
		"http://caddy:80",
		"DNSSEC",
		"Zero Trust",
		"Free",
		"nameservers",
		"#### Rollback",
		"cellular",
		"Remove the Gryphon forwards",
		"Dynu",
		"pantry-pi",
		"9e158cce-4d31-4881-82f3-d905af6164e7",
		"stockbot.rhionin.com",
		"pantry.rhionin.com",
		"LEGACY_PUBLIC_HOST",
	} {
		if !strings.Contains(readmeText, want) {
			t.Fatalf("deploy README missing %q", want)
		}
	}
	if strings.Contains(readmeText, "Create a tunnel") {
		t.Fatal("deploy README must use the existing pantry-pi tunnel")
	}
	if strings.Contains(setupText, "Create a tunnel") {
		t.Fatal("setup.sh must not tell the operator to create a tunnel")
	}
	if !strings.Contains(setupText, "pantry-pi") {
		t.Fatal("setup.sh must name the existing tunnel pantry-pi")
	}
}

// routeBody returns the shared site routes. The certificate and tunnel
// files differ above this comment and must match from it to the end.
func routeBody(text string) string {
	const marker = "\n\t# handle is first-match."
	i := strings.Index(text, marker)
	if i < 0 {
		return ""
	}
	return text[i:]
}

// pathMatchers returns every Caddy `path` matcher, in source order.
// Comment lines are skipped so a note about a path is not treated as a route.
func pathMatchers(text string) []string {
	var got []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		const marker = " path "
		if i := strings.Index(line, marker); i >= 0 {
			got = append(got, strings.TrimSpace(line[i+1:]))
			continue
		}
		if strings.HasPrefix(line, "path ") {
			got = append(got, line)
		}
	}
	return got
}

func TestPinnedCaddyImage(t *testing.T) {
	for _, image := range []string{"caddy:2.11.4-alpine", "caddy:2.11.7-alpine"} {
		if !pinnedCaddyImage(image) {
			t.Fatalf("%q should be a pinned caddy image", image)
		}
	}
	for _, image := range []string{
		"",
		"caddy",
		"caddy:",
		"caddy:latest",
		"caddy:latest-alpine",
		"library/caddy:2.11.4-alpine",
		"caddy:2.11.4 alpine",
	} {
		if pinnedCaddyImage(image) {
			t.Fatalf("%q should not be a pinned official caddy image", image)
		}
	}
}

// pinnedCaddyImage reports whether image is the official caddy image with an
// explicit tag. latest is not a pin: both compose files could agree on it
// and still float.
func pinnedCaddyImage(image string) bool {
	tag, ok := strings.CutPrefix(image, "caddy:")
	if !ok || tag == "" || tag == "latest" || strings.HasPrefix(tag, "latest-") {
		return false
	}
	return !strings.ContainsAny(tag, " \t")
}

func hasExactPort(ports []string, want string) bool {
	for _, port := range ports {
		if port == want {
			return true
		}
	}
	return false
}

func hasContainerPort(ports []string, containerPort string) bool {
	suffix := ":" + containerPort
	for _, port := range ports {
		if port == containerPort || strings.HasSuffix(port, suffix) {
			return true
		}
	}
	return false
}

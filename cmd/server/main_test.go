package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"gopkg.in/yaml.v3"
)

// TestComposeDefaultsMatchMainGo verifies that the defaults in deploy/docker-compose.yml
// match the defaults used by main.go, preventing cross-artifact drift.
func TestComposeDefaultsMatchMainGo(t *testing.T) {
	// Read the compose file
	composeFile := filepath.Join("..", "..", "deploy", "docker-compose.yml")
	composeData, err := os.ReadFile(composeFile)
	if err != nil {
		t.Fatalf("Failed to read compose file: %v", err)
	}

	// Parse the compose file
	var compose struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}

	if err := yaml.Unmarshal(composeData, &compose); err != nil {
		t.Fatalf("Failed to parse compose file: %v", err)
	}

	pantryService, exists := compose.Services["pantry"]
	if !exists {
		t.Fatal("pantry service not found in compose file")
	}

	// Test cases mapping compose defaults to Go constants/defaults
	testCases := []struct {
		name           string
		envVar         string
		composeDefault string
		goDefault      interface{}
	}{
		{
			name:           "PRODUCT_CACHE_TTL",
			envVar:         "PRODUCT_CACHE_TTL",
			composeDefault: extractDefault(t, pantryService.Environment["PRODUCT_CACHE_TTL"]),
			goDefault:      defaultProductCacheTTL,
		},
		{
			name:           "PRODUCT_MISS_TTL",
			envVar:         "PRODUCT_MISS_TTL",
			composeDefault: extractDefault(t, pantryService.Environment["PRODUCT_MISS_TTL"]),
			goDefault:      defaultMissTTL,
		},
		{
			name:           "DISABLE_EXTERNAL_PRODUCT_LOOKUP",
			envVar:         "DISABLE_EXTERNAL_PRODUCT_LOOKUP",
			composeDefault: extractDefault(t, pantryService.Environment["DISABLE_EXTERNAL_PRODUCT_LOOKUP"]),
			goDefault:      "false", // main.go checks != "true", so default is effectively false
		},
		{
			name:           "STOCK_IN_CONTROL_BARCODE",
			envVar:         "STOCK_IN_CONTROL_BARCODE",
			composeDefault: extractDefault(t, pantryService.Environment["STOCK_IN_CONTROL_BARCODE"]),
			goDefault:      "STOCK_IN", // from loadScanListenerConfig
		},
		{
			name:           "STOCK_OUT_CONTROL_BARCODE",
			envVar:         "STOCK_OUT_CONTROL_BARCODE",
			composeDefault: extractDefault(t, pantryService.Environment["STOCK_OUT_CONTROL_BARCODE"]),
			goDefault:      "STOCK_OUT", // from loadScanListenerConfig
		},
		{
			name:           "APP_BASE_URL",
			envVar:         "APP_BASE_URL",
			composeDefault: extractDefault(t, pantryService.Environment["APP_BASE_URL"]),
			goDefault:      "",
		},
		{
			name:           "APP_NAME",
			envVar:         "APP_NAME",
			composeDefault: extractDefault(t, pantryService.Environment["APP_NAME"]),
			goDefault:      app.DefaultAppName,
		},
		{
			name:           "SESSION_COOKIE_NAME",
			envVar:         "SESSION_COOKIE_NAME",
			composeDefault: extractDefault(t, pantryService.Environment["SESSION_COOKIE_NAME"]),
			goDefault:      app.DefaultSessionCookieName,
		},
		{
			name:           "COOKIE_DOMAIN",
			envVar:         "COOKIE_DOMAIN",
			composeDefault: extractDefault(t, pantryService.Environment["COOKIE_DOMAIN"]),
			goDefault:      "",
		},
		{
			name:           "EMAIL_FROM",
			envVar:         "EMAIL_FROM",
			composeDefault: extractDefault(t, pantryService.Environment["EMAIL_FROM"]),
			goDefault:      "",
		},
		{
			name:           "LAN_PEOPLE",
			envVar:         "LAN_PEOPLE",
			composeDefault: extractDefault(t, pantryService.Environment["LAN_PEOPLE"]),
			goDefault:      app.DefaultLANPeople,
		},
		{
			name:           "EXPO_MIN_VERSION",
			envVar:         "EXPO_MIN_VERSION",
			composeDefault: extractDefault(t, pantryService.Environment["EXPO_MIN_VERSION"]),
			goDefault:      app.DefaultExpoMinVersion,
		},
		{
			name:           "PUBLIC_LISTEN_ADDR",
			envVar:         "PUBLIC_LISTEN_ADDR",
			composeDefault: extractDefault(t, pantryService.Environment["PUBLIC_LISTEN_ADDR"]),
			goDefault:      app.DefaultPublicListenAddr,
		},
		{
			name:           "LAN_LISTEN_ADDR",
			envVar:         "LAN_LISTEN_ADDR",
			composeDefault: extractDefault(t, pantryService.Environment["LAN_LISTEN_ADDR"]),
			goDefault:      app.DefaultLANListenAddr,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			switch expected := tc.goDefault.(type) {
			case time.Duration:
				// Compare as durations: the compose literal ("720h") and the Go
				// constant's String() form ("720h0m0s") are the same duration
				// but different strings, so parse before comparing.
				composeDuration, err := time.ParseDuration(tc.composeDefault)
				if err != nil {
					t.Errorf("Compose default %q for %s is not a valid duration: %v",
						tc.composeDefault, tc.envVar, err)
				} else if composeDuration != expected {
					t.Errorf("Compose default %q for %s doesn't match Go default %q",
						tc.composeDefault, tc.envVar, expected)
				}
			case string:
				if tc.composeDefault != expected {
					t.Errorf("Compose default %q for %s doesn't match Go default %q",
						tc.composeDefault, tc.envVar, expected)
				}
			default:
				t.Errorf("Unsupported type %T for %s", expected, tc.envVar)
			}
		})
	}

	// Verify the duration parsing matches by actually parsing the compose defaults
	t.Run("DurationParsing", func(t *testing.T) {
		cacheTTLStr := extractDefault(t, pantryService.Environment["PRODUCT_CACHE_TTL"])
		cacheTTL, err := time.ParseDuration(cacheTTLStr)
		if err != nil {
			t.Errorf("Compose PRODUCT_CACHE_TTL %q is not a valid duration: %v", cacheTTLStr, err)
		} else if cacheTTL != defaultProductCacheTTL {
			t.Errorf("Parsed compose PRODUCT_CACHE_TTL %s != Go default %s", cacheTTL, defaultProductCacheTTL)
		}

		missTTLStr := extractDefault(t, pantryService.Environment["PRODUCT_MISS_TTL"])
		missTTL, err := time.ParseDuration(missTTLStr)
		if err != nil {
			t.Errorf("Compose PRODUCT_MISS_TTL %q is not a valid duration: %v", missTTLStr, err)
		} else if missTTL != defaultMissTTL {
			t.Errorf("Parsed compose PRODUCT_MISS_TTL %s != Go default %s", missTTL, defaultMissTTL)
		}
	})
}

// extractDefault extracts the default value from a ${VAR:-default} environment variable string
func extractDefault(t *testing.T, envValue string) string {
	t.Helper()

	// Expected format: ${VAR:-default}
	if !strings.HasPrefix(envValue, "${") || !strings.HasSuffix(envValue, "}") {
		t.Fatalf("Environment variable %q is not in expected ${VAR:-default} format", envValue)
	}

	// Remove ${ and }
	inner := envValue[2 : len(envValue)-1]

	// Split on :-
	parts := strings.SplitN(inner, ":-", 2)
	if len(parts) != 2 {
		t.Fatalf("Environment variable %q does not contain :- separator", envValue)
	}

	// ${OTHER:+text} is empty when OTHER is unset. That is the value Compose
	// passes when the deployment .env sets neither name, which is the Go default.
	def := parts[1]
	if strings.HasPrefix(def, "${") && strings.HasSuffix(def, "}") && strings.Contains(def, ":+") {
		return ""
	}
	return def
}

// expandCompose applies Compose's ${VAR:-default} and ${VAR:+alt} rules.
// An empty or missing variable is unset. Nested substitutions are expanded.
func expandCompose(t *testing.T, expr string, env map[string]string) string {
	t.Helper()
	got, err := expandComposeExpr(expr, env)
	if err != nil {
		t.Fatalf("expand %q: %v", expr, err)
	}
	return got
}

func expandComposeExpr(expr string, env map[string]string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(expr); {
		if expr[i] == '$' && i+1 < len(expr) && expr[i+1] == '{' {
			end := composeBraceEnd(expr, i+1)
			if end < 0 {
				return "", fmt.Errorf("unclosed substitution")
			}
			piece, err := expandComposeItem(expr[i+2:end], env)
			if err != nil {
				return "", err
			}
			b.WriteString(piece)
			i = end + 1
			continue
		}
		b.WriteByte(expr[i])
		i++
	}
	return b.String(), nil
}

func expandComposeItem(inner string, env map[string]string) (string, error) {
	name, op, rest := splitComposeOp(inner)
	val, ok := env[name]
	if op == "" {
		return val, nil
	}
	alt, err := expandComposeExpr(rest, env)
	if err != nil {
		return "", err
	}
	set := ok && val != ""
	switch op {
	case ":-":
		if !set {
			return alt, nil
		}
		return val, nil
	case ":+":
		if set {
			return alt, nil
		}
		return "", nil
	default:
		return "", fmt.Errorf("unknown operator %q", op)
	}
}

func splitComposeOp(inner string) (name, op, rest string) {
	depth := 0
	for i := 0; i < len(inner); i++ {
		switch {
		case inner[i] == '$' && i+1 < len(inner) && inner[i+1] == '{':
			depth++
			i++
		case inner[i] == '{':
			depth++
		case inner[i] == '}' && depth > 0:
			depth--
		case depth == 0 && i+1 < len(inner) && inner[i] == ':' && (inner[i+1] == '-' || inner[i+1] == '+'):
			return inner[:i], inner[i : i+2], inner[i+2:]
		}
	}
	return inner, "", ""
}

func composeBraceEnd(expr string, open int) int {
	depth := 0
	for i := open; i < len(expr); i++ {
		switch expr[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func TestUnauthenticatedListenWarning(t *testing.T) {
	if got := unauthenticatedListenWarning(":8080"); got == "" {
		t.Fatal("empty host should warn")
	}
	if got := unauthenticatedListenWarning("0.0.0.0:8080"); got == "" {
		t.Fatal("wildcard should warn")
	}
	if got := unauthenticatedListenWarning("192.168.1.203:8080"); got == "" {
		t.Fatal("LAN address should warn")
	}
	if got := unauthenticatedListenWarning("127.0.0.1:8080"); got != "" {
		t.Fatalf("loopback warned: %q", got)
	}
	if got := unauthenticatedListenWarning("localhost:8080"); got != "" {
		t.Fatalf("localhost warned: %q", got)
	}
	if got := unauthenticatedListenWarning("localhost"); got != "" {
		t.Fatalf("localhost without a port warned: %q", got)
	}
	if got := unauthenticatedListenWarning("0.0.0.0"); got == "" {
		t.Fatal("wildcard without a port should warn")
	}
}

package hub

import (
	"context"
	"crypto/ed25519"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
)

const (
	defaultStatePath   = "/var/lib/stockbot-hub/state.json"
	defaultBufferPath  = "/var/lib/stockbot-hub/buffer.sqlite"
	defaultReleaseRoot = "/opt/stockbot-hub"
)

// Main runs the stockbot hub command. args are the arguments after the
// program name. "hub" as the first argument is accepted so `stockbot hub`
// and the hub binary take the same flags.
func Main(args []string) int {
	return mainWith(args, os.Stdin, os.Stdout, os.Stderr)
}

func mainWith(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "hub" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("stockbot hub", flag.ContinueOnError)
	fs.SetOutput(stderr)
	server := fs.String("server", envOr("APP_BASE_URL", ""), "Stockbot server URL")
	statePath := fs.String("state", envOr("HUB_STATE", defaultStatePath), "credentials file (mode 0600)")
	bufferPath := fs.String("buffer", envOr("HUB_BUFFER", defaultBufferPath), "offline scan buffer")
	name := fs.String("name", envOr("HUB_NAME", defaultName), "name shown for this hub")
	code := fs.String("code", "", "pairing code typed on a keyboard")
	version := fs.String("version", envOr("HUB_VERSION", "s12"), "hub version")
	pub := fs.String("pubkey", envOr("HUB_RELEASE_PUBKEY", ""), "Ed25519 release public key, or a file containing it")
	releases := fs.String("releases", envOr("HUB_RELEASE_ROOT", defaultReleaseRoot), "directory for signed updates")
	selfCheck := fs.Bool("self-check", false, "exit after a health check")
	watchdog := fs.Bool("watchdog", false, "roll back a staged update that missed its deadline")
	pairOnly := fs.Bool("pair-only", false, "pair and exit")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 2
		}
		return 2
	}
	if *selfCheck {
		fmt.Fprintln(stdout, "ok")
		return 0
	}
	var key ed25519.PublicKey
	if strings.TrimSpace(*pub) != "" {
		loaded, err := loadKey(*pub)
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 2
		}
		key = loaded
	}
	if *code != "" {
		if _, well, ok := auth.NormalizePairingCode(*code); well && !ok {
			fmt.Fprintln(stderr, ErrBadPairingCheck.Error())
			return 2
		}
	}
	h, err := Open(Options{
		ServerURL:   *server,
		StatePath:   *statePath,
		BufferPath:  *bufferPath,
		ReleaseRoot: *releases,
		Name:        *name,
		Version:     FormatVersion(*version),
		PublicKey:   key,
		Stdin:       stdin,
		Now:         time.Now,
	})
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	defer h.Close()
	ctx := context.Background()
	if *watchdog {
		rolled, err := h.EvaluateWatchdog(time.Now())
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		if rolled {
			fmt.Fprintln(stdout, "rolled back")
		}
		return 0
	}
	if *code != "" {
		if err := h.Pair(ctx, *code); err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		if *pairOnly {
			fmt.Fprintln(stdout, "paired")
			return 0
		}
	}
	if err := h.Run(ctx); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	return 0
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func loadKey(raw string) (ed25519.PublicKey, error) {
	if b, err := os.ReadFile(raw); err == nil {
		return ParsePublicKey(string(b))
	}
	return ParsePublicKey(raw)
}

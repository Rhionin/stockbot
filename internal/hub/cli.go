package hub

import (
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
		OnRestart:   restartInto,
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
		if errors.Is(err, ErrRestart) {
			return 0
		}
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	return 0
}

// restartInto replaces this process with the staged binary. HUB_VERSION is
// the release directory name so the new process's version matches the pending
// update and a healthy heartbeat can clear it.
func restartInto(binary string) error {
	version := filepath.Base(filepath.Dir(binary))
	if version == "" || version == "." || version == string(filepath.Separator) {
		return errors.New("the staged hub has no version")
	}
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "HUB_VERSION=") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "HUB_VERSION="+version)
	argv := append([]string(nil), os.Args...)
	argv = replaceFlag(argv, "version", version)
	if err := syscall.Exec(binary, argv, env); err != nil {
		return errors.New("the staged hub did not start")
	}
	return nil
}

func replaceFlag(argv []string, name, value string) []string {
	dash := "--" + name
	out := make([]string, 0, len(argv)+2)
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == dash {
			out = append(out, dash, value)
			if i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-") {
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, dash+"=") {
			out = append(out, dash+"="+value)
			continue
		}
		out = append(out, arg)
	}
	return out
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

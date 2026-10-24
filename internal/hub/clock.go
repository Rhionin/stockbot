package hub

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// ageMs is how long a scan has waited, measured on a monotonic clock so a
// wall-clock step (the Pi has no RTC until chrony syncs) does not move it.
func ageMs(nowMono, capturedMono int64) int64 {
	age := nowMono - capturedMono
	if age < 0 {
		return 0
	}
	return age
}

// restartedAge adds the previous boot's remaining uptime to this boot.
// The monotonic clock starts over when the process starts, so a scan captured
// in an earlier boot keeps the uptime recorded for that boot.
func restartedAge(prevUptime, capturedMono, nowMono int64) int64 {
	waited := prevUptime - capturedMono
	if waited < 0 {
		waited = 0
	}
	return waited + ageMs(nowMono, 0)
}

// ParseChronyTracking reports whether chronyc tracking shows a synced clock.
// A zero reference id or a leap status of "Not synchronised" means the clock
// is not safe to stamp onto scannedAt.
func ParseChronyTracking(output string) bool {
	synced := false
	for _, line := range strings.Split(output, "\n") {
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		field = strings.TrimSpace(field)
		value = strings.TrimSpace(value)
		switch field {
		case "Reference ID":
			id, _, _ := strings.Cut(value, " ")
			if id == "00000000" || id == "" {
				return false
			}
		case "Leap status":
			if strings.EqualFold(value, "Not synchronised") || strings.EqualFold(value, "Not synchronized") {
				return false
			}
			if strings.EqualFold(value, "Normal") {
				synced = true
			}
		}
	}
	return synced
}

// CommandRunner executes a command and returns its combined output.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ChronySynced runs `chronyc tracking` and parses the result.
// A missing chronyc binary reports not synced without wrapping the exec error
// into a failure the hub must stop for: scans can still queue with ageMs.
func ChronySynced(ctx context.Context, run CommandRunner) (bool, error) {
	if run == nil {
		run = defaultCommand
	}
	out, err := run(ctx, "chronyc", "tracking")
	if err != nil {
		return false, err
	}
	return ParseChronyTracking(string(out)), nil
}

func defaultCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Mono is a process-local monotonic millisecond clock. It uses time.Since,
// which follows Go's monotonic clock reading, so wall-clock steps do not
// change durations measured with it.
type Mono struct {
	start time.Time
}

// NewMono starts a monotonic clock at start.
func NewMono(start time.Time) Mono {
	return Mono{start: start}
}

// Millis is the elapsed time since start, in milliseconds, never negative.
func (m Mono) Millis(now time.Time) int64 {
	if m.start.IsZero() {
		return 0
	}
	d := now.Sub(m.start)
	if d < 0 {
		return 0
	}
	return d.Milliseconds()
}

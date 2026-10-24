package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// Account passwords and setup codes use these argon2id parameters.
// 19 MiB is the memory the design locks so a later host can match a hash
// written on the Pi. The semaphore keeps two of those allocations from
// stacking up when several sign-ins arrive together.
const (
	argonMemoryKiB = 19 * 1024
	argonTime      = 2
	argonThreads   = 1
	argonSaltLen   = 16
	argonKeyLen    = 32
	argonSlots     = 2
	argonWait      = 2 * time.Second
)

var argonSem = make(chan struct{}, argonSlots)

// HashPassword returns a PHC argon2id string. The password is checked for
// length before this runs.
func HashPassword(ctx context.Context, password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum, err := argonKey(ctx, []byte(password), salt, argonTime, argonMemoryKiB, argonThreads)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemoryKiB, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	), nil
}

// VerifyPassword reports whether password matches a PHC string.
// Parameters come from the string, so a hash written with another cost
// still verifies. A malformed string does not match.
func VerifyPassword(ctx context.Context, encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, nil
	}
	memory, timeCost, threads, ok := parseArgonParams(parts[3])
	if !ok {
		return false, nil
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false, nil
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, nil
	}
	got, err := argonKey(ctx, []byte(password), salt, timeCost, memory, threads)
	if err != nil {
		return false, err
	}
	if len(got) != len(want) {
		return false, nil
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func parseArgonParams(raw string) (memory, timeCost uint32, threads uint8, ok bool) {
	memory, timeCost, threads = argonMemoryKiB, argonTime, argonThreads
	ok = true
	for _, field := range strings.Split(raw, ",") {
		name, value, found := strings.Cut(field, "=")
		if !found {
			return 0, 0, 0, false
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return 0, 0, 0, false
		}
		switch name {
		case "m":
			memory = uint32(n)
		case "t":
			timeCost = uint32(n)
		case "p":
			if n > 255 {
				return 0, 0, 0, false
			}
			threads = uint8(n)
		default:
			return 0, 0, 0, false
		}
	}
	return memory, timeCost, threads, true
}

func argonKey(ctx context.Context, password, salt []byte, timeCost, memory uint32, threads uint8) ([]byte, error) {
	release, err := acquireArgon(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return argon2.IDKey(password, salt, timeCost, memory, threads, argonKeyLen), nil
}

func acquireArgon(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(argonWait)
	defer timer.Stop()
	select {
	case argonSem <- struct{}{}:
		return func() { <-argonSem }, nil
	case <-timer.C:
		return nil, ErrBusy
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrBusy
		}
		return nil, ctx.Err()
	}
}

package hub

import (
	"math/rand"
	"time"
)

// BackoffCap is the longest ordinary retry wait. A Retry-After value is
// honored even when it is longer, because the server asked for that pause.
const BackoffCap = 30 * time.Second

// Backoff is the wait before retry attempt (0 is the first retry).
// The delay doubles from one second and is capped, then jitter draws it
// into the upper half of that range so hubs do not retry in lockstep.
// retryAfter, when longer than that delay, replaces it.
func Backoff(attempt int, rng func() float64, retryAfter time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	delay := time.Second
	for i := 0; i < attempt; i++ {
		if delay >= BackoffCap {
			delay = BackoffCap
			break
		}
		delay *= 2
		if delay > BackoffCap {
			delay = BackoffCap
		}
	}
	if rng == nil {
		rng = rand.Float64
	}
	unit := rng()
	if unit < 0 {
		unit = 0
	}
	if unit > 1 {
		unit = 1
	}
	// Full jitter stays inside [delay/2, delay] so the backoff still grows.
	wait := time.Duration(float64(delay) * (0.5 + 0.5*unit))
	if retryAfter > wait {
		return retryAfter
	}
	return wait
}

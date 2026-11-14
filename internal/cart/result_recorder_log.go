package cart

import (
	"log"

	"github.com/getsentry/sentry-go"
)

func logRecorderFailure(err error, outcomeCount int) {
	if err == nil {
		return
	}
	log.Printf("cart send results: failed to record %d outcomes: %v", outcomeCount, err)
	if hub := sentry.CurrentHub(); hub != nil {
		hub.CaptureException(err)
	}
}

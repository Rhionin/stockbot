package scan

import (
	"time"

	"github.com/google/uuid"
)

// ProcessingLookupFailed is the message shown when a barcode was accepted but
// the product lookup did not finish. It stays free of internal details so it
// can be shown directly in the scan queue.
const ProcessingLookupFailed = "Couldn't look up that barcode."

// ProcessingNotice is announced as soon as a barcode is accepted, before the
// product lookup returns. It is not a scan_entries row: the lookup is still in
// flight, and the following scan event replaces it.
type ProcessingNotice struct {
	ID        string         `json:"id"`
	UserID    string         `json:"userId"`
	Barcode   string         `json:"barcode"`
	Direction *ScanDirection `json:"direction"`
	ScannedAt time.Time      `json:"scannedAt"`
	// HouseholdID routes the notice. It is not part of the JSON payload.
	HouseholdID string `json:"-"`
}

// ProcessingFailure withdraws a ProcessingNotice when lookup does not finish.
type ProcessingFailure struct {
	ID      string `json:"id"`
	Barcode string `json:"barcode"`
	Message string `json:"message"`
	// HouseholdID routes the failure. It is not part of the JSON payload.
	HouseholdID string `json:"-"`
}

// ProcessingAnnouncer receives the live lookup notices. A nil announcer is a no-op.
type ProcessingAnnouncer interface {
	PublishScanProcessingEvent(notice ProcessingNotice)
	PublishScanProcessingFailedEvent(failure ProcessingFailure)
}

// AnnounceProcessing tells subscribers a barcode was accepted. Call it before lookup.
func AnnounceProcessing(events ProcessingAnnouncer, householdID, userID, barcode string, direction *ScanDirection, at time.Time) ProcessingNotice {
	notice := ProcessingNotice{
		ID:          uuid.NewString(),
		UserID:      userID,
		Barcode:     barcode,
		Direction:   direction,
		ScannedAt:   at,
		HouseholdID: householdID,
	}
	if events != nil {
		events.PublishScanProcessingEvent(notice)
	}
	return notice
}

// AnnounceProcessingFailed withdraws the notice from AnnounceProcessing.
func AnnounceProcessingFailed(events ProcessingAnnouncer, notice ProcessingNotice) {
	if events == nil {
		return
	}
	events.PublishScanProcessingFailedEvent(ProcessingFailure{
		ID:          notice.ID,
		Barcode:     notice.Barcode,
		Message:     ProcessingLookupFailed,
		HouseholdID: notice.HouseholdID,
	})
}

package cart

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLastSendJSONOmitsReasonForSentAndAdded(t *testing.T) {
	at := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC).Format(time.RFC3339)
	for _, status := range []PublicStatus{PublicStatusSent, PublicStatusAdded} {
		raw, err := json.Marshal(LastSend{
			ProviderID:  "kroger",
			Status:      status,
			Quantity:    2,
			AttemptedAt: at,
		})
		require.NoError(t, err)
		assert.NotContains(t, string(raw), `"reason"`)
		var doc map[string]any
		require.NoError(t, json.Unmarshal(raw, &doc))
		assert.Equal(t, "kroger", doc["providerId"])
		assert.Equal(t, string(status), doc["status"])
		assert.Equal(t, float64(2), doc["quantity"])
		assert.Equal(t, at, doc["attemptedAt"])
	}
}

func TestLastSendJSONIncludesReasonWhenSet(t *testing.T) {
	at := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC).Format(time.RFC3339)
	raw, err := json.Marshal(LastSend{
		ProviderID:  "kroger",
		Status:      PublicStatusFailed,
		Reason:      PublicReasonBatchRejected,
		Quantity:    2,
		AttemptedAt: at,
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"providerId": "kroger",
		"status": "failed",
		"reason": "batch_rejected",
		"quantity": 2,
		"attemptedAt": "2026-11-11T14:00:00Z"
	}`, string(raw))
}

func TestSendResultRetentionIsNinetyDays(t *testing.T) {
	assert.Equal(t, 90*24*time.Hour, SendResultRetention)
}

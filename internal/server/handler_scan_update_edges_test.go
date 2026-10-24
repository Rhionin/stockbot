package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
)

func TestScanUpdateRejectsAnUnknownStatusAndDirection(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "a scan update refuses an unknown status and direction",
			setup: func(env testEnv) {
				direction := scan.StockIn
				entry := scan.ScanEntry{
					ID: "scan-bad", UserID: "user-1", Barcode: "111222333444",
					ScannedAt: time.Now(), Direction: &direction, UnitCount: 1, Status: scan.Pending,
				}
				if _, err := scan.NewQueue(env.DB).CreateScanEntry(context.Background(), primaryScope(env.T, env.DB), entry); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-bad",
				body:           `{"status":"nope"}`,
				expectedStatus: http.StatusBadRequest,
				bodyContains:   []string{"invalid status value"},
			},
			afterRequest: exchanges(httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-bad",
				body:           `{"direction":"sideways"}`,
				expectedStatus: http.StatusBadRequest,
				bodyContains:   []string{"invalid direction value"},
			}),
		},
	})
}

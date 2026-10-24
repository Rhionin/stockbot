package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
)

func TestScanGroupHint(t *testing.T) {
	krAt := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	dmAt := krAt.Add(time.Minute)
	milkAt := krAt.Add(2 * time.Minute)

	runHandlerTests(t, []handlerTestCase{
		{
			name: "a matching scan names the group and a miss omits the hint",
			setup: func(env testEnv) {
				mustGroupProduct(env, "kr", "Kroger Cut Green Beans")
				mustGroupProduct(env, "dm", "Del Monte Cut Green Beans")
				mustGroupProduct(env, "milk", "Whole Milk")
				insertGroup(env, "beans", "Cut green beans")
				insertMember(env, "beans", "kr")
				insertHintScan(env, "scan-kr", "kr", "111", krAt, 1)
				insertHintScan(env, "scan-dm", "dm", "222", dmAt, 1)
				insertHintScan(env, "scan-milk", "milk", "333", milkAt, 1)
			},
			httpExchange: httpExchange{
				method:         http.MethodGet,
				path:           "/api/scans",
				query:          map[string]string{"userId": "user-1", "status": "pending"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].barcode", value: "111"},
					{path: "$[0].groupHint", absent: true},
					{path: "$[1].barcode", value: "222"},
					{path: "$[1].groupHint.groupId", value: "beans"},
					{path: "$[1].groupHint.name", value: "Cut green beans"},
					{path: "$[2].barcode", value: "333"},
					{path: "$[2].groupHint", absent: true},
				},
			},
		},
		{
			name: "not now records one suggestion and a second scan does not ask again",
			setup: func(env testEnv) {
				mustGroupProduct(env, "dm", "Del Monte Cut Green Beans")
				insertGroup(env, "beans", "Cut green beans")
				insertHintScan(env, "scan-dm", "dm", "222", dmAt, 1)
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/group-suggestions/from-scan",
				body:           `{"productId":"dm"}`,
				expectedStatus: http.StatusNoContent,
			},
			afterRequest: func(env testEnv) {
				insertHintScan(env, "scan-dm-2", "dm", "222b", milkAt, 1)
				exchanges(
					httpExchange{
						method:         http.MethodPost,
						path:           "/api/group-suggestions/from-scan",
						body:           `{"productId":"dm"}`,
						expectedStatus: http.StatusNoContent,
					},
					httpExchange{
						method:         http.MethodGet,
						path:           "/api/group-suggestions",
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$[0].kind", value: "from_scan"},
							{path: "$[0].title", value: "Cut green beans"},
							{path: "$[0].existingGroupId", value: "beans"},
							{path: "$[0].members[0].productId", value: "dm"},
							{path: "$[1]", absent: true},
						},
					},
					httpExchange{
						method:         http.MethodGet,
						path:           "/api/scans",
						query:          map[string]string{"userId": "user-1", "status": "pending"},
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$[0].groupHint", absent: true},
							{path: "$[1].groupHint", absent: true},
						},
					},
				)(env)
			},
		},
		{
			name: "a multipack scan stocks one unit per can",
			setup: func(env testEnv) {
				seedShelfItem(env, "pack", "Seltzer 6 x 12 fl oz", "item-pack")
				mustExec(env, `UPDATE products SET pack_count = 6 WHERE id = 'pack'`)
				insertHintScan(env, "scan-pack", "pack", "444", dmAt, 2)
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/scans/scan-pack/commit",
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(httpExchange{
				method:         http.MethodGet,
				path:           "/api/inventory",
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$[0].instanceCount", value: float64(12)}},
			}),
		},
	})
}

func insertHintScan(env testEnv, id, productID, barcode string, at time.Time, units int) {
	env.T.Helper()
	direction := scan.StockIn
	queue := scan.NewQueue(env.DB)
	if _, err := queue.CreateScanEntry(env.T.Context(), primaryScope(env.T, env.DB), scan.ScanEntry{
		ID:        id,
		UserID:    "user-1",
		Barcode:   barcode,
		ScannedAt: at,
		Direction: &direction,
		UnitCount: units,
		ProductID: &productID,
		Status:    scan.Pending,
	}); err != nil {
		env.T.Fatal(err)
	}
}

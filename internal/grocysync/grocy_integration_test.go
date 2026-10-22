package grocysync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "time/tzdata"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/syncsnap"
)

// TestGrocyIntegration talks to a real Grocy. It does not run in CI.
// GROCY_INTEGRATION=1 GROCY_URL=... GROCY_API_KEY=... GROCY_DB=... GROCY_CONTAINER=...
func TestGrocyIntegration(t *testing.T) {
	if os.Getenv("GROCY_INTEGRATION") != "1" {
		t.Skip("set GROCY_INTEGRATION=1 to run against a real Grocy")
	}
	if os.Getenv("GROCY_INTEGRATION_PART") == "backfill" {
		runIntegrationBackfill(t)
		return
	}
	runIntegration(t)
}

func runIntegration(t *testing.T) {
	base := strings.TrimSpace(os.Getenv("GROCY_URL"))
	key := strings.TrimSpace(os.Getenv("GROCY_API_KEY"))
	dbPath := strings.TrimSpace(os.Getenv("GROCY_DB"))
	container := strings.TrimSpace(os.Getenv("GROCY_CONTAINER"))
	if base == "" || key == "" || dbPath == "" || container == "" {
		t.Fatal("GROCY_URL, GROCY_API_KEY, GROCY_DB, and GROCY_CONTAINER are required")
	}
	api, err := NewAPI(base, key)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ver, err := api.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ver != "4.7.1" {
		t.Fatalf("Grocy version = %q", ver)
	}
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureStrayShoppingRow(ctx, api); err != nil {
		t.Fatal(err)
	}

	snap := liveSnapshot("shelf")
	token := "pantry-sync-token-live"
	pantry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/sync/snapshot":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(snap)
		case "/api/events":
			w.Header().Set("Content-Type", "text/event-stream")
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer pantry.Close()

	mapPath := filepath.Join(t.TempDir(), "map.db")
	var logs strings.Builder
	log.SetOutput(io.MultiWriter(os.Stderr, &logs))
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(runCtx, Config{
			PantryURL:   pantry.URL,
			PantryToken: token,
			GrocyURL:    base,
			GrocyKey:    key,
			MapPath:     mapPath,
			Interval:    time.Hour,
			Loc:         loc,
		})
	}()
	deadline := time.Now().Add(90 * time.Second)
	for {
		amount, ok := stockAmountIfPresent(api, "Baking Powder", instancePrefix+"inst-bp")
		if strings.Contains(logs.String(), "reconciled") && ok && amount == 1 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("first reconcile did not finish:\n%s", logs.String())
		}
		time.Sleep(300 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "Grocy 4.7.1") {
		t.Fatalf("startup did not log the version:\n%s", logs.String())
	}

	m, err := OpenMap(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	rec := &Reconciler{Pantry: &fixedSource{snap: snap}, Grocy: api, Map: m, Loc: loc}
	assertLiveCatalog(t, api)
	note := instancePrefix + "inst-bp"
	productID := liveProductID(t, api, "Baking Powder")
	before := openPurchaseIDs(t, api, productID, note)
	if len(before) != 1 {
		t.Fatalf("purchases after first reconcile = %v", before)
	}
	entry := liveEntry(t, api, productID, note)
	if entry.Purchased[:10] != "2024-11-02" || float64(entry.Amount) != 1 {
		t.Fatalf("entry = %#v", entry)
	}

	if err := rec.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got := openPurchaseIDs(t, api, productID, note); len(got) != 1 || got[0] != before[0] {
		t.Fatalf("second reconcile purchases = %v, want %v", got, before)
	}
	if liveStockAmount(t, api, "Baking Powder", note) != 1 {
		t.Fatal("second reconcile changed the amount")
	}

	link, ok, err := m.Get(linkInstance, "inst-bp")
	if err != nil || !ok || link.GrocyRef == "" {
		t.Fatalf("link = %#v %v", link, err)
	}
	savedRef := link.GrocyRef
	link.GrocyRef = ""
	if err := m.Put(link); err != nil {
		t.Fatal(err)
	}
	if err := rec.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got := openPurchaseIDs(t, api, productID, note); len(got) != 1 || got[0] != savedRef {
		t.Fatalf("lost reply purchases = %v, want [%s]", got, savedRef)
	}
	link, _, err = m.Get(linkInstance, "inst-bp")
	if err != nil || link.GrocyRef != savedRef {
		t.Fatalf("ref = %q (%v)", link.GrocyRef, err)
	}

	dup, err := api.AddStock(ctx, productID, addStockBody{
		Amount:          1,
		BestBefore:      entry.BestBefore,
		Purchased:       entry.Purchased[:10],
		TransactionType: "purchase",
		LocationID:      liveLocationID(t, api, "Pantry"),
		Note:            note,
	})
	if err != nil {
		t.Fatal(err)
	}
	if liveStockAmount(t, api, "Baking Powder", note) != 2 {
		t.Fatalf("duplicate amount = %v", liveStockAmount(t, api, "Baking Powder", note))
	}
	if err := rec.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if liveStockAmount(t, api, "Baking Powder", note) != 1 {
		t.Fatalf("amount after repair = %v", liveStockAmount(t, api, "Baking Powder", note))
	}
	if !purchaseUndone(t, api, productID, dup.TransactionID) {
		t.Fatalf("extra transaction %s is still open", dup.TransactionID)
	}
	repaired := openPurchaseIDs(t, api, productID, note)
	if len(repaired) != 1 {
		t.Fatalf("open purchases after repair = %v", repaired)
	}
	if err := rec.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got := openPurchaseIDs(t, api, productID, note); len(got) != 1 || got[0] != repaired[0] {
		t.Fatalf("repair was not idempotent: %v then %v", repaired, got)
	}

	used := liveSnapshot("used")
	rec.Pantry = &fixedSource{snap: used}
	if err := rec.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if liveStockAmount(t, api, "Baking Powder", note) != 0 {
		t.Fatalf("amount after use = %v", liveStockAmount(t, api, "Baking Powder", note))
	}
	if !hasConsume(t, api, productID, note) {
		t.Fatal("use did not record a consume")
	}

	rec.Pantry = &fixedSource{snap: liveSnapshot("shelf")}
	if err := rec.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if liveStockAmount(t, api, "Baking Powder", note) != 1 {
		t.Fatalf("amount after correction = %v", liveStockAmount(t, api, "Baking Powder", note))
	}

	rec.Pantry = &fixedSource{snap: liveSnapshot("redated")}
	if err := rec.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	redated := liveEntry(t, api, productID, note)
	if redated.Purchased[:10] != "2024-11-04" || float64(redated.Amount) != 1 {
		t.Fatalf("redated entry = %#v", redated)
	}

	assertStrayShoppingRow(t, api)
	runIntegrationBackfillAsOwner(t, m, dbPath, container, productID)
}

func runIntegrationBackfillAsOwner(t *testing.T, m *Map, dbPath, container string, stockProduct int) {
	t.Helper()
	amount := stockTableAmount(t, dbPath, stockProduct)
	products := map[string]int{}
	for _, typ := range []string{linkProduct, linkParent} {
		links, err := m.ByType(typ)
		if err != nil {
			t.Fatal(err)
		}
		for _, link := range links {
			if link.GrocyID > 0 {
				products[link.PantryID] = link.GrocyID
			}
		}
	}
	loc, ok, err := m.Get(linkLocation, "Pantry")
	if err != nil || !ok || loc.GrocyID == 0 {
		t.Fatalf("location = %#v %v", loc, err)
	}
	raw, err := json.Marshal(products)
	if err != nil {
		t.Fatal(err)
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dst := "/tmp/grocy-integration.test"
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatal(err)
	}
	stop := exec.Command("sudo", "-n", "docker", "stop", container)
	stop.Stdout, stop.Stderr = os.Stderr, os.Stderr
	if err := stop.Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		start := exec.Command("sudo", "-n", "docker", "start", container)
		_ = start.Run()
	})
	if _, err := os.Stat(dbPath + "-wal"); err == nil {
		t.Fatalf("%s-wal is still present after stopping %s", dbPath, container)
	}
	cmd := exec.Command("sudo", "-n", "-u", "#1001", "env",
		"GROCY_INTEGRATION=1",
		"GROCY_INTEGRATION_PART=backfill",
		"GROCY_DB="+dbPath,
		"GROCY_LOCATION_ID="+strconv.Itoa(loc.GrocyID),
		"GROCY_PRODUCTS="+string(raw),
		"GROCY_STOCK_PRODUCT="+strconv.Itoa(stockProduct),
		"GROCY_STOCK_AMOUNT="+strconv.FormatFloat(amount, 'f', -1, 64),
		"HOME=/tmp",
		"PATH="+os.Getenv("PATH"),
		dst, "-test.run", "^TestGrocyIntegration$", "-test.v", "-test.count=1",
	)
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("backfill as uid 1001: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "BACKFILL_OK") {
		t.Fatalf("backfill output missing BACKFILL_OK:\n%s", out)
	}
}

func runIntegrationBackfill(t *testing.T) {
	if os.Getuid() != 1001 {
		t.Fatalf("backfill ran as uid %d", os.Getuid())
	}
	dbPath := os.Getenv("GROCY_DB")
	var products map[string]int
	if err := json.Unmarshal([]byte(os.Getenv("GROCY_PRODUCTS")), &products); err != nil {
		t.Fatal(err)
	}
	locationID, err := strconv.Atoi(os.Getenv("GROCY_LOCATION_ID"))
	if err != nil {
		t.Fatal(err)
	}
	stockProduct, err := strconv.Atoi(os.Getenv("GROCY_STOCK_PRODUCT"))
	if err != nil {
		t.Fatal(err)
	}
	wantAmount, err := strconv.ParseFloat(os.Getenv("GROCY_STOCK_AMOUNT"), 64)
	if err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyHistory(context.Background(), HistoryOptions{
		DBPath:     dbPath,
		LocationID: locationID,
		Products:   products,
		Snap:       liveSnapshot("shelf"),
		Loc:        loc,
		Confirm:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	gotAmount := stockTableAmount(t, dbPath, stockProduct)
	if gotAmount != wantAmount {
		t.Fatalf("stock amount %v, want %v", gotAmount, wantAmount)
	}
	stamps := historyStamps(t, dbPath)
	stockIn := time.Date(2024, 11, 3, 5, 30, 0, 0, time.UTC)
	histIn := time.Date(2023, 1, 15, 16, 0, 0, 0, time.UTC)
	histOut := time.Date(2023, 2, 1, 16, 0, 0, 0, time.UTC)
	want := map[string]string{
		historyPrefix + "in-bp":    stamp(stockIn, loc),
		historyPrefix + "hist-in":  stamp(histIn, loc),
		historyPrefix + "hist-out": stamp(histOut, loc),
	}
	for note, wantStamp := range want {
		if canonicalStamp(stamps[note]) != wantStamp {
			t.Fatalf("journal %s = %q, want %q (all %v)", note, stamps[note], wantStamp, stamps)
		}
	}
	t.Logf("BACKFILL_OK inserted=%d updated=%d undone=%d stamps=%v amount=%v", result.Inserted, result.Updated, result.Undone, stamps, gotAmount)
	fmt.Printf("BACKFILL_OK inserted=%d updated=%d undone=%d amount=%v\n", result.Inserted, result.Updated, result.Undone, gotAmount)
	for note, stamp := range stamps {
		fmt.Printf("JOURNAL %s %s\n", note, stamp)
	}
}

func liveSnapshot(phase string) syncsnap.Snapshot {
	stockIn := time.Date(2024, 11, 3, 5, 30, 0, 0, time.UTC)
	expires := time.Date(2025, 11, 2, 0, 0, 0, 0, time.UTC)
	histIn := time.Date(2023, 1, 15, 16, 0, 0, 0, time.UTC)
	histOut := time.Date(2023, 2, 1, 16, 0, 0, 0, time.UTC)
	twoHundred := 200.0
	oneSeventy := 170.0
	threeForty := 340.0
	target := 2
	snap := syncsnap.Snapshot{
		Products: []syncsnap.Product{
			{ID: "bp-can", Name: "Baking Powder", Category: "Baking", GroupID: "g-bp", TargetQuantity: &target, Barcodes: []string{"012345678905"}},
			{ID: "tp-mutti", Name: "Tomato paste", Brand: "Mutti", NetBaseValue: &twoHundred, NetDimension: "mass", Barcodes: []string{"8076801230111"}},
			{ID: "tp-cento", Name: "Tomato paste", Brand: "Cento", NetBaseValue: &oneSeventy, NetDimension: "mass"},
			{ID: "tp-plain", Name: "Tomato paste", NetBaseValue: &threeForty, NetDimension: "mass"},
		},
		Groups: []syncsnap.Group{{
			ID: "g-bp", Name: "Baking powder", Rule: "favorite", MemberIDs: []string{"bp-can"},
		}},
		Moves: []syncsnap.Move{
			{ID: "in-bp", ProductID: "bp-can", Direction: "in", Quantity: 1, At: stockIn},
			{ID: "hist-in", ProductID: "tp-mutti", Direction: "in", Quantity: 1, At: histIn},
			{ID: "hist-out", ProductID: "tp-mutti", Direction: "out", Quantity: 1, At: histOut},
		},
		Removals: []syncsnap.Removal{{
			InstanceID: "inst-old", ProductID: "tp-mutti", Reason: "consumed",
			StockMoveID: "hist-in", RemovedByMoveID: "hist-out", StockInAt: histIn,
		}},
		ShoppingList: []syncsnap.ShoppingLine{
			{ID: "line-bp", ProductID: "bp-can", GroupID: "g-bp", Quantity: 2, Source: "auto"},
			{ID: "line-paste", ProductID: "tp-mutti", Quantity: 1, Source: "manual"},
		},
	}
	switch phase {
	case "shelf":
		snap.Instances = []syncsnap.Instance{{
			ID: "inst-bp", ProductID: "bp-can", StockInAt: stockIn, ExpiresAt: &expires, StockMoveID: "in-bp",
		}}
	case "used":
		snap.Removals = append(snap.Removals, syncsnap.Removal{
			InstanceID: "inst-bp", ProductID: "bp-can", Reason: "expired",
			StockMoveID: "in-bp", RemovedByMoveID: "out-bp", StockInAt: stockIn, ExpiresAt: &expires,
		})
		snap.Moves = append(snap.Moves, syncsnap.Move{ID: "out-bp", ProductID: "bp-can", Direction: "out", Quantity: 1, At: stockIn.Add(24 * time.Hour)})
	case "redated":
		later := stockIn.Add(48 * time.Hour)
		snap.Instances = []syncsnap.Instance{{
			ID: "inst-bp", ProductID: "bp-can", StockInAt: later, ExpiresAt: &expires, StockMoveID: "in-bp",
		}}
		snap.Moves[0].At = later
	}
	return snap
}

func assertLiveCatalog(t *testing.T, api *API) {
	t.Helper()
	names := map[string]bool{}
	for _, row := range liveObjects(t, api, "products") {
		names[row.Name] = true
	}
	for _, name := range []string{"Baking powder (group)", "Baking Powder", "Tomato paste (Mutti)", "Tomato paste (Cento)", "Tomato paste (340 g)"} {
		if !names[name] {
			t.Fatalf("missing %q in %v", name, names)
		}
	}
	var lists []string
	var pantryID int
	for _, row := range liveObjects(t, api, "shopping_lists") {
		lists = append(lists, row.Name)
		if row.Name == "Pantry" {
			pantryID = row.ID
		}
	}
	if pantryID == 0 || !contains(lists, "Shopping list") {
		t.Fatalf("lists = %v", lists)
	}
	var onPantry int
	for _, row := range liveObjects(t, api, "shopping_list") {
		if row.ShoppingListID == pantryID && strings.HasPrefix(row.note(), linePrefix) {
			onPantry++
		}
	}
	if onPantry != 2 {
		t.Fatalf("Pantry shopping lines = %d", onPantry)
	}
	parent := liveProductID(t, api, "Baking powder (group)")
	child := liveProduct(t, api, "Baking Powder")
	if int(child["parent_product_id"].(float64)) != parent {
		t.Fatalf("parent = %v, want %d", child["parent_product_id"], parent)
	}
	fields, err := api.Userfields(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	if fields["pantry_name"] != "Baking powder" || fields["pantry_id"] != parentMarker("g-bp") {
		t.Fatalf("parent fields = %v", fields)
	}
}

func ensureStrayShoppingRow(ctx context.Context, api *API) error {
	for _, row := range mustObjects(api, "shopping_list") {
		if row.note() == "leave me" {
			return nil
		}
	}
	var productID, locationID, unitID, listID int
	for _, row := range mustObjects(api, "products") {
		if row.Name == "Chalk" {
			productID = row.ID
		}
	}
	for _, row := range mustObjects(api, "locations") {
		if locationID == 0 {
			locationID = row.ID
		}
	}
	for _, row := range mustObjects(api, "quantity_units") {
		if row.Name == "Piece" {
			unitID = row.ID
		}
	}
	for _, row := range mustObjects(api, "shopping_lists") {
		if row.Name != "Pantry" && listID == 0 {
			listID = row.ID
		}
	}
	if listID == 0 || locationID == 0 || unitID == 0 {
		return fmt.Errorf("Grocy has no default shopping list, location, or Piece unit")
	}
	if productID == 0 {
		id, err := api.Create(ctx, "products", map[string]any{
			"name": "Chalk", "location_id": locationID, "qu_id_purchase": unitID, "qu_id_stock": unitID, "active": 1,
		})
		if err != nil {
			return err
		}
		productID = id
	}
	_, err := api.Create(ctx, "shopping_list", map[string]any{
		"product_id": productID, "shopping_list_id": listID, "amount": 1, "note": "leave me", "done": 0,
	})
	return err
}

func assertStrayShoppingRow(t *testing.T, api *API) {
	t.Helper()
	var pantryID, defaultID int
	for _, row := range liveObjects(t, api, "shopping_lists") {
		if row.Name == "Pantry" {
			pantryID = row.ID
		}
		if row.Name == "Shopping list" {
			defaultID = row.ID
		}
	}
	var found bool
	for _, row := range liveObjects(t, api, "shopping_list") {
		if row.note() == "leave me" {
			found = true
			if row.ShoppingListID == pantryID || row.ShoppingListID != defaultID {
				t.Fatalf("stray row moved: %#v", row)
			}
		}
	}
	if !found {
		t.Fatal("default shopping list lost its row")
	}
}

func liveStockAmount(t *testing.T, api *API, productName, note string) float64 {
	t.Helper()
	id := liveProductID(t, api, productName)
	var sum float64
	for _, entry := range mustEntries(t, api, id) {
		if entry.note() == note {
			sum += float64(entry.Amount)
		}
	}
	return sum
}

func liveEntry(t *testing.T, api *API, productID int, note string) StockEntry {
	t.Helper()
	for _, entry := range mustEntries(t, api, productID) {
		if entry.note() == note {
			return entry
		}
	}
	t.Fatalf("no stock entry %s", note)
	return StockEntry{}
}

func liveProductID(t *testing.T, api *API, name string) int {
	t.Helper()
	for _, row := range liveObjects(t, api, "products") {
		if row.Name == name {
			return row.ID
		}
	}
	t.Fatalf("no product %q", name)
	return 0
}

func liveLocationID(t *testing.T, api *API, name string) int {
	t.Helper()
	for _, row := range liveObjects(t, api, "locations") {
		if row.Name == name {
			return row.ID
		}
	}
	t.Fatalf("no location %q", name)
	return 0
}

func liveProduct(t *testing.T, api *API, name string) map[string]any {
	t.Helper()
	id := liveProductID(t, api, name)
	var body map[string]any
	if err := api.do(context.Background(), http.MethodGet, fmt.Sprintf("/objects/products/%d", id), nil, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func openPurchaseIDs(t *testing.T, api *API, productID int, note string) []string {
	t.Helper()
	var ids []string
	for _, row := range mustLogs(t, api, productID) {
		if row.note() == note && row.TransactionType == "purchase" && !row.undone() {
			ids = append(ids, row.TransactionID.String())
		}
	}
	return ids
}

func purchaseUndone(t *testing.T, api *API, productID int, transactionID string) bool {
	t.Helper()
	for _, row := range mustLogs(t, api, productID) {
		if row.TransactionID.String() == transactionID {
			return row.undone()
		}
	}
	t.Fatalf("missing transaction %s", transactionID)
	return false
}

func hasConsume(t *testing.T, api *API, productID int, note string) bool {
	t.Helper()
	for _, row := range mustLogs(t, api, productID) {
		if row.note() == note && row.TransactionType == "consume" && !row.undone() {
			return true
		}
	}
	return false
}

func liveObjects(t *testing.T, api *API, entity string) []objectRow {
	t.Helper()
	rows, err := api.List(context.Background(), entity)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func mustObjects(api *API, entity string) []objectRow {
	rows, err := api.List(context.Background(), entity)
	if err != nil {
		panic(err)
	}
	return rows
}

func mustEntries(t *testing.T, api *API, productID int) []StockEntry {
	t.Helper()
	entries, err := api.StockEntries(context.Background(), productID)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func mustLogs(t *testing.T, api *API, productID int) []StockLog {
	t.Helper()
	rows, err := api.StockLogs(context.Background(), productID)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func stockAmountIfPresent(api *API, productName, note string) (float64, bool) {
	id, ok := productIDByName(api, productName)
	if !ok {
		return 0, false
	}
	entries, err := api.StockEntries(context.Background(), id)
	if err != nil {
		return 0, false
	}
	var sum float64
	for _, entry := range entries {
		if entry.note() == note {
			sum += float64(entry.Amount)
		}
	}
	return sum, true
}

func productIDByName(api *API, name string) (int, bool) {
	rows, err := api.List(context.Background(), "products")
	if err != nil {
		return 0, false
	}
	for _, row := range rows {
		if row.Name == name {
			return row.ID, true
		}
	}
	return 0, false
}

func stockTableAmount(t *testing.T, dbPath string, productID int) float64 {
	t.Helper()
	db, err := openLiveDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var amount float64
	if err := db.QueryRow(`SELECT COALESCE(SUM(amount), 0) FROM stock WHERE product_id = ?`, productID).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	return amount
}

func historyStamps(t *testing.T, dbPath string) map[string]string {
	t.Helper()
	db, err := openLiveDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT note, row_created_timestamp FROM stock_log WHERE note LIKE ? AND undone = 0`, historyPrefix+"%")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var note, stamp string
		if err := rows.Scan(&note, &stamp); err != nil {
			t.Fatal(err)
		}
		out[note] = stamp
	}
	return out
}

func openLiveDB(path string) (*sql.DB, error) {
	db, err := app.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

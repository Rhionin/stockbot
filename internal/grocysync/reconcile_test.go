package grocysync

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/syncsnap"
)

func TestReconcileMirrorsPantryAndIsIdempotent(t *testing.T) {
	fake := newFakeGrocy()
	srv := fake.server()
	defer srv.Close()
	api, err := NewAPI(srv.URL, fake.key)
	if err != nil {
		t.Fatal(err)
	}
	m, err := OpenMap(filepath.Join(t.TempDir(), "map.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	exp := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	inAt := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	pack := 6
	base := 400.0
	target := 4
	window := 2
	snap := syncsnap.Snapshot{
		Products: []syncsnap.Product{
			{
				ID: "p-chick", Name: "Chickpeas", Category: "Canned",
				NetBaseValue: &base, NetDimension: "mass", PackCount: &pack,
				Barcodes: []string{"111"}, TargetQuantity: &target, GroupID: "g1",
			},
			{
				ID: "p-oats", Name: "Oats", Category: "Cereal",
				Barcodes: []string{"222"},
			},
		},
		Groups: []syncsnap.Group{{
			ID: "g1", Name: "Beans", Rule: "same_as_ran_out", WindowMonths: &window,
			MemberIDs: []string{"p-chick"},
		}},
		Instances: []syncsnap.Instance{{
			ID: "inst-1", ProductID: "p-chick", StockInAt: inAt, ExpiresAt: &exp, StockMoveID: "move-in",
		}},
		Moves: []syncsnap.Move{{
			ID: "move-in", ProductID: "p-chick", Direction: "in", Quantity: 1, At: inAt,
		}},
		ShoppingList: []syncsnap.ShoppingLine{
			{ID: "line-1", ProductID: "p-chick", GroupID: "g1", Quantity: 2, Source: "auto"},
			{ID: "line-2", ProductID: "p-oats", Quantity: 1, Source: "manual"},
		},
	}
	// A row already on Grocy's default list, and a stray row that will land on the Pantry list.
	if _, err := api.Create(context.Background(), "shopping_lists", map[string]any{"name": "Default"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Create(context.Background(), "shopping_list", map[string]any{
		"product_id": 1, "shopping_list_id": 1, "amount": 9, "note": "leave me", "done": 0,
	}); err != nil {
		t.Fatal(err)
	}
	src := &fixedSource{snap: snap}
	rec := &Reconciler{Pantry: src, Grocy: api, Map: m, Loc: time.UTC}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	posts := fake.count("POST", "/objects/products")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fake.count("POST", "/objects/products"); got != posts {
		t.Fatalf("second reconcile created products, posts %d then %d", posts, got)
	}
	if got := fake.count("POST", "/stock/products/"); got != 1 {
		t.Fatalf("stock adds = %d, want 1", got)
	}

	parent := fake.productByMarker("group:g1")
	if parent == nil || strField(parent, "name") != "Beans" {
		t.Fatalf("parent = %#v", parent)
	}
	if numField(parent, "cumulate_min_stock_amount_of_sub_products") != 1 || numField(parent, "no_own_stock") != 1 {
		t.Fatalf("parent stock flags = %#v", parent)
	}
	if numField(parent, "min_stock_amount") != 0 {
		t.Fatalf("parent min stock = %v", parent["min_stock_amount"])
	}
	child := fake.productByMarker("p-chick")
	if child == nil {
		t.Fatal("missing chickpeas")
	}
	if numField(child, "parent_product_id") != numField(parent, "id") {
		t.Fatalf("child parent = %v, parent id = %v", child["parent_product_id"], parent["id"])
	}
	if numField(child, "min_stock_amount") != 4 {
		t.Fatalf("min stock = %v", child["min_stock_amount"])
	}
	if !strings.Contains(strField(child, "description"), "400 g") || !strings.Contains(strField(child, "description"), "pack of 6") {
		t.Fatalf("description = %q", child["description"])
	}
	oats := fake.productByMarker("p-oats")
	if oats == nil || oats["parent_product_id"] != nil {
		t.Fatalf("oats = %#v", oats)
	}

	var labels []string
	for _, obj := range fake.objects["product_groups"] {
		labels = append(labels, strField(obj, "name"))
	}
	if !hasAll(labels, "Beans", "Cereal") {
		t.Fatalf("labels = %v", labels)
	}
	if childGroup := numField(child, "product_group_id"); childGroup == 0 || childGroup != numField(parent, "product_group_id") {
		t.Fatalf("child group %d parent group %d", childGroup, numField(parent, "product_group_id"))
	}

	if len(fake.objects["product_barcodes"]) != 2 {
		t.Fatalf("barcodes = %d", len(fake.objects["product_barcodes"]))
	}
	if len(fake.stock) != 1 || fake.stock[0].Purchased != "2026-01-02" || fake.stock[0].BestBefore != "2026-06-01" {
		t.Fatalf("stock = %#v", fake.stock)
	}
	var pantryList int
	for _, obj := range fake.objects["shopping_lists"] {
		if strField(obj, "name") == "Pantry" {
			pantryList = numField(obj, "id")
		}
		if strField(obj, "name") == "Default" && numField(obj, "id") == pantryList {
			t.Fatal("renamed the default list")
		}
	}
	var onPantry, onDefault int
	for _, obj := range fake.objects["shopping_list"] {
		if numField(obj, "shopping_list_id") == pantryList {
			onPantry++
			if strField(obj, "note") == "pantry-line:line-1" && numField(obj, "product_id") != numField(parent, "id") {
				t.Fatalf("group line product = %v", obj["product_id"])
			}
		}
		if strField(obj, "note") == "leave me" {
			onDefault++
		}
	}
	if onPantry != 2 || onDefault != 1 {
		t.Fatalf("pantry lines %d, default lines %d", onPantry, onDefault)
	}
	for _, body := range fake.bodies {
		if strings.Contains(body, `"price"`) {
			t.Fatalf("sent a price: %s", body)
		}
	}
}

func TestReconcileStockCorrections(t *testing.T) {
	fake := newFakeGrocy()
	srv := fake.server()
	defer srv.Close()
	api, err := NewAPI(srv.URL, fake.key)
	if err != nil {
		t.Fatal(err)
	}
	m, err := OpenMap(filepath.Join(t.TempDir(), "map.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	inAt := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	exp := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	snap := syncsnap.Snapshot{
		Products: []syncsnap.Product{{ID: "p1", Name: "Milk"}},
		Instances: []syncsnap.Instance{{
			ID: "i1", ProductID: "p1", StockInAt: inAt, ExpiresAt: &exp, StockMoveID: "in1",
		}},
		Moves: []syncsnap.Move{{ID: "in1", ProductID: "p1", Direction: "in", Quantity: 1, At: inAt}},
	}
	rec := &Reconciler{Pantry: &fixedSource{snap: snap}, Grocy: api, Map: m, Loc: time.UTC}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Used, and the stock-in is still in the history: consume, don't undo the purchase.
	used := snap
	used.Instances = nil
	used.Removals = []syncsnap.Removal{{InstanceID: "i1", ProductID: "p1", Reason: "expired", StockMoveID: "in1", RemovedByMoveID: "out1"}}
	used.Moves = append(used.Moves, syncsnap.Move{ID: "out1", ProductID: "p1", Direction: "out", Quantity: 1, At: inAt.Add(time.Hour)})
	rec.Pantry = &fixedSource{snap: used}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.stock) != 0 {
		t.Fatalf("stock after use = %#v", fake.stock)
	}
	if fake.count("POST", "/consume") != 1 {
		t.Fatalf("consumes = %d", fake.count("POST", "/consume"))
	}
	// The use was undone in Pantry. The unit is back, so the consume booking is undone.
	rec.Pantry = &fixedSource{snap: snap}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.stock) != 1 {
		t.Fatalf("stock after undo use = %#v", fake.stock)
	}
	// The stock-in itself was undone. No move, no unit: undo the purchase.
	rec.Pantry = &fixedSource{snap: syncsnap.Snapshot{Products: snap.Products}}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.stock) != 0 {
		t.Fatalf("stock after undo purchase = %#v", fake.stock)
	}
}

func TestReconcileRewritesPurchaseDate(t *testing.T) {
	fake := newFakeGrocy()
	srv := fake.server()
	defer srv.Close()
	api, err := NewAPI(srv.URL, fake.key)
	if err != nil {
		t.Fatal(err)
	}
	m, err := OpenMap(filepath.Join(t.TempDir(), "map.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	inAt := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	snap := syncsnap.Snapshot{
		Products:  []syncsnap.Product{{ID: "p1", Name: "Milk"}},
		Instances: []syncsnap.Instance{{ID: "i1", ProductID: "p1", StockInAt: inAt, StockMoveID: "in1"}},
		Moves:     []syncsnap.Move{{ID: "in1", ProductID: "p1", Direction: "in", Quantity: 1, At: inAt}},
	}
	rec := &Reconciler{Pantry: &fixedSource{snap: snap}, Grocy: api, Map: m, Loc: time.UTC}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	later := inAt.Add(48 * time.Hour)
	snap.Instances[0].StockInAt = later
	snap.Moves[0].At = later
	rec.Pantry = &fixedSource{snap: snap}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.stock) != 1 || fake.stock[0].Purchased != "2026-03-03" {
		t.Fatalf("stock = %#v", fake.stock)
	}
	if fake.count("POST", "/undo") < 1 {
		t.Fatal("expected the old booking to be undone")
	}
}

func TestReconcileKeepsOnePurchaseWhenGrocyAlreadyHasTheNote(t *testing.T) {
	fake := newFakeGrocy()
	srv := fake.server()
	defer srv.Close()
	api, err := NewAPI(srv.URL, fake.key)
	if err != nil {
		t.Fatal(err)
	}
	m, err := OpenMap(filepath.Join(t.TempDir(), "map.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	inAt := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	snap := syncsnap.Snapshot{
		Products:  []syncsnap.Product{{ID: "p1", Name: "Milk"}},
		Instances: []syncsnap.Instance{{ID: "i1", ProductID: "p1", StockInAt: inAt, StockMoveID: "in1"}},
		Moves:     []syncsnap.Move{{ID: "in1", ProductID: "p1", Direction: "in", Quantity: 1, At: inAt}},
	}
	rec := &Reconciler{Pantry: &fixedSource{snap: snap}, Grocy: api, Map: m, Loc: time.UTC}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	link, ok, err := m.Get(linkInstance, "i1")
	if err != nil || !ok || link.GrocyRef == "" {
		t.Fatalf("link = %#v %v", link, err)
	}
	saved := link.GrocyRef
	link.GrocyRef = ""
	if err := m.Put(link); err != nil {
		t.Fatal(err)
	}
	adds := fake.count("POST", "/stock/products/")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fake.count("POST", "/stock/products/"); got != adds {
		t.Fatalf("lost reply was added again, posts %d then %d", adds, got)
	}
	link, _, err = m.Get(linkInstance, "i1")
	if err != nil || link.GrocyRef != saved {
		t.Fatalf("ref = %q, want %q (%v)", link.GrocyRef, saved, err)
	}
	if len(fake.stock) != 1 || fake.stock[0].Amount != 1 {
		t.Fatalf("stock = %#v", fake.stock)
	}

	if err := m.Delete(linkInstance, "i1"); err != nil {
		t.Fatal(err)
	}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fake.count("POST", "/stock/products/"); got != adds {
		t.Fatalf("missing map row added again, posts %d", got)
	}

	extra := fake.duplicatePurchase(instancePrefix + "i1")
	if extra == "" || extra == saved {
		t.Fatalf("extra = %q", extra)
	}
	if len(fake.stock) != 1 || fake.stock[0].Amount != 2 {
		t.Fatalf("duplicated stock = %#v", fake.stock)
	}
	undos := fake.count("POST", "/undo")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Undoing the extra purchase deletes the shared stock row, so both bookings are undone and one unit is added back.
	if got := fake.count("POST", "/stock/products/"); got != adds+1 {
		t.Fatalf("repair posts = %d, want %d", got, adds+1)
	}
	if got := fake.count("POST", "/undo"); got != undos+2 {
		t.Fatalf("undos = %d, want %d", got, undos+2)
	}
	if !fake.saw("POST", "/stock/transactions/"+extra+"/undo") {
		t.Fatalf("extra transaction %s was not undone: %v", extra, fake.calls)
	}
	if len(fake.stock) != 1 || fake.stock[0].Amount != 1 {
		t.Fatalf("stock after repair = %#v", fake.stock)
	}
	adds = fake.count("POST", "/stock/products/")
	undos = fake.count("POST", "/undo")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fake.count("POST", "/stock/products/"); got != adds {
		t.Fatalf("third reconcile added stock, posts %d", got)
	}
	if got := fake.count("POST", "/undo"); got != undos {
		t.Fatalf("third reconcile undid again, undos %d", got)
	}
}

func TestReconcileConsumesAfterUndoingADuplicatePurchase(t *testing.T) {
	fake := newFakeGrocy()
	srv := fake.server()
	defer srv.Close()
	api, err := NewAPI(srv.URL, fake.key)
	if err != nil {
		t.Fatal(err)
	}
	m, err := OpenMap(filepath.Join(t.TempDir(), "map.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	inAt := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	snap := syncsnap.Snapshot{
		Products:  []syncsnap.Product{{ID: "p1", Name: "Milk"}},
		Instances: []syncsnap.Instance{{ID: "i1", ProductID: "p1", StockInAt: inAt, StockMoveID: "in1"}},
		Moves:     []syncsnap.Move{{ID: "in1", ProductID: "p1", Direction: "in", Quantity: 1, At: inAt}},
	}
	rec := &Reconciler{Pantry: &fixedSource{snap: snap}, Grocy: api, Map: m, Loc: time.UTC}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if extra := fake.duplicatePurchase(instancePrefix + "i1"); extra == "" {
		t.Fatal("no duplicate")
	}
	used := snap
	used.Instances = nil
	used.Removals = []syncsnap.Removal{{InstanceID: "i1", ProductID: "p1", Reason: "expired", StockMoveID: "in1", RemovedByMoveID: "out1"}}
	used.Moves = append(append([]syncsnap.Move{}, snap.Moves...), syncsnap.Move{ID: "out1", ProductID: "p1", Direction: "out", Quantity: 1, At: inAt.Add(time.Hour)})
	rec.Pantry = &fixedSource{snap: used}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.stock) != 0 {
		t.Fatalf("stock = %#v", fake.stock)
	}
	if fake.count("POST", "/consume") != 1 {
		t.Fatalf("consumes = %d", fake.count("POST", "/consume"))
	}
}

func TestReconcileIncludesGrocyStockError(t *testing.T) {
	const msg = "The given best before date is invalid"
	fake := newFakeGrocy()
	fake.bookingError = msg
	srv := fake.server()
	defer srv.Close()
	api, err := NewAPI(srv.URL, fake.key)
	if err != nil {
		t.Fatal(err)
	}
	m, err := OpenMap(filepath.Join(t.TempDir(), "map.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	inAt := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	rec := &Reconciler{
		Pantry: &fixedSource{snap: syncsnap.Snapshot{
			Products:  []syncsnap.Product{{ID: "p1", Name: "Milk"}},
			Instances: []syncsnap.Instance{{ID: "i1", ProductID: "p1", StockInAt: inAt, StockMoveID: "in1"}},
			Moves:     []syncsnap.Move{{ID: "in1", ProductID: "p1", Direction: "in", Quantity: 1, At: inAt}},
		}},
		Grocy: api, Map: m, Loc: time.UTC,
	}
	err = rec.Reconcile(context.Background())
	if err == nil || !strings.Contains(err.Error(), "could not add stock in Grocy") || !strings.Contains(err.Error(), msg) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), fake.key) {
		t.Fatalf("error leaked the API key: %v", err)
	}

	fake.bookingError = ""
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.undoError = "This transaction was not found or already undone"
	later := inAt.Add(48 * time.Hour)
	rec.Pantry = &fixedSource{snap: syncsnap.Snapshot{
		Products:  []syncsnap.Product{{ID: "p1", Name: "Milk"}},
		Instances: []syncsnap.Instance{{ID: "i1", ProductID: "p1", StockInAt: later, StockMoveID: "in1"}},
		Moves:     []syncsnap.Move{{ID: "in1", ProductID: "p1", Direction: "in", Quantity: 1, At: later}},
	}}
	err = rec.Reconcile(context.Background())
	if err == nil || !strings.Contains(err.Error(), "could not undo a Grocy booking") || !strings.Contains(err.Error(), fake.undoError) {
		t.Fatalf("err = %v", err)
	}
}

func TestReconcileDeletesAProductThatLeft(t *testing.T) {
	fake := newFakeGrocy()
	srv := fake.server()
	defer srv.Close()
	api, err := NewAPI(srv.URL, fake.key)
	if err != nil {
		t.Fatal(err)
	}
	m, err := OpenMap(filepath.Join(t.TempDir(), "map.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	qty := 500.0
	snap := syncsnap.Snapshot{
		Products: []syncsnap.Product{{
			ID: "p1", Name: "Milk", Category: "Dairy",
			NetBaseValue: floatPtr(250), NetDimension: "volume", UnitOfMeasure: "carton",
		}},
		Groups: []syncsnap.Group{{
			ID: "g1", Name: "Milk", Rule: "favorite", PinnedProductID: "p1",
			QuantityBase: &qty, Dimension: "volume", MemberIDs: []string{"p1"},
		}},
	}
	snap.Products[0].GroupID = "g1"
	rec := &Reconciler{Pantry: &fixedSource{snap: snap}, Grocy: api, Map: m, Loc: time.UTC}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec.Pantry = &fixedSource{}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(fake.objects["products"]); n != 0 {
		t.Fatalf("products left = %d", n)
	}
	if n := len(fake.objects["product_groups"]); n != 0 {
		t.Fatalf("labels left = %d", n)
	}
}

func TestReconcileIncludesGrocyProductError(t *testing.T) {
	const msg = "SQLSTATE[HY000]: General error: 1 table products has no column named qu_factor_purchase_to_stock"
	for _, tc := range []struct {
		name string
		snap syncsnap.Snapshot
		want string
	}{
		{
			name: "group",
			snap: syncsnap.Snapshot{
				Groups:   []syncsnap.Group{{ID: "g1", Name: "Beans", Rule: "favorite"}},
				Products: []syncsnap.Product{{ID: "p1", Name: "Chickpeas", GroupID: "g1"}},
			},
			want: "could not save the Beans group in Grocy",
		},
		{
			name: "product",
			snap: syncsnap.Snapshot{
				Products: []syncsnap.Product{{ID: "p1", Name: "Chickpeas"}},
			},
			want: "could not save Chickpeas in Grocy",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeGrocy()
			fake.productError = msg
			srv := fake.server()
			defer srv.Close()
			api, err := NewAPI(srv.URL, fake.key)
			if err != nil {
				t.Fatal(err)
			}
			m, err := OpenMap(filepath.Join(t.TempDir(), "map.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			rec := &Reconciler{Pantry: &fixedSource{snap: tc.snap}, Grocy: api, Map: m, Loc: time.UTC}
			err = rec.Reconcile(context.Background())
			if err == nil || !strings.Contains(err.Error(), msg) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
			if !strings.Contains(err.Error(), "could not save the product in Grocy") {
				t.Fatalf("upsert error dropped: %v", err)
			}
			if strings.Contains(err.Error(), fake.key) {
				t.Fatalf("error leaked the API key: %v", err)
			}
		})
	}
}

func TestAssignGrocyNamesKeepsBothClashTypesStable(t *testing.T) {
	small := 170.0
	large := 340.0
	snap := syncsnap.Snapshot{
		Groups: []syncsnap.Group{{ID: "g-bake", Name: "Baking powder"}},
		Products: []syncsnap.Product{
			{ID: "bake", Name: "Baking Powder"},
			{ID: "hunt", Name: "Tomato paste", Brand: "Hunt's"},
			{ID: "cont", Name: "Tomato paste", Brand: "Contadina"},
			{ID: "oats-a", Name: "Oats", Brand: "Quaker", NetBaseValue: &small, NetDimension: "mass"},
			{ID: "oats-b", Name: "Oats", Brand: "Quaker", NetBaseValue: &large, NetDimension: "mass"},
			{ID: "product-aaaa-1111", Name: "Salt"},
			{ID: "product-bbbb-2222", Name: "Salt"},
		},
	}
	parents, products := assignGrocyNames(snap)
	againParents, againProducts := assignGrocyNames(snap)
	if parents["g-bake"] != "Baking powder (group)" || againParents["g-bake"] != parents["g-bake"] {
		t.Fatalf("parent = %#v", parents)
	}
	if products["bake"] != "Baking Powder" {
		t.Fatalf("member = %q", products["bake"])
	}
	if products["hunt"] != "Tomato paste (Hunt's)" || products["cont"] != "Tomato paste (Contadina)" {
		t.Fatalf("brands = %#v", products)
	}
	if products["oats-a"] != "Oats (170 g)" || products["oats-b"] != "Oats (340 g)" {
		t.Fatalf("sizes = %#v", products)
	}
	if products["product-aaaa-1111"] != "Salt (product-a)" || products["product-bbbb-2222"] != "Salt (product-b)" {
		t.Fatalf("ids = %#v", products)
	}
	if againProducts["hunt"] != products["hunt"] || againProducts["product-bbbb-2222"] != products["product-bbbb-2222"] {
		t.Fatal("names changed between calls")
	}
}

func TestReconcileRenamesClashingProductsInPlace(t *testing.T) {
	fake := newFakeGrocy()
	srv := fake.server()
	defer srv.Close()
	api, err := NewAPI(srv.URL, fake.key)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	bakeID, err := api.Create(ctx, "products", map[string]any{
		"name": "Baking Powder", "description": "created earlier", "location_id": 1, "qu_id_purchase": 1, "qu_id_stock": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := api.PutUserfields(ctx, bakeID, map[string]string{"pantry_id": "bake"}); err != nil {
		t.Fatal(err)
	}
	pasteID, err := api.Create(ctx, "products", map[string]any{
		"name": "Tomato paste", "description": "created earlier", "location_id": 1, "qu_id_purchase": 1, "qu_id_stock": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := api.PutUserfields(ctx, pasteID, map[string]string{"pantry_id": "paste-a"}); err != nil {
		t.Fatal(err)
	}
	m, err := OpenMap(filepath.Join(t.TempDir(), "map.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	snap := syncsnap.Snapshot{
		Groups: []syncsnap.Group{{ID: "g-bake", Name: "Baking powder", Rule: "favorite", MemberIDs: []string{"bake"}}},
		Products: []syncsnap.Product{
			{ID: "bake", Name: "Baking Powder", GroupID: "g-bake"},
			{ID: "paste-a", Name: "Tomato paste", Brand: "Hunt's"},
			{ID: "paste-b", Name: "Tomato paste", Brand: "Contadina"},
		},
	}
	rec := &Reconciler{Pantry: &fixedSource{snap: snap}, Grocy: api, Map: m, Loc: time.UTC}
	if err := rec.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(fake.objects["products"]); got != 4 {
		t.Fatalf("products = %d, want the two existing rows renamed plus Contadina and the group", got)
	}
	bake := fake.objects["products"][bakeID]
	if strField(bake, "name") != "Baking Powder" {
		t.Fatalf("baking powder = %#v", bake)
	}
	paste := fake.objects["products"][pasteID]
	if strField(paste, "name") != "Tomato paste (Hunt's)" || !strings.Contains(strField(paste, "description"), "Name: Tomato paste") {
		t.Fatalf("tomato paste = %#v", paste)
	}
	if fake.fields[pasteID]["pantry_id"] != "paste-a" || fake.fields[pasteID]["pantry_name"] != "Tomato paste" {
		t.Fatalf("fields = %#v", fake.fields[pasteID])
	}
	parent := fake.productByMarker("group:g-bake")
	if parent == nil || strField(parent, "name") != "Baking powder (group)" {
		t.Fatalf("parent = %#v", parent)
	}
	if fake.fields[numField(parent, "id")]["pantry_name"] != "Baking powder" {
		t.Fatalf("parent fields = %#v", fake.fields[numField(parent, "id")])
	}
	posts := fake.count("POST", "/objects/products")
	if err := rec.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.count("POST", "/objects/products") != posts {
		t.Fatal("second reconcile duplicated a clashing product")
	}
}

func floatPtr(v float64) *float64 { return &v }

func TestReconcileAdoptsExistingProductAfterMapLoss(t *testing.T) {
	fake := newFakeGrocy()
	srv := fake.server()
	defer srv.Close()
	api, err := NewAPI(srv.URL, fake.key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	m, err := OpenMap(filepath.Join(dir, "map.db"))
	if err != nil {
		t.Fatal(err)
	}
	snap := syncsnap.Snapshot{Products: []syncsnap.Product{{ID: "p1", Name: "Milk"}}}
	rec := &Reconciler{Pantry: &fixedSource{snap: snap}, Grocy: api, Map: m, Loc: time.UTC}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.Close()
	fresh, err := OpenMap(filepath.Join(dir, "other.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	rec.Map = fresh
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(fake.objects["products"]); n != 1 {
		t.Fatalf("products = %d", n)
	}
}

func TestReconcileRewritesDriftedShoppingLine(t *testing.T) {
	fake := newFakeGrocy()
	srv := fake.server()
	defer srv.Close()
	api, err := NewAPI(srv.URL, fake.key)
	if err != nil {
		t.Fatal(err)
	}
	m, err := OpenMap(filepath.Join(t.TempDir(), "map.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	snap := syncsnap.Snapshot{
		Products: []syncsnap.Product{{ID: "p-milk", Name: "Milk"}},
		ShoppingList: []syncsnap.ShoppingLine{{
			ID: "line-milk", ProductID: "p-milk", Quantity: 2, Source: "manual",
		}},
	}
	rec := &Reconciler{Pantry: &fixedSource{snap: snap}, Grocy: api, Map: m, Loc: time.UTC}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	var line map[string]any
	for _, obj := range fake.objects["shopping_list"] {
		if strField(obj, "note") == "pantry-line:line-milk" {
			line = obj
		}
	}
	if line == nil {
		t.Fatal("missing shopping line")
	}
	line["done"] = float64(1)
	line["amount"] = 2.5

	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if numField(line, "done") != 0 {
		t.Fatalf("done = %v, want 0", line["done"])
	}
	amount, ok := line["amount"].(float64)
	if !ok || amount != 2 {
		t.Fatalf("amount = %v, want 2", line["amount"])
	}

	puts := fake.count("PUT", "/objects/shopping_list/")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fake.count("PUT", "/objects/shopping_list/"); got != puts {
		t.Fatalf("third reconcile updated the shopping line, puts %d then %d", puts, got)
	}
	if numField(line, "done") != 0 || line["amount"] != float64(2) {
		t.Fatalf("line drifted again: %#v", line)
	}
}

func TestSyncBarcodesUpdatesAndRemovesPantryCodes(t *testing.T) {
	fake := newFakeGrocy()
	srv := fake.server()
	defer srv.Close()
	api, err := NewAPI(srv.URL, fake.key)
	if err != nil {
		t.Fatal(err)
	}
	m, err := OpenMap(filepath.Join(t.TempDir(), "map.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	snap := syncsnap.Snapshot{
		Products: []syncsnap.Product{{ID: "p1", Name: "Milk", Barcodes: []string{"111"}}},
	}
	rec := &Reconciler{Pantry: &fixedSource{snap: snap}, Grocy: api, Map: m, Loc: time.UTC}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	var kept int
	for id, obj := range fake.objects["product_barcodes"] {
		obj["note"] = "other"
		kept = id
	}
	fake.next++
	stray := fake.next
	fake.objects["product_barcodes"][stray] = map[string]any{
		"id": stray, "barcode": "999", "product_id": float64(1), "note": barcodeNote,
	}
	fake.mu.Unlock()
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	if _, ok := fake.objects["product_barcodes"][stray]; ok {
		t.Fatal("stray pantry barcode was kept")
	}
	if strField(fake.objects["product_barcodes"][kept], "note") != barcodeNote {
		t.Fatalf("barcode note = %#v", fake.objects["product_barcodes"][kept])
	}
	fake.mu.Unlock()
	if err := rec.syncBarcodes(context.Background(), syncsnap.Snapshot{
		Products: []syncsnap.Product{{ID: "missing", Name: "Gone", Barcodes: []string{"222"}}},
	}, map[string]int{}); err == nil || !strings.Contains(err.Error(), "not in Grocy") {
		t.Fatalf("missing product = %v", err)
	}
}

func TestClearLeftoverRemovesStockLeftAfterAUse(t *testing.T) {
	fake, rec, _ := reconcileUntilConsumed(t)
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.count("POST", "/consume") != 1 {
		t.Fatalf("first leftover pass consumed, count %d", fake.count("POST", "/consume"))
	}

	fake.mu.Lock()
	var productID int
	for i := range fake.logs {
		if fake.logs[i].Note == instancePrefix+"i1" {
			productID = fake.logs[i].ProductID
			fake.logs[i].Undone = 1
		}
	}
	fake.stock = append(fake.stock, fakeStock{
		ProductID: productID, Amount: 1, StockID: "left", Note: instancePrefix + "i1",
	})
	fake.mu.Unlock()
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.count("POST", "/consume") != 2 || len(fake.stock) != 0 {
		t.Fatalf("consumes %d stock %#v", fake.count("POST", "/consume"), fake.stock)
	}

	fake.bookingError = "stock entry is gone"
	fake.mu.Lock()
	fake.stock = append(fake.stock, fakeStock{
		ProductID: productID, Amount: 1, StockID: "again", Note: instancePrefix + "i1",
	})
	fake.mu.Unlock()
	if err := rec.Reconcile(context.Background()); err == nil || !strings.Contains(err.Error(), "could not record a use") {
		t.Fatalf("consume error = %v", err)
	}
}

func TestClearLeftoverUndoDropsTheSharedPurchase(t *testing.T) {
	fake, rec, _ := reconcileUntilConsumed(t)
	addOpenPurchase(t, fake, instancePrefix+"i1")
	undos := fake.count("POST", "/undo")
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.count("POST", "/undo") <= undos {
		t.Fatalf("undos %d then %d", undos, fake.count("POST", "/undo"))
	}
	if len(fake.stock) != 0 {
		t.Fatalf("stock %#v", fake.stock)
	}

	fake.undoError = "The transaction could not be undone"
	addOpenPurchase(t, fake, instancePrefix+"i1")
	if err := rec.Reconcile(context.Background()); err == nil || !strings.Contains(err.Error(), "could not undo a Grocy booking") {
		t.Fatalf("undo error = %v", err)
	}
}

// addOpenPurchase puts a stock row back with a new open purchase. A consume
// already deleted the row, so raising an old booking's amount would not.
func addOpenPurchase(t *testing.T, fake *fakeGrocy, note string) {
	t.Helper()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	var sample fakeLog
	for _, log := range fake.logs {
		if log.Note == note && log.Type == "purchase" {
			sample = log
			break
		}
	}
	if sample.ProductID == 0 {
		t.Fatal("no purchase to copy")
	}
	fake.next++
	tx := "tx" + strconv.Itoa(fake.next)
	fake.stock = append(fake.stock, fakeStock{
		ProductID: sample.ProductID, Amount: 1, StockID: sample.StockID, Note: note,
		BestBefore: sample.BestBefore, Purchased: sample.Purchased,
	})
	fake.logs = append(fake.logs, fakeLog{
		ID: fake.next, ProductID: sample.ProductID, Amount: 1, BestBefore: sample.BestBefore, Purchased: sample.Purchased,
		Type: "purchase", TxID: tx, StockID: sample.StockID, Note: note,
	})
	fake.txs[tx] = &fakeTx{Kind: "purchase", Entry: fakeStock{
		ProductID: sample.ProductID, Amount: 1, BestBefore: sample.BestBefore, Purchased: sample.Purchased,
		StockID: sample.StockID, Note: note,
	}}
}

func reconcileUntilConsumed(t *testing.T) (*fakeGrocy, *Reconciler, syncsnap.Snapshot) {
	t.Helper()
	fake := newFakeGrocy()
	srv := fake.server()
	t.Cleanup(srv.Close)
	api, err := NewAPI(srv.URL, fake.key)
	if err != nil {
		t.Fatal(err)
	}
	m, err := OpenMap(filepath.Join(t.TempDir(), "map.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	inAt := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	snap := syncsnap.Snapshot{
		Products:  []syncsnap.Product{{ID: "p1", Name: "Milk"}},
		Instances: []syncsnap.Instance{{ID: "i1", ProductID: "p1", StockInAt: inAt, StockMoveID: "in1"}},
		Moves:     []syncsnap.Move{{ID: "in1", ProductID: "p1", Direction: "in", Quantity: 1, At: inAt}},
	}
	rec := &Reconciler{Pantry: &fixedSource{snap: snap}, Grocy: api, Map: m, Loc: time.UTC}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	used := snap
	used.Instances = nil
	used.Removals = []syncsnap.Removal{{InstanceID: "i1", ProductID: "p1", Reason: "used", StockMoveID: "in1", RemovedByMoveID: "out1"}}
	used.Moves = append(append([]syncsnap.Move{}, snap.Moves...), syncsnap.Move{ID: "out1", ProductID: "p1", Direction: "out", Quantity: 1, At: inAt.Add(time.Hour)})
	rec.Pantry = &fixedSource{snap: used}
	if err := rec.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	link, ok, err := m.Get(linkConsume, "i1")
	if err != nil || !ok || link.GrocyRef == "" {
		t.Fatalf("consume link %#v ok %v err %v", link, ok, err)
	}
	return fake, rec, used
}

type fixedSource struct {
	snap syncsnap.Snapshot
}

func (s *fixedSource) Snapshot(context.Context) (syncsnap.Snapshot, error) {
	return s.snap, nil
}

func hasAll(got []string, want ...string) bool {
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	for _, w := range want {
		if !have[w] {
			return false
		}
	}
	return true
}

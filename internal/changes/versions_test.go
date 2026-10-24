package changes

import (
	"context"
	"database/sql"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	_ "modernc.org/sqlite"
)

func openVersions(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := db.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return db, id
}

func TestBumpMovesTheCursorForCreatesAndDeletes(t *testing.T) {
	db, house := openVersions(t)
	ctx := context.Background()
	before, err := Version(ctx, db, house, ScopeStock)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := Bump(ctx, tx, house, ScopeStock); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	after, err := Version(ctx, db, house, ScopeStock)
	if err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Fatalf("version %d, want %d", after, before+1)
	}
	cursor, err := Current(ctx, db, house, ScopeStock)
	if err != nil {
		t.Fatal(err)
	}
	if len(cursor) > 128 || strings.Contains(cursor, " ") || strings.Contains(cursor, house) {
		t.Fatalf("cursor %q leaks or is too long", cursor)
	}
	same, err := Compare(ctx, db, house, ScopeStock, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if same.Changed || !same.Recognized {
		t.Fatalf("equal cursor = %+v", same)
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := Bump(ctx, tx, house, ScopeStock); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	rolled, err := Compare(ctx, db, house, ScopeStock, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if rolled.Changed {
		t.Fatal("a rolled-back delete looked like a change")
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := Bump(ctx, tx, house, ScopeList, ScopeScans, ScopeDevices); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{ScopeList, ScopeScans, ScopeDevices} {
		got, err := Compare(ctx, db, house, scope, Cursor(house, scope, 0))
		if err != nil {
			t.Fatal(err)
		}
		if !got.Changed || !got.Recognized {
			t.Fatalf("%s after delete = %+v", scope, got)
		}
	}
}

func TestCursorFromAnotherHouseholdIsAFullRead(t *testing.T) {
	db, house := openVersions(t)
	ctx := context.Background()
	foreign := Cursor("other-household", ScopeStock, 0)
	got, err := Compare(ctx, db, house, ScopeStock, foreign)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Changed || got.Recognized {
		t.Fatalf("foreign = %+v", got)
	}
	otherScope := Cursor(house, ScopeList, 0)
	got, err = Compare(ctx, db, house, ScopeStock, otherScope)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Changed || got.Recognized {
		t.Fatalf("other scope = %+v", got)
	}
	if _, _, _, ok := Parse("not-a-cursor"); ok {
		t.Fatal("accepted garbage")
	}
	if _, _, _, ok := Parse(strings.Repeat("v", 200)); ok {
		t.Fatal("accepted a long cursor")
	}
}

func TestChangeVersionsRejectBadInput(t *testing.T) {
	db, house := openVersions(t)
	ctx := context.Background()
	if err := Bump(ctx, nil, house, ScopeStock); err != nil {
		t.Fatal(err)
	}
	if err := Bump(ctx, db, "  ", ScopeStock); err != nil {
		t.Fatal(err)
	}
	if err := Bump(ctx, db, house); err != nil {
		t.Fatal(err)
	}
	if err := Bump(ctx, db, house, "nope"); err == nil {
		t.Fatal("unknown scope was recorded")
	}
	if _, err := Version(ctx, nil, house, ScopeStock); err == nil {
		t.Fatal("nil reader")
	}
	if _, err := Version(ctx, db, house, "nope"); err == nil {
		t.Fatal("unknown scope was read")
	}
	if _, err := Current(ctx, db, house, "nope"); err == nil {
		t.Fatal("unknown scope became a cursor")
	}
	if _, _, _, ok := Parse("v1.!!!"); ok {
		t.Fatal("accepted a broken cursor")
	}
	broken := "v1." + encodeRaw("only|two")
	if _, _, _, ok := Parse(broken); ok {
		t.Fatal("accepted a short cursor")
	}
	negative := "v1." + encodeRaw(house+"|stock|-1")
	if _, _, _, ok := Parse(negative); ok {
		t.Fatal("accepted a negative version")
	}
	empty, err := Compare(ctx, db, house, ScopeStock, "  ")
	if err != nil || !empty.Changed || empty.Recognized {
		t.Fatalf("blank since = %+v %v", empty, err)
	}
	future, err := Compare(ctx, db, house, ScopeStock, Cursor(house, ScopeStock, 99))
	if err != nil || !future.Changed || future.Recognized {
		t.Fatalf("future = %+v %v", future, err)
	}
	db.Close()
	if err := Bump(ctx, db, house, ScopeStock); err == nil {
		t.Fatal("closed database recorded a change")
	}
	if _, err := Version(ctx, db, house, ScopeStock); err == nil {
		t.Fatal("closed database returned a version")
	}
	if _, err := Compare(ctx, db, house, ScopeStock, ""); err == nil {
		t.Fatal("closed database compared a cursor")
	}
}

func encodeRaw(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

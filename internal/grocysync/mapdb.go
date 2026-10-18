package grocysync

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

const (
	linkLocation = "location"
	linkUnit     = "quantity_unit"
	linkLabel    = "product_group"
	linkProduct  = "product"
	linkParent   = "parent"
	linkBarcode  = "barcode"
	linkShopList = "shopping_list"
	linkShopItem = "shopping_item"
	linkInstance = "instance"
	linkConsume  = "consume"
)

// Link is one Pantry id mapped to a Grocy id.
// GrocyRef is a stock transaction id when the row is a booking.
// Detail holds extra identity (the stock-in move and the Grocy stock entry).
type Link struct {
	PantryType string
	PantryID   string
	GrocyType  string
	GrocyID    int
	GrocyRef   string
	Hash       string
	Detail     string
}

// Map is the sidecar's id map. It is not Grocy's database.
type Map struct {
	db *sql.DB
}

// OpenMap opens or creates the sqlite file at path.
func OpenMap(path string) (*Map, error) {
	db, err := app.OpenSQLite(path)
	if err != nil {
		return nil, fmt.Errorf("could not open the sync map")
	}
	db.SetMaxOpenConns(1)
	m := &Map{db: db}
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		db.Close()
		return nil, fmt.Errorf("could not open the sync map")
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS links (
		pantry_type TEXT NOT NULL,
		pantry_id   TEXT NOT NULL,
		grocy_type  TEXT NOT NULL,
		grocy_id    INTEGER NOT NULL DEFAULT 0,
		grocy_ref   TEXT NOT NULL DEFAULT '',
		hash        TEXT NOT NULL DEFAULT '',
		detail      TEXT NOT NULL DEFAULT '',
		updated_at  TEXT NOT NULL,
		PRIMARY KEY (pantry_type, pantry_id)
	)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("could not open the sync map")
	}
	return m, nil
}

func (m *Map) Close() error {
	if m == nil || m.db == nil {
		return nil
	}
	return m.db.Close()
}

func (m *Map) Get(typ, id string) (Link, bool, error) {
	var link Link
	err := m.db.QueryRowContext(context.Background(), `
		SELECT pantry_type, pantry_id, grocy_type, grocy_id, grocy_ref, hash, detail
		FROM links WHERE pantry_type = ? AND pantry_id = ?`, typ, id).Scan(
		&link.PantryType, &link.PantryID, &link.GrocyType, &link.GrocyID, &link.GrocyRef, &link.Hash, &link.Detail,
	)
	if err == sql.ErrNoRows {
		return Link{}, false, nil
	}
	if err != nil {
		return Link{}, false, fmt.Errorf("could not read the sync map")
	}
	return link, true, nil
}

func (m *Map) Put(link Link) error {
	_, err := m.db.ExecContext(context.Background(), `
		INSERT INTO links (pantry_type, pantry_id, grocy_type, grocy_id, grocy_ref, hash, detail, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (pantry_type, pantry_id) DO UPDATE SET
			grocy_type = excluded.grocy_type,
			grocy_id = excluded.grocy_id,
			grocy_ref = excluded.grocy_ref,
			hash = excluded.hash,
			detail = excluded.detail,
			updated_at = excluded.updated_at`,
		link.PantryType, link.PantryID, link.GrocyType, link.GrocyID, link.GrocyRef, link.Hash, link.Detail,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("could not write the sync map")
	}
	return nil
}

func (m *Map) Delete(typ, id string) error {
	_, err := m.db.ExecContext(context.Background(), `DELETE FROM links WHERE pantry_type = ? AND pantry_id = ?`, typ, id)
	if err != nil {
		return fmt.Errorf("could not write the sync map")
	}
	return nil
}

func (m *Map) ByType(typ string) ([]Link, error) {
	rows, err := m.db.QueryContext(context.Background(), `
		SELECT pantry_type, pantry_id, grocy_type, grocy_id, grocy_ref, hash, detail
		FROM links WHERE pantry_type = ? ORDER BY pantry_id`, typ)
	if err != nil {
		return nil, fmt.Errorf("could not read the sync map")
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		var link Link
		if err := rows.Scan(&link.PantryType, &link.PantryID, &link.GrocyType, &link.GrocyID, &link.GrocyRef, &link.Hash, &link.Detail); err != nil {
			return nil, fmt.Errorf("could not read the sync map")
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

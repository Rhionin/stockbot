package server

import (
	"context"
	"database/sql"

	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/go-json-experiment/json"
)

// changeView is a list response. With no since cursor it encodes as the
// collection itself, which is the body existing clients already read.
// With a cursor it is the changes-since object.
type changeView struct {
	Since   string
	Cursor  string
	Changed bool
	Key     string
	Items   any
}

func (v changeView) NoStore() bool {
	return v.Since != ""
}

func (v changeView) RawJSON() ([]byte, error) {
	if v.Since == "" {
		return json.Marshal(v.Items)
	}
	if !v.Changed {
		return json.Marshal(struct {
			Cursor  string `json:"cursor"`
			Changed bool   `json:"changed"`
		}{Cursor: v.Cursor, Changed: false})
	}
	items := v.Items
	if items == nil {
		items = []any{}
	}
	return json.Marshal(map[string]any{
		"cursor":  v.Cursor,
		"changed": true,
		v.Key:     items,
	})
}

func viewChanges(ctx context.Context, db *sql.DB, household, scope, since, key string, items any) (changeView, error) {
	view := changeView{Since: since, Key: key, Items: items, Changed: true}
	if since == "" || db == nil {
		view.Since = ""
		return view, nil
	}
	out, err := changes.Compare(ctx, db, household, scope, since)
	if err != nil {
		return changeView{}, err
	}
	view.Cursor = out.Cursor
	view.Changed = out.Changed
	if !out.Changed {
		view.Items = nil
	}
	return view, nil
}

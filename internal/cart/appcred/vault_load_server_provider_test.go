package appcred

import (
	"testing"
)

func TestLoadServerProviderStartupRules(t *testing.T) {
	type tc struct {
		name string
		run  func(t *testing.T)
	}
	setup := func(t *testing.T, opInB bool) (*Vault, func(string, string, string, string), string, string) {
		vault, db, _, scopeA, scopeB := newTestVault(t)
		op := "01909999-9999-7000-8000-0000000000op"
		if _, err := db.Exec(`INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at) VALUES (?, 'Op', 'op@p.test', 'h', 'local', 1, '2026-02-02T00:00:00Z')`, op); err != nil {
			t.Fatal(err)
		}
		if opInB {
			if _, err := db.Exec(`INSERT INTO memberships (user_id, household_id, role, created_at) VALUES (?, ?, 'owner', '2026-02-02T00:00:00Z')`, op, scopeB.HouseholdID()); err != nil {
				t.Fatal(err)
			}
		}
		ins := func(hh, actor, cid, ts string) {
			var a any = actor
			if actor == "<nil>" {
				a = nil
			}
			if _, err := db.Exec(`INSERT INTO provider_app_credentials (id, household_id, user_id, actor_user_id, provider_id, client_id, client_secret, redirect_uri, modality, updated_at) VALUES (?, ?, ?, ?, 'kroger', ?, 's', 'https://x.example/cb', 'PICKUP', ?)`, hh+":kroger", hh, hh, a, cid, ts); err != nil {
				t.Fatal(err)
			}
		}
		var legacy string
		_ = db.QueryRow(`SELECT legacy_user_id FROM households WHERE is_primary=1`).Scan(&legacy)
		return vault, ins, scopeA.HouseholdID(), scopeB.HouseholdID() + "|" + op + "|" + legacy
	}
	split := func(s string) (string, string, string) {
		a := [3]string{}
		i := 0
		for _, c := range s {
			if c == '|' {
				i++
				continue
			}
			a[i] += string(c)
		}
		return a[0], a[1], a[2]
	}
	load := func(v *Vault) string {
		s, _, ok, err := v.LoadServerProvider(t.Context(), "kroger")
		if err != nil {
			return "ERR " + err.Error()
		}
		if !ok {
			return "<none>"
		}
		return s.ClientID
	}
	check := func(t *testing.T, label, got, want string) {
		t.Helper()
		if got != want {
			t.Fatalf("%s: got %q want %q", label, got, want)
		}
	}

	cases := []tc{
		{
			name: "S1 foreign operator-actor row primary empty",
			run: func(t *testing.T) {
				v, ins, _, rest := setup(t, true)
				b, op, _ := split(rest)
				ins(b, op, "FOREIGN-OP", "2026-03-01T00:00:00Z")
				check(t, "S1", load(v), "<none>")
			},
		},
		{
			name: "S2 foreign newer vs primary older M13",
			run: func(t *testing.T) {
				v, ins, a, rest := setup(t, true)
				b, op, _ := split(rest)
				ins(a, op, "PRIMARY", "2026-01-01T00:00:00Z")
				ins(b, op, "FOREIGN-NEWER", "2026-09-01T00:00:00Z")
				check(t, "S2", load(v), "PRIMARY")
			},
		},
		{
			name: "S3 legacy actor on primary op not primary member",
			run: func(t *testing.T) {
				v, ins, a, rest := setup(t, true)
				_, _, legacy := split(rest)
				if legacy == "" {
					t.Fatal("expected legacy_user_id on primary")
				}
				ins(a, legacy, "LEGACY", "2026-01-01T00:00:00Z")
				check(t, "S3", load(v), "LEGACY")
			},
		},
		{
			name: "S4 null actor on primary",
			run: func(t *testing.T) {
				v, ins, a, _ := setup(t, false)
				ins(a, "<nil>", "NULLACTOR", "2026-01-01T00:00:00Z")
				check(t, "S4", load(v), "NULLACTOR")
			},
		},
		{
			name: "S5 null-actor foreign row",
			run: func(t *testing.T) {
				v, ins, _, rest := setup(t, true)
				b, _, _ := split(rest)
				ins(b, "<nil>", "FOREIGN-NULL", "2026-03-01T00:00:00Z")
				check(t, "S5", load(v), "<none>")
			},
		},
		{
			name: "S6 foreign legacy-shaped row only",
			run: func(t *testing.T) {
				v, ins, _, rest := setup(t, true)
				b, _, legacy := split(rest)
				if err := dbExecHouseholdLegacy(t, v, b, legacy); err != nil {
					t.Fatal(err)
				}
				ins(b, legacy, "FOREIGN-LEGACY", "2026-03-01T00:00:00Z")
				check(t, "S6", load(v), "<none>")
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, c.run)
	}
}

func dbExecHouseholdLegacy(t *testing.T, v *Vault, householdID, legacy string) error {
	t.Helper()
	if v == nil || v.db == nil {
		t.Fatal("vault db")
	}
	_, err := v.db.Exec(`UPDATE households SET legacy_user_id = ? WHERE id = ?`, legacy, householdID)
	return err
}

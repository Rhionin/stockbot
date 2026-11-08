package app

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration029FreshAndIdempotent(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := RunMigrations(conn); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}
	assertFull029State(t, conn)

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 39 {
		t.Fatalf("schema_migrations after fresh migrations: got %d want 39", count)
	}
	masterAfterFirst, err := snapshotSqliteMaster(conn)
	if err != nil {
		t.Fatal(err)
	}

	if err := RunMigrations(conn); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 39 {
		t.Fatalf("schema_migrations after repeat: got %d want 39", count)
	}
	masterAfterSecond, err := snapshotSqliteMaster(conn)
	if err != nil {
		t.Fatal(err)
	}
	if masterAfterFirst != masterAfterSecond {
		t.Fatal("schema changed on repeat RunMigrations")
	}
}

func TestMigration029AdditiveUpgradeOn030Database(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := RunMigrationsThrough(conn, firmwareOTAMigration); err != nil {
		t.Fatal(err)
	}
	if err := applyEmbeddedMigrationTx(t, conn, multiStoreMigration); err != nil {
		t.Fatal(err)
	}

	var stores int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM stores`).Scan(&stores); err != nil {
		t.Fatal(err)
	}
	if stores != 0 {
		t.Fatalf("stores: got %d want 0", stores)
	}

	var deviceID string
	if err := conn.QueryRow(`SELECT id FROM devices LIMIT 1`).Scan(&deviceID); err != nil {
		t.Fatal(err)
	}
	const hubVersion = "1.0.0"
	if _, err := conn.Exec(`UPDATE devices SET hub_version = ? WHERE id = ?`, hubVersion, deviceID); err != nil {
		t.Fatal(err)
	}

	var recorded029 int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = ?`, firmwareOTAMigration).Scan(&recorded029); err != nil {
		t.Fatal(err)
	}
	if recorded029 != 0 {
		t.Fatal("029 must be unrecorded in this fixture")
	}

	if err := RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations with 030-only gap: %v", err)
	}
	assertFull029State(t, conn)
	assertFull030State(t, conn)

	var hub string
	var fwChannel sql.NullString
	var fwTarget, otaState sql.NullString
	if err := conn.QueryRow(`SELECT hub_version, fw_channel, fw_target, ota_state FROM devices WHERE id = ?`, deviceID).
		Scan(&hub, &fwChannel, &fwTarget, &otaState); err != nil {
		t.Fatal(err)
	}
	if hub != hubVersion {
		t.Fatalf("hub_version: got %q want %q", hub, hubVersion)
	}
	if !fwChannel.Valid || fwChannel.String != "stable" {
		t.Fatalf("fw_channel: got %v want stable", fwChannel)
	}
	if fwTarget.Valid || otaState.Valid {
		t.Fatalf("fw_target and ota_state should be null, got %v %v", fwTarget, otaState)
	}

	var autoUpdate int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM household_settings WHERE key = 'fw_auto_update'`).Scan(&autoUpdate); err != nil {
		t.Fatal(err)
	}
	if autoUpdate != 0 {
		t.Fatal("household_settings must not gain fw_auto_update from 029")
	}
}

func TestMigration029DefaultsAndConstraints(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := RunMigrations(conn); err != nil {
		t.Fatal(err)
	}

	var household string
	if err := conn.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&household); err != nil {
		t.Fatal(err)
	}
	const devID = "dev-029-constraints"
	if _, err := conn.Exec(`INSERT INTO devices (id, household_id, kind, label, created_at) VALUES (?, ?, 'esp32_scanner', 'constraint probe', '2026-01-01T00:00:00Z')`,
		devID, household); err != nil {
		t.Fatal(err)
	}
	var fwChannel string
	if err := conn.QueryRow(`SELECT fw_channel FROM devices WHERE id = ?`, devID).Scan(&fwChannel); err != nil {
		t.Fatal(err)
	}
	if fwChannel != "stable" {
		t.Fatalf("omitted fw_channel: got %q want stable", fwChannel)
	}
	if _, err := conn.Exec(`UPDATE devices SET fw_channel = 'nope' WHERE id = ?`, devID); err == nil {
		t.Fatal("fw_channel check should reject nope")
	}

	const releaseSQL = `INSERT INTO firmware_releases (id, target, version, size, sha256, key_id, sig, path, created_at)
		VALUES (?, ?, ?, 1, 'deadbeef', 'key1', 'sig1', '/data/firmware/x.bin', '2026-01-01T00:00:00Z')`
	if _, err := conn.Exec(releaseSQL, "fwr-1", "esp32", "1.0.1"); err != nil {
		t.Fatal(err)
	}
	var product, channel string
	if err := conn.QueryRow(`SELECT product, channel FROM firmware_releases WHERE id = 'fwr-1'`).Scan(&product, &channel); err != nil {
		t.Fatal(err)
	}
	if product != "esp32_scanner" || channel != "beta" {
		t.Fatalf("release defaults: product=%q channel=%q", product, channel)
	}
	if _, err := conn.Exec(releaseSQL, "fwr-2", "esp32", "1.0.1"); err == nil {
		t.Fatal("duplicate (target, version) should fail")
	}
}

func TestMigration029RollsBackOnFailure(t *testing.T) {
	cases := []struct {
		name              string
		fileDB            bool
		assertAfterALTERs bool
		prepare           func(t *testing.T, conn *sql.DB)
	}{
		{
			name: "mid migration in memory",
			prepare: func(t *testing.T, conn *sql.DB) {
				t.Helper()
				inject029FailAfter(t, "CREATE TABLE firmware_releases")
			},
		},
		{
			name:              "after last ALTER in memory",
			assertAfterALTERs: true,
			prepare: func(t *testing.T, conn *sql.DB) {
				t.Helper()
				inject029FailAfter(t, "ALTER TABLE devices ADD COLUMN ota_retry_release_id TEXT")
			},
		},
		{
			name: "schema_migrations insert in memory",
			prepare: func(t *testing.T, conn *sql.DB) {
				t.Helper()
				if _, err := conn.Exec(`
					CREATE TRIGGER migration029_block_version_insert
					BEFORE INSERT ON schema_migrations
					WHEN NEW.filename = '` + firmwareOTAMigration + `'
					BEGIN
						SELECT RAISE(ABORT, 'inject migration029 version insert failure');
					END`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:              "after last ALTER on WAL file database",
			fileDB:            true,
			assertAfterALTERs: true,
			prepare: func(t *testing.T, conn *sql.DB) {
				t.Helper()
				inject029FailAfter(t, "ALTER TABLE devices ADD COLUMN ota_retry_release_id TEXT")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, cleanup := openPre029Database(t, tc.fileDB)
			defer cleanup()
			baseline, err := snapshotSqliteMaster(conn)
			if err != nil {
				t.Fatal(err)
			}
			rowCountsBefore, err := snapshotTableRowCounts(conn)
			if err != nil {
				t.Fatal(err)
			}
			var recorded int
			if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = ?`, firmwareOTAMigration).Scan(&recorded); err != nil {
				t.Fatal(err)
			}
			if recorded != 0 {
				t.Fatal("baseline must be pre-029")
			}
			tc.prepare(t, conn)
			if err := RunMigrations(conn); err == nil {
				t.Fatal("expected migration 029 to fail")
			}
			cleanupMigration029TestArtifacts(t, conn)
			got, err := snapshotSqliteMaster(conn)
			if err != nil {
				t.Fatal(err)
			}
			if got != baseline {
				t.Fatalf("sqlite_master changed after rolled-back 029\nwant:\n%s\ngot:\n%s", baseline, got)
			}
			rowCountsAfter, err := snapshotTableRowCounts(conn)
			if err != nil {
				t.Fatal(err)
			}
			assertTableRowCountsEqual(t, rowCountsBefore, rowCountsAfter)
			if tc.assertAfterALTERs {
				assert029SchemaAbsent(t, conn)
			}
			if err := RunMigrations(conn); err != nil {
				t.Fatalf("rerun after rollback: %v", err)
			}
			assertFull029State(t, conn)
		})
	}
}

func openPre029Database(t *testing.T, file bool) (*sql.DB, func()) {
	t.Helper()
	if file {
		path := filepath.Join(t.TempDir(), "pantry.db")
		writer, reader, err := OpenPantry(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := RunMigrationsThrough(writer, firmwareOTAMigration); err != nil {
			t.Fatal(err)
		}
		return writer, func() {
			writer.Close()
			reader.Close()
		}
	}
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsThrough(conn, firmwareOTAMigration); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	return conn, func() { conn.Close() }
}

func applyEmbeddedMigrationTx(t *testing.T, conn *sql.DB, name string) error {
	t.Helper()
	sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
	if err != nil {
		return err
	}
	return applyMigrationTx(conn, name, prepareMigrationSQL(name, string(sqlBytes)))
}

func inject029FailAfter(t *testing.T, marker string) {
	t.Helper()
	orig := prepareMigrationSQL
	t.Cleanup(func() { prepareMigrationSQL = orig })
	prepareMigrationSQL = func(name, sqlText string) string {
		if name != firmwareOTAMigration {
			return sqlText
		}
		idx := strings.Index(sqlText, marker)
		if idx < 0 {
			t.Fatalf("029 migration missing marker %q", marker)
		}
		end := idx + len(marker)
		return sqlText[:end] + ";\nCREATE TABLE migration029_fail_probe (x TEXT, x TEXT);\n" + sqlText[end:]
	}
}

func cleanupMigration029TestArtifacts(t *testing.T, conn *sql.DB) {
	t.Helper()
	prepareMigrationSQL = func(_ string, sqlText string) string { return sqlText }
	if _, err := conn.Exec(`DROP TRIGGER IF EXISTS migration029_block_version_insert`); err != nil {
		t.Fatal(err)
	}
}

func assert029SchemaAbsent(t *testing.T, conn *sql.DB) {
	t.Helper()
	var one int
	err := conn.QueryRow(`SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'firmware_releases'`).Scan(&one)
	if err == nil {
		t.Fatal("firmware_releases still present after rollback")
	}
	for _, col := range columnsAddedBy029 {
		gotCols, err := tableColumns(conn, "devices")
		if err != nil {
			t.Fatal(err)
		}
		if gotCols[strings.ToLower(col)] {
			t.Fatalf("029 column devices.%s still present after rollback", col)
		}
	}
}

func assertFull029State(t *testing.T, conn *sql.DB) {
	t.Helper()
	var one int
	if err := conn.QueryRow(`SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'firmware_releases'`).Scan(&one); err != nil {
		t.Fatalf("firmware_releases missing: %v", err)
	}
	gotCols, err := tableColumns(conn, "devices")
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range columnsAddedBy029 {
		if !gotCols[strings.ToLower(col)] {
			t.Fatalf("029 column devices.%s missing", col)
		}
	}
	var recorded int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = ?`, firmwareOTAMigration).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 1 {
		t.Fatalf("want 029 recorded after successful migration, got %d", recorded)
	}
}

var columnsAddedBy029 = []string{
	"fw_target",
	"fw_channel",
	"fw_pinned_release_id",
	"ota_state",
	"ota_release_id",
	"ota_last_error",
	"ota_updated_at",
	"ota_retry_release_id",
}

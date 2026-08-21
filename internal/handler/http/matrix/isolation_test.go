package matrix_test

import (
	"fmt"
	"testing"
)

func tenantOwnedTables(t *testing.T, s *server) []string {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(), `
		SELECT table_name
		FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name = 'tenant_id'
		ORDER BY table_name`)
	if err != nil {
		t.Fatalf("list tenant-owned tables: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list tenant-owned tables: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no table carries a tenant_id, so this guard is checking nothing")
	}
	return out
}

func TestEveryTenantOwnedTableCascadesFromTheTenant(t *testing.T) {
	s := newServer(t)

	for _, table := range tenantOwnedTables(t, s) {
		var count int
		err := s.db.QueryRowContext(t.Context(), `
			WITH RECURSIVE covered(oid) AS (
				SELECT 'tenants'::regclass::oid
				UNION
				SELECT fk.conrelid
				FROM pg_constraint fk
				JOIN covered ON fk.confrelid = covered.oid
				WHERE fk.contype = 'f' AND fk.confdeltype = 'c'
			)
			SELECT count(*) FROM covered WHERE oid = $1::regclass::oid`, table).Scan(&count)
		if err != nil {
			t.Fatalf("check cascade on %s: %v", table, err)
		}
		if count == 0 {
			t.Fatalf("no cascade reaches %s from tenants, so deleting a tenant would leave it behind", table)
		}
	}
}

func TestEveryTenantOwnedTableCarriesAnIndexOnTheTenant(t *testing.T) {
	s := newServer(t)

	for _, table := range tenantOwnedTables(t, s) {
		var count int
		err := s.db.QueryRowContext(t.Context(), `
			SELECT count(*)
			FROM pg_index i
			JOIN pg_class c ON c.oid = i.indrelid
			JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = i.indkey[0]
			WHERE c.relname = $1 AND a.attname = 'tenant_id'`, table).Scan(&count)
		if err != nil {
			t.Fatalf("check index on %s: %v", table, err)
		}
		if count == 0 {
			t.Fatalf("%s has no index leading with tenant_id, so purging a tenant scans the whole table", table)
		}
	}
}

var globalTables = map[string]bool{
	"goose_db_version":   true,
	"stream_positions":   true,
	"event_types":        true,
	"event_state_keys":   true,
	"sync_state_configs": true,
}

func (s *server) allTables(t *testing.T) []string {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(), `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
		ORDER BY table_name`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		if !globalTables[name] {
			out = append(out, name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list tables: %v", err)
	}
	return out
}

func (s *server) rowCount(t *testing.T, table string) int {
	t.Helper()
	var count int
	query := fmt.Sprintf("SELECT count(*) FROM %s", table)
	if err := s.db.QueryRowContext(t.Context(), query).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func TestDeletingADomainLeavesNothingOfItBehind(t *testing.T) {
	s := newServer(t)
	doomed := s.tenant(t, "alpha.test", "matrix.alpha.test")

	s.token(t, doomed, "@someone:alpha.test")
	if _, err := s.tenants.RotateKey(t.Context(), doomed.Scope()); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}
	resident := s.seedIdentity(t, doomed)
	s.seedRoom(t, doomed, resident)

	tables := s.allTables(t)
	for _, table := range tables {
		if s.rowCount(t, table) == 0 {
			t.Fatalf("%s is empty before the deletion, so it proves nothing", table)
		}
	}

	if err := s.tenants.Delete(t.Context(), doomed.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	for _, table := range tables {
		if n := s.rowCount(t, table); n != 0 {
			t.Fatalf("%s still holds %d rows after its domain was deleted", table, n)
		}
	}

	if _, err := s.tenants.ByServerName(t.Context(), "alpha.test"); err == nil {
		t.Fatal("the deleted tenant still resolves by server name")
	}
	if rec := s.get(t, "matrix.alpha.test", "/_matrix/key/v2/server", ""); rec.Code != 404 {
		t.Fatalf("a host of the deleted tenant still resolves: %d", rec.Code)
	}
}

func TestDeletingOneDomainLeavesTheOtherIntact(t *testing.T) {
	s := newServer(t)
	doomed := s.tenant(t, "alpha.test")
	survivor := s.tenant(t, "beta.test")

	s.token(t, doomed, "@someone:alpha.test")
	s.token(t, survivor, "@someone:beta.test")
	doomedResident := s.seedIdentity(t, doomed)
	survivingResident := s.seedIdentity(t, survivor)
	s.seedRoom(t, doomed, doomedResident)
	s.seedRoom(t, survivor, survivingResident)

	if err := s.tenants.Delete(t.Context(), doomed.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	for _, table := range s.allTables(t) {
		if s.rowCount(t, table) == 0 {
			t.Fatalf("deleting one domain emptied %s for the other", table)
		}
	}
}

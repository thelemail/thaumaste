package matrix_test

import (
	"fmt"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
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

// Every table that names a tenant must be reachable by a cascading delete from tenants. Checking
// this against the live catalogue rather than a hand-kept list is the point: a table added by a
// later task is covered the moment it exists, without anyone remembering to come back here.
func TestEveryTenantOwnedTableCascadesFromTheTenant(t *testing.T) {
	s := newServer(t)

	for _, table := range tenantOwnedTables(t, s) {
		var count int
		err := s.db.QueryRowContext(t.Context(), `
			SELECT count(*)
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu
			  ON kcu.constraint_name = tc.constraint_name
			JOIN information_schema.referential_constraints rc
			  ON rc.constraint_name = tc.constraint_name
			JOIN information_schema.constraint_column_usage ccu
			  ON ccu.constraint_name = tc.constraint_name
			WHERE tc.table_schema = 'public'
			  AND tc.table_name = $1
			  AND tc.constraint_type = 'FOREIGN KEY'
			  AND kcu.column_name = 'tenant_id'
			  AND ccu.table_name = 'tenants'
			  AND rc.delete_rule = 'CASCADE'`, table).Scan(&count)
		if err != nil {
			t.Fatalf("check cascade on %s: %v", table, err)
		}
		if count == 0 {
			t.Fatalf("%s.tenant_id does not cascade from tenants, so deleting a tenant would leave it behind", table)
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

func TestDeletingADomainLeavesNothingOfItBehind(t *testing.T) {
	s := newServer(t)
	doomed := s.tenant(t, "alpha.test", "matrix.alpha.test")
	survivor := s.tenant(t, "beta.test")

	s.token(t, doomed, "@someone:alpha.test")
	s.token(t, survivor, "@someone:beta.test")
	if _, err := s.tenants.RotateKey(t.Context(), doomed.Scope()); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}

	tables := tenantOwnedTables(t, s)
	for _, table := range tables {
		if rows := s.countFor(t, table, doomed); rows == 0 {
			t.Fatalf("%s holds nothing for the tenant about to be deleted, so this proves nothing", table)
		}
	}

	if err := s.tenants.Delete(t.Context(), doomed.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	for _, table := range tables {
		if rows := s.countFor(t, table, doomed); rows != 0 {
			t.Fatalf("%s still holds %d rows for the deleted tenant", table, rows)
		}
		if rows := s.countFor(t, table, survivor); rows == 0 {
			t.Fatalf("deleting one tenant emptied %s for the other", table)
		}
	}

	if _, err := s.tenants.ByServerName(t.Context(), "alpha.test"); err == nil {
		t.Fatal("the deleted tenant still resolves by server name")
	}
	if rec := s.get(t, "matrix.alpha.test", "/_matrix/key/v2/server", ""); rec.Code != 404 {
		t.Fatalf("a host of the deleted tenant still resolves: %d", rec.Code)
	}
}

func (s *server) countFor(t *testing.T, table string, of entity.Tenant) int {
	t.Helper()
	var count int
	query := fmt.Sprintf("SELECT count(*) FROM %s WHERE tenant_id = $1", table)
	if err := s.db.QueryRowContext(t.Context(), query, of.ID.String()).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

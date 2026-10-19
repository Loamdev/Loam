package db

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// testDBEnv names the environment variable that points the integration tests at
// a live Postgres holding the testapp/ fixture schema.
//
// Bring one up with:
//
//	loam up --dir testapp
//	LOAM_TEST_DB=postgres://appuser:apppass@localhost:5433/appdb go test ./internal/db/...
//
// Without it these tests skip, so `go test ./...` stays runnable with no Docker.
const testDBEnv = "LOAM_TEST_DB"

// liveSchema connects to the fixture database and introspects it, or skips.
func liveSchema(t *testing.T) (*Schema, func()) {
	t.Helper()

	url := os.Getenv(testDBEnv)
	if url == "" {
		t.Skipf("%s is not set; skipping the live introspection test", testDBEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	pool, err := Connect(ctx, url, ReadyTimeout)
	if err != nil {
		cancel()
		t.Fatalf("connect to the fixture database: %v", err)
	}

	schema, err := Introspect(ctx, pool)
	if err != nil {
		pool.Close()
		cancel()
		t.Fatalf("Introspect: %v", err)
	}
	return schema, func() { pool.Close(); cancel() }
}

func TestIntrospectFixtureTables(t *testing.T) {
	schema, done := liveSchema(t)
	defer done()

	want := []string{"categories", "employees", "order_items", "orders", "products", "promotions", "tenant_events", "tenant_users", "users"}
	got := schema.SortedTableNames()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tables = %v, want %v", got, want)
	}
}

func TestIntrospectFixtureColumnTypes(t *testing.T) {
	schema, done := liveSchema(t)
	defer done()

	// Every branch of the type normalizer that the fixture is built to exercise.
	tests := []struct {
		table, column string
		wantType      string
		wantNullable  bool
		wantMaxLength int
	}{
		{"users", "id", TypeBigint, false, 0},
		{"users", "email", TypeVarchar, false, 255},
		{"users", "pincode", TypeVarchar, true, 6},
		{"users", "status", TypeEnum, false, 0},
		{"users", "is_verified", TypeBool, false, 0},
		{"users", "bio", TypeText, true, 0},
		{"users", "created_at", TypeTimestamptz, false, 0},
		{"users", "updated_at", TypeTimestamptz, true, 0},
		{"employees", "manager_id", TypeBigint, true, 0},
		{"employees", "hired_on", TypeDate, true, 0},
		{"employees", "last_synced_at", TypeTimestamp, true, 0},
		{"products", "price", TypeNumeric, false, 0},
		{"products", "metadata", TypeJSONB, true, 0},
		{"products", "in_stock", TypeBool, true, 0},
		{"products", "category_id", TypeSmallint, true, 0},
		{"promotions", "id", TypeInt, false, 0},
		{"promotions", "starts_on", TypeDate, false, 0},
		{"promotions", "created_at", TypeTimestamp, true, 0},
		{"order_items", "id", TypeUUID, false, 0},
		{"order_items", "qty", TypeInt, false, 0},
		{"categories", "id", TypeSmallint, false, 0},
	}

	for _, tc := range tests {
		t.Run(tc.table+"."+tc.column, func(t *testing.T) {
			tbl, ok := schema.Tables[tc.table]
			if !ok {
				t.Fatalf("table %q missing", tc.table)
			}
			c, ok := tbl.Column(tc.column)
			if !ok {
				t.Fatalf("column %s.%s missing", tc.table, tc.column)
			}
			if c.DataType != tc.wantType {
				t.Errorf("DataType = %q, want %q", c.DataType, tc.wantType)
			}
			if c.Nullable != tc.wantNullable {
				t.Errorf("Nullable = %v, want %v", c.Nullable, tc.wantNullable)
			}
			if c.MaxLength != tc.wantMaxLength {
				t.Errorf("MaxLength = %d, want %d", c.MaxLength, tc.wantMaxLength)
			}
		})
	}
}

func TestIntrospectFixturePrimaryKeys(t *testing.T) {
	schema, done := liveSchema(t)
	defer done()

	// tenant_users is deliberately excluded: its key is composite and checked below.
	for table, want := range map[string]string{
		"users": "id", "orders": "id", "order_items": "id",
		"products": "id", "categories": "id", "promotions": "id", "employees": "id",
	} {
		tbl := schema.Tables[table]
		if tbl == nil {
			t.Errorf("table %q missing", table)
			continue
		}
		if strings.Join(tbl.PrimaryKey, ",") != want {
			t.Errorf("%s primary key = %v, want [%s]", table, tbl.PrimaryKey, want)
		}
		c, ok := tbl.Column(want)
		if !ok || !c.IsPrimaryKey {
			t.Errorf("%s.%s is not marked IsPrimaryKey", table, want)
		}
		// Every fixture PK is serial or has a default, which the seeder needs
		// to know so it can supply its own value instead.
		if !c.HasDefault {
			t.Errorf("%s.%s HasDefault = false, want true (serial/uuid default)", table, want)
		}
	}
}

func TestIntrospectFixtureCompositePrimaryKey(t *testing.T) {
	schema, done := liveSchema(t)
	defer done()

	tbl := schema.Tables["tenant_users"]
	if tbl == nil {
		t.Fatal("tenant_users missing")
	}
	if strings.Join(tbl.PrimaryKey, ",") != "tenant_id,user_ref" {
		t.Errorf("primary key = %v, want [tenant_id user_ref] in key order", tbl.PrimaryKey)
	}
	for _, name := range []string{"tenant_id", "user_ref"} {
		c, ok := tbl.Column(name)
		if !ok || !c.IsPrimaryKey {
			t.Errorf("%s is not marked IsPrimaryKey", name)
		}
	}
}

func TestIntrospectFixtureForeignKeys(t *testing.T) {
	schema, done := liveSchema(t)
	defer done()

	// child.column -> parent.column
	want := map[string]string{
		"employees.manager_id":    "employees.id",
		"tenant_events.tenant_id": "tenant_users.tenant_id",
		"tenant_events.user_ref":  "tenant_users.user_ref",
		"orders.user_id":          "users.id",
		"order_items.order_id":    "orders.id",
		"order_items.product_id":  "products.id",
		"products.category_id":    "categories.id",
		"promotions.category_id":  "categories.id",
	}

	got := map[string]string{}
	for _, table := range schema.SortedTableNames() {
		for _, fk := range schema.Tables[table].FKs {
			got[table+"."+fk.ChildColumn] = fk.ParentTable + "." + fk.ParentColumn
		}
	}

	if len(got) != len(want) {
		t.Errorf("found %d foreign keys, want %d\n got: %v\nwant: %v", len(got), len(want), got, want)
	}
	for child, parent := range want {
		if got[child] != parent {
			t.Errorf("%s references %q, want %q", child, got[child], parent)
		}
	}
	if schema.FKCount() != len(want) {
		t.Errorf("FKCount() = %d, want %d", schema.FKCount(), len(want))
	}

	// The self-reference must be recorded, not dropped.
	if !schema.Tables["employees"].SelfReferencing() {
		t.Error("employees.manager_id -> employees.id was not recorded as a self-reference")
	}

	// The composite key must group into ONE constraint. Seen as two independent
	// references, its columns get drawn separately and the pairs do not exist.
	groups := schema.Tables["tenant_events"].FKGroups()
	if len(groups) != 1 {
		t.Fatalf("tenant_events has %d foreign key groups, want 1 composite", len(groups))
	}
	if !groups[0].Composite() {
		t.Errorf("tenant_events key was not recognized as composite: %+v", groups[0])
	}
	if strings.Join(groups[0].ChildColumns, ",") != "tenant_id,user_ref" {
		t.Errorf("child columns = %v, want [tenant_id user_ref] in constraint order", groups[0].ChildColumns)
	}
	if strings.Join(groups[0].ParentColumns, ",") != "tenant_id,user_ref" {
		t.Errorf("parent columns = %v, want [tenant_id user_ref]", groups[0].ParentColumns)
	}
}

func TestIntrospectFixtureAllowedValues(t *testing.T) {
	url := os.Getenv(testDBEnv)
	if url == "" {
		t.Skipf("%s is not set; skipping", testDBEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := Connect(ctx, url, ReadyTimeout)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	schema, err := Introspect(ctx, pool)
	if err != nil {
		t.Fatalf("Introspect: %v", err)
	}

	allowed, err := AllowedValues(ctx, pool, schema)
	if err != nil {
		t.Fatalf("AllowedValues: %v", err)
	}

	// users.status is a real enum type.
	assertValues(t, allowed, "users.status", []string{"active", "churned", "suspended", "trial"})
	// orders.status is a CHECK (status IN (...)) - a different code path.
	assertValues(t, allowed, "orders.status", []string{"cancelled", "paid", "pending", "refunded", "shipped"})
}

// assertValues compares an allowed-value set irrespective of order.
func assertValues(t *testing.T, allowed map[string][]string, key string, want []string) {
	t.Helper()

	got, ok := allowed[key]
	if !ok {
		t.Errorf("%s has no allowed values; the seeder would fall back to a generic profile", key)
		return
	}
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	if strings.Join(sorted, ",") != strings.Join(want, ",") {
		t.Errorf("%s allowed values = %v, want %v", key, sorted, want)
	}
}

func TestTopoSortFixtureOrdersParentsFirst(t *testing.T) {
	schema, done := liveSchema(t)
	defer done()

	warnings, err := TopoSort(schema)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none: the fixture schema is acyclic", warnings)
	}

	position := make(map[string]int, len(schema.Order))
	for i, name := range schema.Order {
		position[name] = i
	}
	if len(schema.Order) != len(schema.Tables) {
		t.Fatalf("order has %d entries for %d tables: %v", len(schema.Order), len(schema.Tables), schema.Order)
	}

	// The build spec's phase 4 acceptance condition, on the real schema.
	for _, name := range schema.SortedTableNames() {
		for _, fk := range schema.Tables[name].FKs {
			if fk.ParentTable == name {
				continue // the employees self-reference
			}
			if position[fk.ParentTable] >= position[name] {
				t.Errorf("%s references %s but comes before it\norder: %v", name, fk.ParentTable, schema.Order)
			}
		}
	}
	t.Logf("fixture order: %v", schema.Order)
}

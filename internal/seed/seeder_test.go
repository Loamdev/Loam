package seed

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/shivansh-source/stateful-playwright/internal/db"
)

// chainSchema is the fixture-shaped schema: a lookup table three levels up, a
// two-level order chain, and a self-referencing table.
func chainSchema() *db.Schema {
	s := db.NewSchema()

	add := func(name string, pkType string, columns []db.Column, fks []db.FK) {
		cols := append([]db.Column{{Name: "id", DataType: pkType, IsPrimaryKey: true, HasDefault: true}}, columns...)
		s.Tables[name] = &db.Table{Name: name, Columns: cols, FKs: fks, PrimaryKey: []string{"id"}}
	}

	add("categories", db.TypeSmallint, []db.Column{{Name: "name", DataType: db.TypeVarchar, MaxLength: 60}}, nil)
	add("users", db.TypeBigint, []db.Column{{Name: "email", DataType: db.TypeVarchar, MaxLength: 255}}, nil)
	add("products", db.TypeBigint,
		[]db.Column{{Name: "category_id", DataType: db.TypeSmallint, Nullable: true}},
		[]db.FK{{ChildColumn: "category_id", ParentTable: "categories", ParentColumn: "id"}})
	add("orders", db.TypeBigint,
		[]db.Column{{Name: "user_id", DataType: db.TypeBigint}},
		[]db.FK{{ChildColumn: "user_id", ParentTable: "users", ParentColumn: "id"}})
	add("order_items", db.TypeUUID,
		[]db.Column{
			{Name: "order_id", DataType: db.TypeBigint},
			{Name: "product_id", DataType: db.TypeBigint, Nullable: true},
		},
		[]db.FK{
			{ChildColumn: "order_id", ParentTable: "orders", ParentColumn: "id"},
			{ChildColumn: "product_id", ParentTable: "products", ParentColumn: "id"},
		})
	add("employees", db.TypeBigint,
		[]db.Column{{Name: "manager_id", DataType: db.TypeBigint, Nullable: true}},
		[]db.FK{{ChildColumn: "manager_id", ParentTable: "employees", ParentColumn: "id"}})

	return s
}

func TestRowCountForScalesDownParents(t *testing.T) {
	s := chainSchema()
	opts := Options{DefaultRows: 10000}

	// order_items is a leaf: it gets the full count. orders and products are one
	// level up, users and categories two. Ten thousand categories would be noise.
	tests := map[string]int{
		"order_items": 10000,
		"orders":      2000,
		"products":    2000,
		"users":       400,
		"categories":  400,
		"employees":   10000, // a self-reference does not make it a parent
	}

	for table, want := range tests {
		if got := RowCountFor(s, table, opts); got != want {
			t.Errorf("RowCountFor(%s) = %d, want %d", table, got, want)
		}
	}
}

func TestRowCountForHonoursExplicitOverrides(t *testing.T) {
	s := chainSchema()
	opts := Options{DefaultRows: 10000, TableRows: map[string]int{"users": 2000, "orders": 20000}}

	if got := RowCountFor(s, "users", opts); got != 2000 {
		t.Errorf("RowCountFor(users) = %d, want the configured 2000", got)
	}
	if got := RowCountFor(s, "orders", opts); got != 20000 {
		t.Errorf("RowCountFor(orders) = %d, want the configured 20000", got)
	}
	// Unconfigured tables still follow the heuristic.
	if got := RowCountFor(s, "order_items", opts); got != 10000 {
		t.Errorf("RowCountFor(order_items) = %d, want 10000", got)
	}
}

func TestRowCountForNeverGoesBelowTheFloor(t *testing.T) {
	s := chainSchema()
	// A tiny default would otherwise divide a deep parent down to nothing,
	// leaving children with no keys to reference.
	opts := Options{DefaultRows: 10}

	for _, table := range s.SortedTableNames() {
		if got := RowCountFor(s, table, opts); got < minRows {
			t.Errorf("RowCountFor(%s) = %d, below the floor of %d", table, got, minRows)
		}
	}
}

func TestRowCountForClampsSmallintKeys(t *testing.T) {
	s := chainSchema()
	// A smallserial key cannot exceed int16, so the row count must not either.
	opts := Options{DefaultRows: 10000, TableRows: map[string]int{"categories": 100000}}

	got := RowCountFor(s, "categories", opts)
	if got != smallintMaxRows {
		t.Errorf("RowCountFor(categories) = %d, want it clamped to %d for a smallint key", got, smallintMaxRows)
	}
}

func TestRowCountForSurvivesACycle(t *testing.T) {
	// The depth walk must not recurse forever on mutually referencing tables.
	s := db.NewSchema()
	s.Tables["a"] = &db.Table{
		Name: "a", PrimaryKey: []string{"id"},
		Columns: []db.Column{{Name: "id", DataType: db.TypeBigint, IsPrimaryKey: true}, {Name: "b_id", Nullable: true}},
		FKs:     []db.FK{{ChildColumn: "b_id", ParentTable: "b", ParentColumn: "id"}},
	}
	s.Tables["b"] = &db.Table{
		Name: "b", PrimaryKey: []string{"id"},
		Columns: []db.Column{{Name: "id", DataType: db.TypeBigint, IsPrimaryKey: true}, {Name: "a_id", Nullable: true}},
		FKs:     []db.FK{{ChildColumn: "a_id", ParentTable: "a", ParentColumn: "id"}},
	}

	done := make(chan int, 1)
	go func() { done <- RowCountFor(s, "a", Options{DefaultRows: 1000}) }()

	select {
	case got := <-done:
		if got <= 0 {
			t.Errorf("RowCountFor = %d, want a positive count", got)
		}
	default:
		// Give the goroutine a chance rather than failing on a race.
		if got := <-done; got <= 0 {
			t.Errorf("RowCountFor = %d, want a positive count", got)
		}
	}
}

func TestReferencedColumns(t *testing.T) {
	s := chainSchema()
	got := referencedColumns(s)

	// Only columns something actually points at need pooling.
	for _, table := range []string{"categories", "users", "products", "orders", "employees"} {
		cols := got[table]
		if len(cols) != 1 || cols[0] != "id" {
			t.Errorf("referencedColumns[%s] = %v, want [id]", table, cols)
		}
	}
	if cols, ok := got["order_items"]; ok {
		t.Errorf("referencedColumns[order_items] = %v, want nothing: no table references it", cols)
	}
}

func TestSeedRequiresAnOrderedSchema(t *testing.T) {
	// Seeding an unordered schema would draw foreign keys from empty pools.
	s := chainSchema()
	_, err := Seed(context.TODO(), nil, s, Options{})
	if err == nil {
		t.Fatal("Seed on an unordered schema returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "TopoSort") {
		t.Errorf("error = %q, want it to point at db.TopoSort", err)
	}
}

func TestSeedWithNothingToDo(t *testing.T) {
	result, err := Seed(context.TODO(), nil, db.NewSchema(), Options{})
	if err != nil {
		t.Fatalf("Seed on an empty schema: %v", err)
	}
	if result.TotalRows != 0 {
		t.Errorf("TotalRows = %d, want 0", result.TotalRows)
	}
}

func TestValidateSeedableRejectsANotNullCycle(t *testing.T) {
	// A deferred foreign key is left empty on the first pass. If the column is
	// NOT NULL that cannot work, and saying so beats a Postgres error.
	s := db.NewSchema()
	s.Tables["left"] = &db.Table{
		Name:       "left",
		Columns:    []db.Column{{Name: "id", IsPrimaryKey: true}, {Name: "right_id", Nullable: false}},
		PrimaryKey: []string{"id"},
		FKs:        []db.FK{{ChildColumn: "right_id", ParentTable: "right", ParentColumn: "id", Deferred: true}},
	}
	s.Tables["right"] = &db.Table{
		Name:       "right",
		Columns:    []db.Column{{Name: "id", IsPrimaryKey: true}},
		PrimaryKey: []string{"id"},
	}

	err := validateSeedable(s)
	if err == nil {
		t.Fatal("validateSeedable accepted a NOT NULL cycle, want an error")
	}
	if !strings.Contains(err.Error(), "NOT NULL") {
		t.Errorf("error = %q, want it to name the problem", err)
	}
}

func TestSeedableColumnsSkipsGeneratedAlwaysIdentity(t *testing.T) {
	g := NewGenerator(Options{Seed: 1})
	t1 := &db.Table{
		Name: "t",
		Columns: []db.Column{
			{Name: "id", DataType: db.TypeBigint, IsPrimaryKey: true, IsIdentity: true, IdentityGeneration: "ALWAYS"},
			{Name: "name", DataType: db.TypeVarchar},
		},
	}

	columns, warnings := seedableColumns(g, t1)
	if len(columns) != 1 || columns[0].Name != "name" {
		t.Errorf("columns = %v, want just [name]: GENERATED ALWAYS rejects a supplied value", columnNames(columns))
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "GENERATED ALWAYS") {
		t.Errorf("warnings = %v, want one explaining the identity column", warnings)
	}
}

func TestSeedableColumnsLeavesUnsupportedTypesToTheDatabase(t *testing.T) {
	g := NewGenerator(Options{Seed: 1})
	t1 := &db.Table{
		Name: "t",
		Columns: []db.Column{
			{Name: "id", DataType: db.TypeBigint, IsPrimaryKey: true},
			{Name: "blob", DataType: db.TypeBytea, Nullable: true},
			{Name: "window", DataType: db.TypeInterval, HasDefault: true},
			{Name: "required_blob", DataType: db.TypeBytea}, // NOT NULL, no default
			{Name: "name", DataType: db.TypeVarchar},
		},
	}

	columns, warnings := seedableColumns(g, t1)
	got := columnNames(columns)
	sort.Strings(got)
	if strings.Join(got, ",") != "id,name" {
		t.Errorf("columns = %v, want [id name]", got)
	}
	if len(warnings) != 3 {
		t.Errorf("warnings = %v, want three: one per skipped column", warnings)
	}

	// The NOT NULL one with no default is the serious case and must say so.
	var sawSerious bool
	for _, w := range warnings {
		if strings.Contains(w, "required_blob") && strings.Contains(w, "NOT NULL") {
			sawSerious = true
		}
	}
	if !sawSerious {
		t.Errorf("warnings = %v, want one calling out required_blob", warnings)
	}
}

func TestSeedableColumnsAlwaysIncludesForeignKeys(t *testing.T) {
	// A foreign key's value comes from a pool, so type support is irrelevant.
	g := NewGenerator(Options{Seed: 1})
	t1 := &db.Table{
		Name: "t",
		Columns: []db.Column{
			{Name: "id", DataType: db.TypeBigint, IsPrimaryKey: true},
			{Name: "odd_ref", DataType: db.TypeUnknown},
		},
		FKs: []db.FK{{ChildColumn: "odd_ref", ParentTable: "other", ParentColumn: "id"}},
	}

	columns, _ := seedableColumns(g, t1)
	if got := columnNames(columns); strings.Join(got, ",") != "id,odd_ref" {
		t.Errorf("columns = %v, want the foreign key included", got)
	}
}

func TestForeignKeyValueNeverInventsAKey(t *testing.T) {
	// The single most important rule (build spec section 12.5).
	g := NewGenerator(Options{Seed: 1})
	t1 := &db.Table{Name: "orders", Columns: []db.Column{{Name: "user_id", DataType: db.TypeBigint}}}
	fk := db.FK{ChildColumn: "user_id", ParentTable: "users", ParentColumn: "id"}

	notNull := db.Column{Name: "user_id", DataType: db.TypeBigint}
	if _, err := g.foreignKeyValue(t1, notNull, fk, map[string][]any{}, nil, 0); err == nil {
		t.Fatal("a NOT NULL foreign key with an empty parent pool returned a value, want an error")
	}

	nullable := db.Column{Name: "user_id", DataType: db.TypeBigint, Nullable: true}
	v, err := g.foreignKeyValue(t1, nullable, fk, map[string][]any{}, nil, 0)
	if err != nil {
		t.Fatalf("a nullable foreign key with an empty pool should be null, got %v", err)
	}
	if v != nil {
		t.Errorf("got %v, want nil", v)
	}

	// With a pool, every value must come from it.
	pools := map[string][]any{"users.id": {int64(7), int64(8), int64(9)}}
	legal := map[any]bool{int64(7): true, int64(8): true, int64(9): true}
	for i := 0; i < 200; i++ {
		v, err := g.foreignKeyValue(t1, notNull, fk, pools, nil, i)
		if err != nil {
			t.Fatalf("draw failed: %v", err)
		}
		if !legal[v] {
			t.Fatalf("drew %v, which is not a generated parent key", v)
		}
	}
}

func TestForeignKeyValueLeavesDeferredReferencesEmpty(t *testing.T) {
	g := NewGenerator(Options{Seed: 1})
	t1 := &db.Table{Name: "a", Columns: []db.Column{{Name: "b_id", Nullable: true}}}
	fk := db.FK{ChildColumn: "b_id", ParentTable: "b", ParentColumn: "id", Deferred: true}

	pools := map[string][]any{"b.id": {int64(1)}}
	v, err := g.foreignKeyValue(t1, db.Column{Name: "b_id", Nullable: true}, fk, pools, nil, 5)
	if err != nil {
		t.Fatalf("deferred draw: %v", err)
	}
	if v != nil {
		t.Errorf("got %v, want nil: a cycle-breaking reference is left empty", v)
	}
}

func TestForeignKeyValueSelfReferenceUsesEarlierRows(t *testing.T) {
	g := NewGenerator(Options{Seed: 1, NullRate: 0.01})
	t1 := &db.Table{Name: "employees", Columns: []db.Column{{Name: "manager_id", DataType: db.TypeBigint, Nullable: true}}}
	fk := db.FK{ChildColumn: "manager_id", ParentTable: "employees", ParentColumn: "id"}
	c := db.Column{Name: "manager_id", DataType: db.TypeBigint, Nullable: true}

	// The first row has nobody to point at.
	v, err := g.foreignKeyValue(t1, c, fk, nil, map[string][]any{}, 0)
	if err != nil || v != nil {
		t.Errorf("first row got (%v, %v), want (nil, nil)", v, err)
	}

	// Later rows may only reference keys already generated in this batch.
	own := map[string][]any{"id": {int64(1), int64(2), int64(3)}}
	legal := map[any]bool{int64(1): true, int64(2): true, int64(3): true}
	for i := 1; i < 200; i++ {
		v, err := g.foreignKeyValue(t1, c, fk, nil, own, i)
		if err != nil {
			t.Fatalf("self draw failed: %v", err)
		}
		if v != nil && !legal[v] {
			t.Fatalf("drew %v, which was not generated earlier in the batch", v)
		}
	}
}

// columnNames extracts names for assertions.
func columnNames(columns []db.Column) []string {
	out := make([]string, len(columns))
	for i, c := range columns {
		out[i] = c.Name
	}
	return out
}

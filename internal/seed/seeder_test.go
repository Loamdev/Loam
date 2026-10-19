package seed

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/loamdev/loam/internal/db"
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

	// A deadline, not a bare receive: if the depth walk ever does recurse
	// through the cycle, this test has to REPORT that rather than hang waiting
	// for a result that never arrives.
	done := make(chan int, 1)
	go func() { done <- RowCountFor(s, "a", Options{DefaultRows: 1000}) }()

	select {
	case got := <-done:
		if got <= 0 {
			t.Errorf("RowCountFor = %d, want a positive count", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RowCountFor did not return within 5s: the depth walk is recursing through the foreign key cycle")
	}
}

func TestReferencedKeys(t *testing.T) {
	s := chainSchema()
	got := referencedKeys(s)

	// Only column sets something actually points at need pooling.
	for _, table := range []string{"categories", "users", "products", "orders", "employees"} {
		sets := got[table]
		if len(sets) != 1 || len(sets[0]) != 1 || sets[0][0] != "id" {
			t.Errorf("referencedKeys[%s] = %v, want [[id]]", table, sets)
		}
	}
	if sets, ok := got["order_items"]; ok {
		t.Errorf("referencedKeys[order_items] = %v, want nothing: no table references it", sets)
	}
}

func TestReferencedKeysGroupsACompositeKey(t *testing.T) {
	// A composite foreign key must pool as ONE column set. Pooling its columns
	// separately is what lets a child draw a pair the parent never had.
	s := compositeSchema()
	got := referencedKeys(s)

	sets := got["tenant_users"]
	if len(sets) != 1 {
		t.Fatalf("referencedKeys[tenant_users] = %v, want a single column set", sets)
	}
	if strings.Join(sets[0], ",") != "tenant_id,user_ref" {
		t.Errorf("column set = %v, want [tenant_id user_ref] in constraint order", sets[0])
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

// compositeSchema has a composite primary key and a composite foreign key
// referencing it, which is what broke before drawFK drew whole tuples.
func compositeSchema() *db.Schema {
	s := db.NewSchema()
	s.Tables["tenant_users"] = &db.Table{
		Name: "tenant_users",
		Columns: []db.Column{
			{Name: "tenant_id", DataType: db.TypeInt, IsPrimaryKey: true},
			{Name: "user_ref", DataType: db.TypeInt, IsPrimaryKey: true},
			{Name: "label", DataType: db.TypeVarchar, MaxLength: 60, Nullable: true},
		},
		PrimaryKey: []string{"tenant_id", "user_ref"},
	}
	s.Tables["tenant_events"] = &db.Table{
		Name: "tenant_events",
		Columns: []db.Column{
			{Name: "id", DataType: db.TypeBigint, IsPrimaryKey: true, HasDefault: true},
			{Name: "tenant_id", DataType: db.TypeInt},
			{Name: "user_ref", DataType: db.TypeInt},
		},
		PrimaryKey: []string{"id"},
		FKs: []db.FK{
			{ConstraintName: "te_fk", ChildColumn: "tenant_id", ParentTable: "tenant_users", ParentColumn: "tenant_id"},
			{ConstraintName: "te_fk", ChildColumn: "user_ref", ParentTable: "tenant_users", ParentColumn: "user_ref"},
		},
	}
	return s
}

func TestDrawFKNeverInventsAKey(t *testing.T) {
	// The single most important rule (build spec section 12.5).
	g := NewGenerator(Options{Seed: 1})
	tbl := &db.Table{
		Name:    "orders",
		Columns: []db.Column{{Name: "user_id", DataType: db.TypeBigint}},
		FKs:     []db.FK{{ConstraintName: "o_fk", ChildColumn: "user_id", ParentTable: "users", ParentColumn: "id"}},
	}
	grp := tbl.FKGroups()[0]
	index := map[string]int{"user_id": 0}

	notNull := []db.Column{{Name: "user_id", DataType: db.TypeBigint}}
	if _, err := g.drawFK(tbl, grp, notNull, index, map[string][][]any{}, nil, 0); err == nil {
		t.Fatal("a NOT NULL foreign key with an empty parent pool returned a value, want an error")
	}

	nullable := []db.Column{{Name: "user_id", DataType: db.TypeBigint, Nullable: true}}
	tuple, err := g.drawFK(tbl, grp, nullable, index, map[string][][]any{}, nil, 0)
	if err != nil {
		t.Fatalf("a nullable foreign key with an empty pool should be null, got %v", err)
	}
	if len(tuple) != 1 || tuple[0] != nil {
		t.Errorf("got %v, want a single nil", tuple)
	}

	// With a pool, every value must come from it.
	pools := map[string][][]any{"users.id": {{int64(7)}, {int64(8)}, {int64(9)}}}
	legal := map[any]bool{int64(7): true, int64(8): true, int64(9): true}
	for i := 0; i < 200; i++ {
		tuple, err := g.drawFK(tbl, grp, notNull, index, pools, nil, i)
		if err != nil {
			t.Fatalf("draw failed: %v", err)
		}
		if !legal[tuple[0]] {
			t.Fatalf("drew %v, which is not a generated parent key", tuple[0])
		}
	}
}

func TestDrawFKDrawsACompositeKeyAsOneTuple(t *testing.T) {
	// Drawing each column independently is exactly the bug: it produces pairs
	// that exist in neither parent row, and Postgres rejects the whole batch.
	g := NewGenerator(Options{Seed: 4})
	s := compositeSchema()
	tbl := s.Tables["tenant_events"]
	grp := tbl.FKGroups()[0]

	if !grp.Composite() {
		t.Fatalf("FKGroups did not recognize a composite key: %+v", grp)
	}

	columns := tbl.Columns
	index := map[string]int{"id": 0, "tenant_id": 1, "user_ref": 2}

	// Only these pairs exist in the parent.
	valid := map[string]bool{"1|10": true, "2|20": true, "3|30": true}
	pools := map[string][][]any{
		"tenant_users.tenant_id,user_ref": {
			{int32(1), int32(10)}, {int32(2), int32(20)}, {int32(3), int32(30)},
		},
	}

	for i := 0; i < 500; i++ {
		tuple, err := g.drawFK(tbl, grp, columns, index, pools, nil, i)
		if err != nil {
			t.Fatalf("draw failed: %v", err)
		}
		if len(tuple) != 2 {
			t.Fatalf("tuple width = %d, want 2", len(tuple))
		}
		key := fmt.Sprintf("%v|%v", tuple[0], tuple[1])
		if !valid[key] {
			t.Fatalf("drew the pair (%v, %v), which no parent row has", tuple[0], tuple[1])
		}
	}
}

func TestDrawFKTreatsAPartlyNullableCompositeKeyAsNotNull(t *testing.T) {
	// Postgres leaves a partially NULL composite key unenforced. Nulling one
	// column would silently drop the relationship, so the whole constraint
	// counts as NOT NULL unless every column is nullable.
	g := NewGenerator(Options{Seed: 2, NullRate: 0.9})
	s := compositeSchema()
	tbl := s.Tables["tenant_events"]
	grp := tbl.FKGroups()[0]

	columns := []db.Column{
		{Name: "id", DataType: db.TypeBigint, IsPrimaryKey: true},
		{Name: "tenant_id", DataType: db.TypeInt, Nullable: true},
		{Name: "user_ref", DataType: db.TypeInt}, // NOT NULL
	}
	index := map[string]int{"id": 0, "tenant_id": 1, "user_ref": 2}

	if _, err := g.drawFK(tbl, grp, columns, index, map[string][][]any{}, nil, 0); err == nil {
		t.Fatal("an empty pool returned a value, want an error: one column is NOT NULL")
	}

	pools := map[string][][]any{"tenant_users.tenant_id,user_ref": {{int32(1), int32(10)}}}
	for i := 0; i < 200; i++ {
		tuple, err := g.drawFK(tbl, grp, columns, index, pools, nil, i)
		if err != nil {
			t.Fatalf("draw failed: %v", err)
		}
		if tuple[0] == nil || tuple[1] == nil {
			t.Fatalf("drew a half-null composite key %v; it must be all or nothing", tuple)
		}
	}
}

func TestDrawFKLeavesDeferredReferencesEmpty(t *testing.T) {
	g := NewGenerator(Options{Seed: 1})
	tbl := &db.Table{
		Name:    "a",
		Columns: []db.Column{{Name: "b_id", Nullable: true}},
		FKs:     []db.FK{{ConstraintName: "a_fk", ChildColumn: "b_id", ParentTable: "b", ParentColumn: "id", Deferred: true}},
	}
	grp := tbl.FKGroups()[0]

	pools := map[string][][]any{"b.id": {{int64(1)}}}
	tuple, err := g.drawFK(tbl, grp, tbl.Columns, map[string]int{"b_id": 0}, pools, nil, 5)
	if err != nil {
		t.Fatalf("deferred draw: %v", err)
	}
	if tuple[0] != nil {
		t.Errorf("got %v, want nil: a cycle-breaking reference is left empty", tuple[0])
	}
}

func TestDrawFKSelfReferenceUsesEarlierRows(t *testing.T) {
	g := NewGenerator(Options{Seed: 1, NullRate: 0.01})
	tbl := &db.Table{
		Name:    "employees",
		Columns: []db.Column{{Name: "manager_id", DataType: db.TypeBigint, Nullable: true}},
		FKs:     []db.FK{{ConstraintName: "e_fk", ChildColumn: "manager_id", ParentTable: "employees", ParentColumn: "id"}},
	}
	grp := tbl.FKGroups()[0]
	index := map[string]int{"manager_id": 0}

	// The first row has nobody to point at.
	tuple, err := g.drawFK(tbl, grp, tbl.Columns, index, nil, map[string][][]any{}, 0)
	if err != nil || tuple[0] != nil {
		t.Errorf("first row got (%v, %v), want (nil, nil)", tuple, err)
	}

	// Later rows may only reference keys already generated in this batch.
	own := map[string][][]any{"employees.id": {{int64(1)}, {int64(2)}, {int64(3)}}}
	legal := map[any]bool{int64(1): true, int64(2): true, int64(3): true}
	for i := 1; i < 200; i++ {
		tuple, err := g.drawFK(tbl, grp, tbl.Columns, index, nil, own, i)
		if err != nil {
			t.Fatalf("self draw failed: %v", err)
		}
		if tuple[0] != nil && !legal[tuple[0]] {
			t.Fatalf("drew %v, which was not generated earlier in the batch", tuple[0])
		}
	}
}

func TestGatherSkipsTuplesContainingNull(t *testing.T) {
	// A row with a NULL in the referenced column cannot be pooled: nothing can
	// legally reference it.
	if _, ok := gather([]any{int64(1), nil}, []int{0, 1}); ok {
		t.Error("gather pooled a tuple containing NULL")
	}
	tuple, ok := gather([]any{int64(1), int64(2), "x"}, []int{0, 2})
	if !ok {
		t.Fatal("gather rejected a complete tuple")
	}
	if len(tuple) != 2 || tuple[0] != int64(1) || tuple[1] != "x" {
		t.Errorf("gather = %v, want [1 x]", tuple)
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

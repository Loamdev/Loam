package db

import (
	"strings"
	"testing"
)

// schemaOf builds a schema from a compact description: table -> list of
// "column->parent.column" references. A "?" prefix marks the column nullable.
func schemaOf(spec map[string][]string) *Schema {
	s := NewSchema()
	for table := range spec {
		s.Tables[table] = &Table{
			Name:       table,
			Columns:    []Column{{Name: "id", DataType: TypeBigint, IsPrimaryKey: true}},
			PrimaryKey: []string{"id"},
		}
	}
	for table, refs := range spec {
		t := s.Tables[table]
		for _, ref := range refs {
			nullable := strings.HasPrefix(ref, "?")
			ref = strings.TrimPrefix(ref, "?")

			child, target, _ := strings.Cut(ref, "->")
			parentTable, parentColumn, _ := strings.Cut(target, ".")

			t.Columns = append(t.Columns, Column{Name: child, DataType: TypeBigint, Nullable: nullable})
			t.FKs = append(t.FKs, FK{ChildColumn: child, ParentTable: parentTable, ParentColumn: parentColumn})
		}
	}
	return s
}

// assertParentsFirst is the build spec's phase 4 acceptance condition: every
// table must appear after every table it references.
func assertParentsFirst(t *testing.T, s *Schema) {
	t.Helper()

	position := make(map[string]int, len(s.Order))
	for i, name := range s.Order {
		position[name] = i
	}

	if len(s.Order) != len(s.Tables) {
		t.Fatalf("order has %d entries for %d tables: %v", len(s.Order), len(s.Tables), s.Order)
	}

	for _, name := range s.SortedTableNames() {
		for _, fk := range s.Tables[name].FKs {
			if fk.Deferred || fk.ParentTable == name {
				continue
			}
			if _, ok := s.Tables[fk.ParentTable]; !ok {
				continue
			}
			if position[fk.ParentTable] >= position[name] {
				t.Errorf("%s (position %d) references %s (position %d): parent must come first\norder: %v",
					name, position[name], fk.ParentTable, position[fk.ParentTable], s.Order)
			}
		}
	}
}

func TestTopoSortChainedSchema(t *testing.T) {
	// The build spec's 3-4 table chain: users -> orders -> order_items.
	s := schemaOf(map[string][]string{
		"users":       nil,
		"orders":      {"user_id->users.id"},
		"order_items": {"order_id->orders.id", "?product_id->products.id"},
		"products":    {"?category_id->categories.id"},
		"categories":  nil,
	})

	warnings, err := TopoSort(s)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none for an acyclic schema", warnings)
	}
	assertParentsFirst(t, s)
}

func TestTopoSortIsDeterministic(t *testing.T) {
	spec := map[string][]string{
		"users":       nil,
		"orders":      {"user_id->users.id"},
		"order_items": {"order_id->orders.id", "?product_id->products.id"},
		"products":    {"?category_id->categories.id"},
		"categories":  nil,
		"promotions":  {"?category_id->categories.id"},
		"employees":   {"?manager_id->employees.id"},
	}

	first := ""
	for i := 0; i < 25; i++ {
		s := schemaOf(spec)
		if _, err := TopoSort(s); err != nil {
			t.Fatalf("TopoSort: %v", err)
		}
		got := strings.Join(s.Order, ",")
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("order changed between runs:\n first: %s\n  then: %s", first, got)
		}
	}
}

func TestTopoSortSelfReferenceIsNotACycle(t *testing.T) {
	// manager_id -> employees.id must not deadlock and must not be deferred:
	// the seeder draws it from PKs generated in the same batch.
	s := schemaOf(map[string][]string{"employees": {"?manager_id->employees.id"}})

	warnings, err := TopoSort(s)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none: a self-reference is not a cycle", warnings)
	}
	if strings.Join(s.Order, ",") != "employees" {
		t.Errorf("order = %v, want [employees]", s.Order)
	}
	if got := s.DeferredFKs(); len(got) != 0 {
		t.Errorf("DeferredFKs() = %v, want none", got)
	}
}

func TestTopoSortBreaksAMutualCycle(t *testing.T) {
	// Two tables referencing each other cannot be ordered. One reference has to
	// give, and it must be the nullable one.
	s := schemaOf(map[string][]string{
		"companies": {"?owner_id->people.id"},
		"people":    {"employer_id->companies.id"}, // NOT NULL
	})

	warnings, err := TopoSort(s)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one cycle report", warnings)
	}
	if !strings.Contains(warnings[0], "circular") {
		t.Errorf("warning = %q, want it to name the problem", warnings[0])
	}

	deferred := s.DeferredFKs()
	if len(deferred) != 1 || deferred[0] != "companies.owner_id" {
		t.Errorf("DeferredFKs() = %v, want [companies.owner_id]: the nullable side must give way", deferred)
	}
	assertParentsFirst(t, s)
}

func TestTopoSortBreaksALongerCycle(t *testing.T) {
	s := schemaOf(map[string][]string{
		"a": {"?b_id->b.id"},
		"b": {"?c_id->c.id"},
		"c": {"?a_id->a.id"},
	})

	warnings, err := TopoSort(s)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if len(warnings) == 0 {
		t.Error("want a warning for a three-table cycle")
	}
	assertParentsFirst(t, s)
}

func TestTopoSortWarnsWhenOnlyANotNullFKCanBreakTheCycle(t *testing.T) {
	// Both sides NOT NULL: seeding genuinely cannot satisfy this, so the
	// warning has to say so rather than quietly pretending it worked.
	s := schemaOf(map[string][]string{
		"left":  {"right_id->right.id"},
		"right": {"left_id->left.id"},
	})

	warnings, err := TopoSort(s)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one", warnings)
	}
	if !strings.Contains(warnings[0], "NOT NULL") {
		t.Errorf("warning = %q, want it to admit the column is NOT NULL and seeding may fail", warnings[0])
	}
	assertParentsFirst(t, s)
}

func TestTopoSortIgnoresReferencesOutsideTheSchema(t *testing.T) {
	// A foreign key to a view or a table in another schema is not orderable and
	// has no PK pool; it must not block the sort.
	s := NewSchema()
	s.Tables["events"] = &Table{
		Name:       "events",
		Columns:    []Column{{Name: "id", IsPrimaryKey: true}, {Name: "audit_id", Nullable: true}},
		PrimaryKey: []string{"id"},
		FKs:        []FK{{ChildColumn: "audit_id", ParentTable: "audit_log_view", ParentColumn: "id"}},
	}

	warnings, err := TopoSort(s)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if strings.Join(s.Order, ",") != "events" {
		t.Errorf("order = %v, want [events]", s.Order)
	}
}

func TestTopoSortIsIdempotent(t *testing.T) {
	// Running twice must not accumulate Deferred flags and quietly drop real
	// references on the second pass.
	s := schemaOf(map[string][]string{
		"users":  nil,
		"orders": {"user_id->users.id"},
	})

	for i := 0; i < 3; i++ {
		warnings, err := TopoSort(s)
		if err != nil {
			t.Fatalf("TopoSort run %d: %v", i, err)
		}
		if len(warnings) != 0 {
			t.Errorf("run %d: warnings = %v, want none", i, warnings)
		}
		if got := s.DeferredFKs(); len(got) != 0 {
			t.Errorf("run %d: DeferredFKs() = %v, want none", i, got)
		}
		assertParentsFirst(t, s)
	}
}

func TestTopoSortEmptyAndNil(t *testing.T) {
	if _, err := TopoSort(nil); err != nil {
		t.Errorf("TopoSort(nil) = %v, want nil", err)
	}

	s := NewSchema()
	warnings, err := TopoSort(s)
	if err != nil {
		t.Errorf("TopoSort(empty) = %v, want nil", err)
	}
	if len(warnings) != 0 || len(s.Order) != 0 {
		t.Errorf("empty schema gave order=%v warnings=%v", s.Order, warnings)
	}
}

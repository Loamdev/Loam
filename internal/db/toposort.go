package db

import (
	"fmt"
	"sort"
)

// TopoSort fills s.Order so that every table appears after the tables it
// references. The seeder walks that order, which is what guarantees a parent's
// primary keys exist before a child draws a foreign key from them.
//
// Self-references are not dependencies. A table whose foreign key points at
// itself (manager_id -> employees.id) is seeded in a single pass, drawing the
// reference from primary keys generated earlier in the same batch or leaving it
// null, so ordering it against itself would be meaningless.
//
// A genuine cycle - two or more tables that reference each other - cannot be
// ordered at all. Rather than fail, the cycle is broken by marking one foreign
// key Deferred: the seeder leaves that column empty on the first pass. A
// nullable column is always preferred, since leaving a NOT NULL column empty
// cannot work. Each break is reported in warnings so the user is told what was
// given up.
func TopoSort(s *Schema) (warnings []string, err error) {
	if s == nil {
		return nil, nil
	}
	if len(s.Tables) == 0 {
		s.Order = nil
		return nil, nil
	}

	// Start from a clean slate: TopoSort must be idempotent, and stale Deferred
	// flags from an earlier call would silently drop real references.
	for _, t := range s.Tables {
		for i := range t.FKs {
			t.FKs[i].Deferred = false
		}
	}

	names := s.SortedTableNames()
	emitted := make(map[string]bool, len(names))
	order := make([]string, 0, len(names))

	for len(order) < len(names) {
		deps := s.dependencies()

		progress := false
		for _, name := range names {
			if emitted[name] {
				continue
			}
			if !ready(deps[name], emitted) {
				continue
			}
			emitted[name] = true
			order = append(order, name)
			progress = true
		}

		if progress {
			continue
		}

		// Nothing became ready, so what is left is a cycle.
		warning, err := breakCycle(s, emitted)
		if err != nil {
			return warnings, err
		}
		warnings = append(warnings, warning)
	}

	s.Order = order
	return warnings, nil
}

// dependencies returns, for each table, the set of other tables it must be
// seeded after. Self-references and deferred foreign keys are excluded, as are
// references to tables outside the schema, which cannot be drawn from anyway.
func (s *Schema) dependencies() map[string]map[string]bool {
	deps := make(map[string]map[string]bool, len(s.Tables))
	for name, t := range s.Tables {
		deps[name] = map[string]bool{}
		for _, fk := range t.FKs {
			if fk.Deferred || fk.ParentTable == name {
				continue
			}
			if _, ok := s.Tables[fk.ParentTable]; !ok {
				continue
			}
			deps[name][fk.ParentTable] = true
		}
	}
	return deps
}

// ready reports whether every dependency has already been emitted.
func ready(deps map[string]bool, emitted map[string]bool) bool {
	for parent := range deps {
		if !emitted[parent] {
			return false
		}
	}
	return true
}

// breakCycle defers one foreign key inside the remaining cycle and describes
// what it did. A nullable column wins over a NOT NULL one; ties are broken
// alphabetically so the choice is reproducible.
func breakCycle(s *Schema, emitted map[string]bool) (string, error) {
	type candidate struct {
		table    string
		index    int
		nullable bool
	}

	var best *candidate
	for _, name := range s.SortedTableNames() {
		if emitted[name] {
			continue
		}
		t := s.Tables[name]

		// Deterministic order within a table, independent of how the FKs were
		// read out of the catalog.
		indexes := make([]int, 0, len(t.FKs))
		for i := range t.FKs {
			indexes = append(indexes, i)
		}
		sort.Slice(indexes, func(a, b int) bool {
			return t.FKs[indexes[a]].ChildColumn < t.FKs[indexes[b]].ChildColumn
		})

		for _, i := range indexes {
			fk := t.FKs[i]
			if fk.Deferred || fk.ParentTable == name {
				continue
			}
			if emitted[fk.ParentTable] {
				continue // already satisfied, not what is blocking us
			}
			if _, ok := s.Tables[fk.ParentTable]; !ok {
				continue
			}

			column, _ := t.Column(fk.ChildColumn)
			c := candidate{table: name, index: i, nullable: column.Nullable}
			if best == nil || (!best.nullable && c.nullable) {
				chosen := c
				best = &chosen
			}
		}
	}

	if best == nil {
		// Every remaining table is blocked but no breakable reference exists.
		// This should be unreachable; report it rather than looping forever.
		var stuck []string
		for _, name := range s.SortedTableNames() {
			if !emitted[name] {
				stuck = append(stuck, name)
			}
		}
		return "", fmt.Errorf("cannot order tables %v: they depend on each other with no foreign key left to defer", stuck)
	}

	t := s.Tables[best.table]
	fk := &t.FKs[best.index]
	fk.Deferred = true

	if best.nullable {
		return fmt.Sprintf("circular foreign keys: %s.%s -> %s.%s is left null on the first pass to break the cycle",
			best.table, fk.ChildColumn, fk.ParentTable, fk.ParentColumn), nil
	}
	return fmt.Sprintf("circular foreign keys: %s.%s -> %s.%s had to be deferred, but the column is NOT NULL, so seeding %s may fail",
		best.table, fk.ChildColumn, fk.ParentTable, fk.ParentColumn, best.table), nil
}

// DeferredFKs lists every foreign key TopoSort had to give up on, as
// "table.column" strings.
func (s *Schema) DeferredFKs() []string {
	var out []string
	for _, name := range s.SortedTableNames() {
		for _, fk := range s.Tables[name].FKs {
			if fk.Deferred {
				out = append(out, name+"."+fk.ChildColumn)
			}
		}
	}
	return out
}

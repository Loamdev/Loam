package db

import (
	"sort"
	"strings"
)

// Canonical column data types. Introspection normalises every Postgres type name
// onto one of these so the generator only has to reason about a small set.
const (
	TypeInt         = "int"
	TypeBigint      = "bigint"
	TypeSmallint    = "smallint"
	TypeText        = "text"
	TypeVarchar     = "varchar"
	TypeChar        = "char"
	TypeBool        = "bool"
	TypeTimestamp   = "timestamp"
	TypeTimestamptz = "timestamptz"
	TypeTime        = "time"
	TypeDate        = "date"
	TypeUUID        = "uuid"
	TypeNumeric     = "numeric"
	TypeFloat       = "float"
	TypeJSON        = "json"
	TypeJSONB       = "jsonb"
	TypeBytea       = "bytea"
	TypeInet        = "inet"
	TypeInterval    = "interval"
	TypeEnum        = "enum"
	TypeUnknown     = "unknown"
)

// Column is a single column of a table, normalised for value generation.
type Column struct {
	// Name is the column name as it appears in Postgres.
	Name string
	// DataType is the canonical type, one of the Type* constants.
	DataType string
	// UDTName is the raw Postgres user-defined type name, used to resolve enums.
	UDTName string
	// Nullable reports whether the column accepts NULL.
	Nullable bool
	// IsPrimaryKey reports whether the column participates in the primary key.
	IsPrimaryKey bool
	// HasDefault reports whether the column has a DEFAULT expression.
	HasDefault bool
	// IsIdentity reports whether the column is GENERATED ... AS IDENTITY.
	IsIdentity bool
	// IdentityGeneration is "ALWAYS" or "BY DEFAULT" for an identity column,
	// and empty otherwise. GENERATED ALWAYS rejects an explicitly supplied
	// value, so the seeder has to treat it differently.
	IdentityGeneration string
	// MaxLength is the declared character limit, or 0 when unbounded.
	MaxLength int
	// NumericPrecision is the total digits allowed by a numeric/decimal column,
	// or 0 when unconstrained. numeric(10,2) gives 10.
	NumericPrecision int
	// NumericScale is the digits after the decimal point. numeric(10,2) gives 2.
	NumericScale int
}

// IsInteger reports whether the column holds an integer type.
func (c Column) IsInteger() bool {
	switch c.DataType {
	case TypeInt, TypeBigint, TypeSmallint:
		return true
	default:
		return false
	}
}

// FK is a single-column foreign key declared on a table.
type FK struct {
	// ConstraintName is the Postgres constraint this reference belongs to. A
	// multi-column foreign key produces one FK per column pair, all sharing
	// this name, which is the only way to tell a composite key from several
	// independent single-column ones.
	ConstraintName string
	// ChildColumn is the referencing column on the table that owns this FK.
	ChildColumn string
	// ParentTable is the referenced table.
	ParentTable string
	// ParentColumn is the referenced column on ParentTable.
	ParentColumn string
	// Deferred is set by TopoSort when it had to break a dependency cycle by
	// leaving this reference unpopulated on the first pass.
	Deferred bool
}

// Table is a Postgres table in the public schema.
type Table struct {
	// Name is the table name.
	Name string
	// Columns are the table's columns in ordinal position order.
	Columns []Column
	// FKs are the table's single-column foreign keys.
	FKs []FK
	// PrimaryKey lists the primary key column names, in key order.
	PrimaryKey []string
}

// Column returns the named column and whether it exists.
func (t *Table) Column(name string) (Column, bool) {
	for _, c := range t.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return Column{}, false
}

// FKFor returns the foreign key declared on the named column, if any.
func (t *Table) FKFor(column string) (FK, bool) {
	for _, fk := range t.FKs {
		if fk.ChildColumn == column {
			return fk, true
		}
	}
	return FK{}, false
}

// SelfReferencing reports whether the table has a foreign key pointing at itself.
func (t *Table) SelfReferencing() bool {
	for _, fk := range t.FKs {
		if fk.ParentTable == t.Name {
			return true
		}
	}
	return false
}

// Schema is the introspected public schema of a database.
type Schema struct {
	// Tables is keyed by table name.
	Tables map[string]*Table
	// Order is the topologically sorted table names, parents before children.
	// It is filled by TopoSort.
	Order []string
}

// NewSchema returns an empty schema ready to be populated.
func NewSchema() *Schema {
	return &Schema{Tables: make(map[string]*Table)}
}

// FKCount returns the total number of foreign keys across all tables.
func (s *Schema) FKCount() int {
	n := 0
	for _, t := range s.Tables {
		n += len(t.FKs)
	}
	return n
}

// SortedTableNames returns every table name in deterministic alphabetical order.
func (s *Schema) SortedTableNames() []string {
	names := make([]string, 0, len(s.Tables))
	for name := range s.Tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Children returns, for each table, the tables that hold a foreign key
// referencing it. The result is deterministic.
func (s *Schema) Children(table string) []string {
	var out []string
	for _, name := range s.SortedTableNames() {
		for _, fk := range s.Tables[name].FKs {
			if fk.ParentTable == table && name != table {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// FKGroup is one foreign key constraint, which may span several columns.
type FKGroup struct {
	// Name is the Postgres constraint name.
	Name string
	// ParentTable is the referenced table.
	ParentTable string
	// ChildColumns are the referencing columns, in constraint order.
	ChildColumns []string
	// ParentColumns are the referenced columns, positionally paired with
	// ChildColumns.
	ParentColumns []string
	// Deferred is set when TopoSort had to break a cycle through this key.
	Deferred bool
}

// Composite reports whether the constraint spans more than one column. A
// composite key has to be drawn as a whole tuple: picking each column
// independently produces combinations that do not exist in the parent.
func (g FKGroup) Composite() bool {
	return len(g.ChildColumns) > 1
}

// Key identifies the parent column set a value tuple is pooled under.
func (g FKGroup) Key() string {
	return g.ParentTable + "." + strings.Join(g.ParentColumns, ",")
}

// FKGroups returns the table's foreign keys grouped by constraint, so a
// multi-column key is one entry rather than several. Order is deterministic.
func (t *Table) FKGroups() []FKGroup {
	byName := map[string]*FKGroup{}
	var order []string

	for _, fk := range t.FKs {
		// A constraint name is always present from introspection; fall back to
		// the column so a hand-built schema in a test still groups sanely.
		name := fk.ConstraintName
		if name == "" {
			name = "fk_" + fk.ChildColumn
		}
		grp, ok := byName[name]
		if !ok {
			grp = &FKGroup{Name: name, ParentTable: fk.ParentTable, Deferred: fk.Deferred}
			byName[name] = grp
			order = append(order, name)
		}
		grp.ChildColumns = append(grp.ChildColumns, fk.ChildColumn)
		grp.ParentColumns = append(grp.ParentColumns, fk.ParentColumn)
		// Deferring any column of a constraint defers the whole thing: a
		// partially populated composite key is not a valid reference.
		if fk.Deferred {
			grp.Deferred = true
		}
	}

	sort.Strings(order)
	out := make([]FKGroup, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out
}

// FKGroupFor returns the constraint that the named column participates in.
func (t *Table) FKGroupFor(column string) (FKGroup, bool) {
	for _, grp := range t.FKGroups() {
		for _, c := range grp.ChildColumns {
			if c == column {
				return grp, true
			}
		}
	}
	return FKGroup{}, false
}

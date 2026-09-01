package db

import "sort"

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

package db

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Introspect reads the public schema: every table, its columns with normalized
// types, its primary key, and its foreign keys.
//
// Only real tables are considered. Views and materialized views are excluded
// because they cannot be seeded.
func Introspect(ctx context.Context, pool *pgxpool.Pool) (*Schema, error) {
	s := NewSchema()

	if err := readColumns(ctx, pool, s); err != nil {
		return nil, err
	}
	if err := readPrimaryKeys(ctx, pool, s); err != nil {
		return nil, err
	}
	if err := readForeignKeys(ctx, pool, s); err != nil {
		return nil, err
	}
	return s, nil
}

// columnsQuery reads column metadata for real tables in the public schema.
// The join against pg_tables is what filters views out.
const columnsQuery = `
SELECT c.table_name,
       c.column_name,
       c.data_type,
       c.udt_name,
       c.is_nullable = 'YES'            AS nullable,
       COALESCE(c.character_maximum_length, 0) AS max_length,
       COALESCE(c.numeric_precision, 0)        AS numeric_precision,
       COALESCE(c.numeric_scale, 0)            AS numeric_scale,
       c.column_default IS NOT NULL     AS has_default,
       c.is_identity = 'YES'            AS is_identity,
       COALESCE(c.identity_generation, '') AS identity_generation,
       COALESCE(t.typtype, ' ')         AS typtype
FROM information_schema.columns c
JOIN pg_catalog.pg_tables pt
  ON pt.tablename = c.table_name AND pt.schemaname = c.table_schema
LEFT JOIN pg_catalog.pg_type t
  ON t.typname = c.udt_name
 AND t.typnamespace = (SELECT ns.oid FROM pg_catalog.pg_namespace ns WHERE ns.nspname = c.udt_schema)
WHERE c.table_schema = 'public'
ORDER BY c.table_name, c.ordinal_position`

// readColumns populates the schema's tables and columns.
func readColumns(ctx context.Context, pool *pgxpool.Pool, s *Schema) error {
	rows, err := pool.Query(ctx, columnsQuery)
	if err != nil {
		return fmt.Errorf("read columns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			table, name, dataType, udtName, typtype   string
			identityGeneration                        string
			nullable, hasDefault, isIdentity          bool
			maxLength, numericPrecision, numericScale int
		)
		if err := rows.Scan(&table, &name, &dataType, &udtName, &nullable,
			&maxLength, &numericPrecision, &numericScale,
			&hasDefault, &isIdentity, &identityGeneration, &typtype); err != nil {
			return fmt.Errorf("scan column: %w", err)
		}

		t, ok := s.Tables[table]
		if !ok {
			t = &Table{Name: table}
			s.Tables[table] = t
		}
		t.Columns = append(t.Columns, Column{
			Name:               name,
			DataType:           NormalizeType(dataType, typtype),
			UDTName:            udtName,
			Nullable:           nullable,
			HasDefault:         hasDefault,
			IsIdentity:         isIdentity,
			IdentityGeneration: identityGeneration,
			MaxLength:          maxLength,
			NumericPrecision:   numericPrecision,
			NumericScale:       numericScale,
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read columns: %w", err)
	}
	return nil
}

// primaryKeysQuery reads primary key columns in key order.
const primaryKeysQuery = `
SELECT tc.table_name, kcu.column_name
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
  ON tc.constraint_name = kcu.constraint_name
 AND tc.table_schema    = kcu.table_schema
WHERE tc.constraint_type = 'PRIMARY KEY'
  AND tc.table_schema    = 'public'
ORDER BY tc.table_name, kcu.ordinal_position`

// readPrimaryKeys fills in each table's PrimaryKey and marks the columns.
func readPrimaryKeys(ctx context.Context, pool *pgxpool.Pool, s *Schema) error {
	rows, err := pool.Query(ctx, primaryKeysQuery)
	if err != nil {
		return fmt.Errorf("read primary keys: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return fmt.Errorf("scan primary key: %w", err)
		}
		t, ok := s.Tables[table]
		if !ok {
			continue // a constraint on something that is not a seedable table
		}
		t.PrimaryKey = append(t.PrimaryKey, column)
		for i := range t.Columns {
			if t.Columns[i].Name == column {
				t.Columns[i].IsPrimaryKey = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read primary keys: %w", err)
	}
	return nil
}

// foreignKeysQuery reads foreign keys, pairing each referencing column with the
// column it references.
//
// This uses pg_catalog rather than the information_schema query sketched in the
// build spec on purpose. Joining key_column_usage to constraint_column_usage
// produces a cross product for a multi-column foreign key, which would invent
// column pairings that do not exist. unnest(conkey, confkey) pairs them
// positionally, which is the only correct reading - and FK correctness is the
// one thing this tool cannot get wrong (build spec section 12.5).
const foreignKeysQuery = `
SELECT con.conname         AS constraint_name,
       child.relname      AS child_table,
       child_att.attname  AS child_column,
       parent.relname     AS parent_table,
       parent_att.attname AS parent_column
FROM pg_catalog.pg_constraint con
JOIN pg_catalog.pg_class     child  ON child.oid  = con.conrelid
JOIN pg_catalog.pg_class     parent ON parent.oid = con.confrelid
JOIN pg_catalog.pg_namespace ns     ON ns.oid     = child.relnamespace
CROSS JOIN LATERAL unnest(con.conkey, con.confkey) AS cols(child_attnum, parent_attnum)
JOIN pg_catalog.pg_attribute child_att
  ON child_att.attrelid = con.conrelid AND child_att.attnum = cols.child_attnum
JOIN pg_catalog.pg_attribute parent_att
  ON parent_att.attrelid = con.confrelid AND parent_att.attnum = cols.parent_attnum
WHERE con.contype = 'f'
  AND ns.nspname  = 'public'
ORDER BY child.relname, con.conname, cols.child_attnum`

// readForeignKeys fills in each table's FKs.
func readForeignKeys(ctx context.Context, pool *pgxpool.Pool, s *Schema) error {
	rows, err := pool.Query(ctx, foreignKeysQuery)
	if err != nil {
		return fmt.Errorf("read foreign keys: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var constraintName, childTable, childColumn, parentTable, parentColumn string
		if err := rows.Scan(&constraintName, &childTable, &childColumn, &parentTable, &parentColumn); err != nil {
			return fmt.Errorf("scan foreign key: %w", err)
		}
		t, ok := s.Tables[childTable]
		if !ok {
			continue
		}
		// A reference to something that is not a seedable table (a view, or a
		// table outside public) cannot be drawn from, so it is not recorded:
		// the generator would have no PK pool for it.
		if _, ok := s.Tables[parentTable]; !ok {
			continue
		}
		t.FKs = append(t.FKs, FK{
			ConstraintName: constraintName,
			ChildColumn:    childColumn,
			ParentTable:    parentTable,
			ParentColumn:   parentColumn,
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read foreign keys: %w", err)
	}
	return nil
}

// NormalizeType maps a Postgres type name from information_schema onto the
// canonical set the generator understands. typtype is the pg_type category
// ('e' for enum), used to resolve the USER-DEFINED case.
func NormalizeType(dataType, typtype string) string {
	switch strings.ToLower(strings.TrimSpace(dataType)) {
	case "integer", "int", "int4":
		return TypeInt
	case "bigint", "int8":
		return TypeBigint
	case "smallint", "int2":
		return TypeSmallint
	case "text":
		return TypeText
	case "character varying", "varchar":
		return TypeVarchar
	case "character", "char", "bpchar":
		return TypeChar
	case "boolean", "bool":
		return TypeBool
	case "timestamp without time zone", "timestamp":
		return TypeTimestamp
	case "timestamp with time zone", "timestamptz":
		return TypeTimestamptz
	case "time without time zone", "time with time zone", "time":
		return TypeTime
	case "date":
		return TypeDate
	case "uuid":
		return TypeUUID
	case "numeric", "decimal", "money":
		return TypeNumeric
	case "real", "double precision", "float4", "float8":
		return TypeFloat
	case "json":
		return TypeJSON
	case "jsonb":
		return TypeJSONB
	case "bytea":
		return TypeBytea
	case "inet", "cidr":
		return TypeInet
	case "interval":
		return TypeInterval
	case "user-defined":
		if typtype == "e" {
			return TypeEnum
		}
		return TypeUnknown
	default:
		return TypeUnknown
	}
}

// enumValuesQuery reads every enum type in the public schema with its labels in
// declaration order.
const enumValuesQuery = `
SELECT t.typname, e.enumlabel
FROM pg_catalog.pg_type t
JOIN pg_catalog.pg_enum e     ON e.enumtypid  = t.oid
JOIN pg_catalog.pg_namespace n ON n.oid       = t.typnamespace
WHERE n.nspname = 'public'
ORDER BY t.typname, e.enumsortorder`

// checkConstraintsQuery reads the rendered definition of every CHECK constraint
// in the public schema.
const checkConstraintsQuery = `
SELECT rel.relname, pg_catalog.pg_get_constraintdef(con.oid)
FROM pg_catalog.pg_constraint con
JOIN pg_catalog.pg_class     rel ON rel.oid = con.conrelid
JOIN pg_catalog.pg_namespace ns  ON ns.oid  = rel.relnamespace
WHERE con.contype = 'c'
  AND ns.nspname  = 'public'`

// AllowedValues reports the values a column is permitted to hold, keyed
// "table.column".
//
// Two sources are read: enum types, and simple single-column CHECK constraints
// of the `col IN ('a','b')` shape. Drawing status-like values from the real
// constraint beats any generic profile, and it is the difference between data
// that inserts and data Postgres rejects.
//
// This is best effort by design. Anything it cannot parse is simply absent from
// the result, and the caller falls back to a distribution profile; a CHECK it
// does not understand must never fail the run.
func AllowedValues(ctx context.Context, pool *pgxpool.Pool, s *Schema) (map[string][]string, error) {
	allowed := map[string][]string{}

	enums, err := readEnums(ctx, pool)
	if err != nil {
		return nil, err
	}
	for _, table := range s.SortedTableNames() {
		for _, c := range s.Tables[table].Columns {
			if c.DataType == TypeEnum {
				if labels, ok := enums[c.UDTName]; ok {
					allowed[table+"."+c.Name] = labels
				}
			}
		}
	}

	rows, err := pool.Query(ctx, checkConstraintsQuery)
	if err != nil {
		return nil, fmt.Errorf("read check constraints: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var table, def string
		if err := rows.Scan(&table, &def); err != nil {
			return nil, fmt.Errorf("scan check constraint: %w", err)
		}
		t, ok := s.Tables[table]
		if !ok {
			continue
		}
		column, values, ok := ParseCheckAllowedValues(def)
		if !ok {
			continue
		}
		if _, ok := t.Column(column); !ok {
			continue
		}
		key := table + "." + column
		// An enum type is the stronger statement; do not let a CHECK on the
		// same column widen it.
		if _, ok := allowed[key]; !ok {
			allowed[key] = values
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read check constraints: %w", err)
	}
	return allowed, nil
}

// readEnums returns enum type name -> labels, in declaration order.
func readEnums(ctx context.Context, pool *pgxpool.Pool) (map[string][]string, error) {
	rows, err := pool.Query(ctx, enumValuesQuery)
	if err != nil {
		return nil, fmt.Errorf("read enum values: %w", err)
	}
	defer rows.Close()

	enums := map[string][]string{}
	for rows.Next() {
		var typeName, label string
		if err := rows.Scan(&typeName, &label); err != nil {
			return nil, fmt.Errorf("scan enum value: %w", err)
		}
		enums[typeName] = append(enums[typeName], label)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read enum values: %w", err)
	}
	return enums, nil
}

// checkColumnPattern finds the column a CHECK constrains. Postgres renders a
// column reference either bare or cast, e.g. `status` or `(status)::text`.
var checkColumnPattern = regexp.MustCompile(`\(?\b([a-z_][a-z0-9_]*)\b\)?(?:::[a-z ]+(?:\[\])?)?\s*=\s*(?:ANY|any)\b|\(?\b([a-z_][a-z0-9_]*)\b\)?(?:::[a-z ]+)?\s+IN\s*\(`)

// checkLiteralPattern extracts the single-quoted literals from a CHECK body.
var checkLiteralPattern = regexp.MustCompile(`'((?:[^']|'')*)'`)

// ParseCheckAllowedValues extracts the column and permitted values from a
// rendered CHECK constraint that restricts one column to a fixed set.
//
// Postgres does not store the SQL as written: `CHECK (status IN ('a','b'))`
// comes back from pg_get_constraintdef as
//
//	CHECK (((status)::text = ANY ((ARRAY['a'::character varying, 'b'::character varying])::text[])))
//
// so both that shape and the plainer `IN (...)` rendering are handled. Anything
// else reports false rather than guessing.
func ParseCheckAllowedValues(def string) (column string, values []string, ok bool) {
	m := checkColumnPattern.FindStringSubmatch(def)
	if m == nil {
		return "", nil, false
	}
	column = m[1]
	if column == "" {
		column = m[2]
	}
	if column == "" {
		return "", nil, false
	}

	for _, lit := range checkLiteralPattern.FindAllStringSubmatch(def, -1) {
		// '' is how a single quote is escaped inside a literal.
		values = append(values, strings.ReplaceAll(lit[1], "''", "'"))
	}
	if len(values) == 0 {
		return "", nil, false
	}
	return column, values, true
}

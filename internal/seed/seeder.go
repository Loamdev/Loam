package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shivansh-source/stateful-playwright/internal/db"
)

// Row-count heuristic constants.
const (
	// rowScaleDivisor is how much a table shrinks per level of tables that
	// depend on it. A lookup table three levels up does not need ten thousand
	// rows for its children to look varied.
	rowScaleDivisor = 5
	// minRows keeps even the most-referenced lookup table populated enough to
	// produce variety in its children.
	minRows = 50
	// smallintMaxRows keeps a smallserial primary key inside int16.
	smallintMaxRows = 32000
	// copyBatchSize bounds how many rows are held in memory per COPY call.
	copyBatchSize = 20000
)

// Result reports what a seeding run produced.
type Result struct {
	// RowsByTable is the number of rows written per table.
	RowsByTable map[string]int
	// TotalRows is the sum across all tables.
	TotalRows int
	// Duration is how long generation plus loading took.
	Duration time.Duration
	// Warnings describes anything that was skipped or compromised.
	Warnings []string
}

// Seed fills every table in the schema with foreign-key-correct synthetic data.
//
// Tables are walked in s.Order, so a parent's primary keys always exist before a
// child needs one. Every foreign key value is drawn from a pool of keys that
// were actually generated - never invented (build spec section 12.5) - and rows
// are loaded with pgx.CopyFrom rather than INSERT (section 12.3).
func Seed(ctx context.Context, pool *pgxpool.Pool, s *db.Schema, opts Options) (*Result, error) {
	if s == nil || len(s.Tables) == 0 {
		return &Result{RowsByTable: map[string]int{}}, nil
	}
	if len(s.Order) != len(s.Tables) {
		return nil, fmt.Errorf("schema is not ordered: %d of %d tables sequenced; call db.TopoSort first",
			len(s.Order), len(s.Tables))
	}
	if err := validateSeedable(s); err != nil {
		return nil, err
	}

	g := NewGenerator(opts)
	started := time.Now()

	result := &Result{RowsByTable: make(map[string]int, len(s.Tables))}
	// pools holds the values generated for every column another table
	// references, keyed "table.column".
	pools := map[string][]any{}
	needed := referencedColumns(s)

	for _, table := range s.Order {
		t := s.Tables[table]

		columns, warnings := seedableColumns(g, t)
		result.Warnings = append(result.Warnings, warnings...)
		if len(columns) == 0 {
			continue
		}

		rowCount := RowCountFor(s, table, opts)
		if rowCount <= 0 {
			continue
		}

		written, err := seedTable(ctx, pool, g, t, columns, rowCount, pools, needed[table])
		if err != nil {
			return nil, err
		}
		result.RowsByTable[table] = written
		result.TotalRows += written
	}

	// Explicit primary keys leave every serial sequence behind, so the app's
	// first insert would collide. Nudging them past the seeded data is what
	// makes the seeded app actually usable, not just readable.
	if err := resetSequences(ctx, pool, s); err != nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("could not reset primary key sequences: %v", err))
	}

	result.Duration = time.Since(started)
	return result, nil
}

// seedTable generates and loads one table, recording the key values that other
// tables will draw from.
func seedTable(
	ctx context.Context,
	pool *pgxpool.Pool,
	g *Generator,
	t *db.Table,
	columns []db.Column,
	rowCount int,
	pools map[string][]any,
	recordColumns []string,
) (int, error) {
	names := make([]string, len(columns))
	for i, c := range columns {
		names[i] = c.Name
	}

	record := make(map[string]bool, len(recordColumns))
	for _, name := range recordColumns {
		record[name] = true
	}

	// Keys generated so far in this batch, so a self-reference has something
	// real to point at.
	ownKeys := map[string][]any{}

	written := 0
	for start := 0; start < rowCount; start += copyBatchSize {
		size := min(copyBatchSize, rowCount-start)

		batch := make([][]any, size)
		for i := range size {
			row := make([]any, len(columns))
			for j, c := range columns {
				v, err := g.columnValue(t, c, pools, ownKeys, start+i)
				if err != nil {
					return written, err
				}
				row[j] = v

				if record[c.Name] && v != nil {
					pools[t.Name+"."+c.Name] = append(pools[t.Name+"."+c.Name], v)
				}
				if c.IsPrimaryKey && v != nil {
					ownKeys[c.Name] = append(ownKeys[c.Name], v)
				}
			}
			batch[i] = row
		}

		n, err := pool.CopyFrom(ctx, pgx.Identifier{t.Name}, names, pgx.CopyFromRows(batch))
		if err != nil {
			return written, fmt.Errorf("bulk load %s (%d rows): %w", t.Name, size, err)
		}
		written += int(n)
	}

	return written, nil
}

// columnValue decides one cell: a drawn foreign key, a fresh primary key, or a
// generated value.
func (g *Generator) columnValue(
	t *db.Table,
	c db.Column,
	pools map[string][]any,
	ownKeys map[string][]any,
	rowIndex int,
) (any, error) {
	if fk, ok := t.FKFor(c.Name); ok {
		return g.foreignKeyValue(t, c, fk, pools, ownKeys, rowIndex)
	}
	if c.IsPrimaryKey {
		return g.PKValue(t.Name, c), nil
	}
	return g.Value(t.Name, c), nil
}

// foreignKeyValue draws a reference from a parent's real keys.
//
// It never invents a value. If the pool is empty the reference is left null when
// the column allows it, and reported as an error when it does not - a fabricated
// key would produce data that looks fine and is structurally a lie.
func (g *Generator) foreignKeyValue(
	t *db.Table,
	c db.Column,
	fk db.FK,
	pools map[string][]any,
	ownKeys map[string][]any,
	rowIndex int,
) (any, error) {
	// A cycle-breaking reference is deliberately left empty on this pass.
	if fk.Deferred {
		return nil, nil
	}

	// Self-reference: draw from keys generated earlier in this same batch.
	if fk.ParentTable == t.Name {
		earlier := ownKeys[fk.ParentColumn]
		switch {
		case rowIndex == 0 || len(earlier) == 0:
			// Nothing to point at yet. A nullable column stays null; a NOT NULL
			// one points at the row itself, which Postgres accepts.
			if c.Nullable {
				return nil, nil
			}
			return nil, nil
		case c.Nullable && g.rng.Float64() < g.opts.NullRate:
			return nil, nil
		default:
			return earlier[g.rng.Intn(len(earlier))], nil
		}
	}

	// A nullable foreign key is sometimes empty, which is what real data looks
	// like and what exposes unguarded joins.
	if c.Nullable && g.rng.Float64() < g.opts.NullRate {
		return nil, nil
	}

	parent := pools[fk.ParentTable+"."+fk.ParentColumn]
	if len(parent) == 0 {
		if c.Nullable {
			return nil, nil
		}
		return nil, fmt.Errorf(
			"%s.%s must reference %s.%s but no rows were generated for %s: cannot invent a foreign key",
			t.Name, c.Name, fk.ParentTable, fk.ParentColumn, fk.ParentTable)
	}
	return parent[g.rng.Intn(len(parent))], nil
}

// seedableColumns picks the columns to write, explaining anything it leaves out.
func seedableColumns(g *Generator, t *db.Table) (columns []db.Column, warnings []string) {
	for _, c := range t.Columns {
		// GENERATED ALWAYS AS IDENTITY rejects a supplied value outright, so the
		// database has to produce it.
		if c.IdentityGeneration == "ALWAYS" {
			warnings = append(warnings, fmt.Sprintf(
				"%s.%s is GENERATED ALWAYS AS IDENTITY, so Postgres assigns it; children cannot reference it",
				t.Name, c.Name))
			continue
		}

		// A foreign key is always writable: its value comes from a pool, not
		// from the generator's type support.
		if _, isFK := t.FKFor(c.Name); isFK {
			columns = append(columns, c)
			continue
		}

		if g.Supported(c) {
			columns = append(columns, c)
			continue
		}

		// An unsupported type is left to its default when it has one or accepts
		// null. Otherwise there is nothing honest to write.
		if c.HasDefault || c.Nullable {
			warnings = append(warnings, fmt.Sprintf(
				"%s.%s has type %s, which Playbrige does not generate; leaving it to the database",
				t.Name, c.Name, c.DataType))
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s.%s is NOT NULL with type %s and no default; Playbrige cannot generate a value for it",
			t.Name, c.Name, c.DataType))
	}
	return columns, warnings
}

// validateSeedable rejects schemas that cannot be filled, with a message that
// says what to change, rather than letting Postgres reject the batch.
func validateSeedable(s *db.Schema) error {
	for _, name := range s.SortedTableNames() {
		t := s.Tables[name]
		for _, fk := range t.FKs {
			if !fk.Deferred {
				continue
			}
			c, ok := t.Column(fk.ChildColumn)
			if ok && !c.Nullable {
				return fmt.Errorf(
					"%s.%s is part of a foreign key cycle and is NOT NULL: it cannot be left empty on the first pass, so this schema cannot be seeded",
					name, fk.ChildColumn)
			}
		}
	}
	return nil
}

// referencedColumns reports, per table, which of its columns another table (or
// the table itself) references. Only those need to be pooled.
func referencedColumns(s *db.Schema) map[string][]string {
	needed := map[string]map[string]bool{}
	for _, name := range s.SortedTableNames() {
		for _, fk := range s.Tables[name].FKs {
			if _, ok := s.Tables[fk.ParentTable]; !ok {
				continue
			}
			if needed[fk.ParentTable] == nil {
				needed[fk.ParentTable] = map[string]bool{}
			}
			needed[fk.ParentTable][fk.ParentColumn] = true
		}
	}

	out := make(map[string][]string, len(needed))
	for table, columns := range needed {
		for column := range columns {
			out[table] = append(out[table], column)
		}
	}
	return out
}

// RowCountFor decides how many rows a table gets.
//
// An explicit override always wins. Otherwise a leaf table - one nothing
// references - gets the full default, and a table shrinks by rowScaleDivisor for
// each level of tables that depend on it: ten thousand order items are
// interesting, ten thousand categories are noise. A smallint key caps the count
// so the sequence cannot overflow int16.
func RowCountFor(s *db.Schema, table string, opts Options) int {
	if rows, ok := opts.TableRows[table]; ok {
		return clampToKeyWidth(s, table, rows)
	}

	base := opts.DefaultRows
	if base <= 0 {
		base = DefaultRowsFallback
	}

	rows := base
	for range dependentDepth(s, table, map[string]bool{}) {
		rows /= rowScaleDivisor
	}
	if rows < minRows {
		rows = minRows
	}
	return clampToKeyWidth(s, table, rows)
}

// DefaultRowsFallback is used when Options carries no default row count.
const DefaultRowsFallback = 10000

// dependentDepth is how many levels of tables depend on this one. A leaf is 0.
// The visiting set keeps a foreign key cycle from recursing forever.
func dependentDepth(s *db.Schema, table string, visiting map[string]bool) int {
	if visiting[table] {
		return 0
	}
	visiting[table] = true
	defer delete(visiting, table)

	deepest := 0
	for _, child := range s.Children(table) {
		if d := dependentDepth(s, child, visiting) + 1; d > deepest {
			deepest = d
		}
	}
	return deepest
}

// clampToKeyWidth keeps a generated sequential key inside its column's type.
func clampToKeyWidth(s *db.Schema, table string, rows int) int {
	t, ok := s.Tables[table]
	if !ok {
		return rows
	}
	for _, name := range t.PrimaryKey {
		c, ok := t.Column(name)
		if !ok {
			continue
		}
		if c.DataType == db.TypeSmallint && rows > smallintMaxRows {
			return smallintMaxRows
		}
	}
	return rows
}

// resetSequences advances each serial or identity sequence past the rows just
// written, so the application's next insert does not collide with seeded data.
func resetSequences(ctx context.Context, pool *pgxpool.Pool, s *db.Schema) error {
	for _, table := range s.Order {
		t := s.Tables[table]
		for _, name := range t.PrimaryKey {
			c, ok := t.Column(name)
			if !ok || !c.IsInteger() {
				continue
			}

			// Identifiers cannot be bound as parameters, so they are sanitized
			// and interpolated; the text arguments to pg_get_serial_sequence are
			// bound normally.
			query := fmt.Sprintf(`
SELECT setval(seq.name, GREATEST(COALESCE((SELECT MAX(%s) FROM %s), 0), 1))
FROM (SELECT pg_get_serial_sequence($1, $2) AS name) seq
WHERE seq.name IS NOT NULL`,
				pgx.Identifier{c.Name}.Sanitize(), pgx.Identifier{t.Name}.Sanitize())

			if _, err := pool.Exec(ctx, query, t.Name, c.Name); err != nil {
				return fmt.Errorf("reset sequence for %s.%s: %w", t.Name, c.Name, err)
			}
		}
	}
	return nil
}

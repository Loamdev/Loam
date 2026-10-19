package seed

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/loamdev/loam/internal/db"
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
	// pools holds the value tuples generated for every column set another table
	// references, keyed "table.col" or "table.col1,col2". Tuples rather than
	// single values, because a composite foreign key must be drawn whole: one
	// independent draw per column invents pairs the parent never had.
	pools := map[string][][]any{}
	needed := referencedKeys(s)

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

// seedTable generates and loads one table, recording the key tuples that other
// tables will draw from.
func seedTable(
	ctx context.Context,
	pool *pgxpool.Pool,
	g *Generator,
	t *db.Table,
	columns []db.Column,
	rowCount int,
	pools map[string][][]any,
	recordKeys [][]string,
) (int, error) {
	names := make([]string, len(columns))
	index := make(map[string]int, len(columns))
	for i, c := range columns {
		names[i] = c.Name
		index[c.Name] = i
	}

	// Only constraints whose columns are all being written can be drawn.
	var groups []db.FKGroup
	for _, grp := range t.FKGroups() {
		if writable(grp, index) {
			groups = append(groups, grp)
		}
	}

	// The parent column sets this table has to publish, resolved to row offsets.
	type recorder struct {
		key     string
		offsets []int
	}
	var recorders []recorder
	for _, key := range recordKeys {
		offsets, ok := offsetsFor(key, index)
		if !ok {
			continue // the column is not being written, so it cannot be pooled
		}
		recorders = append(recorders, recorder{
			key:     t.Name + "." + strings.Join(key, ","),
			offsets: offsets,
		})
	}

	// Tuples generated so far in this batch, so a self-reference has something
	// real to point at.
	own := map[string][][]any{}

	written := 0
	for start := 0; start < rowCount; start += copyBatchSize {
		size := min(copyBatchSize, rowCount-start)

		batch := make([][]any, size)
		for i := range size {
			row := make([]any, len(columns))
			filled := make([]bool, len(columns))

			// Foreign keys first, drawn as whole tuples.
			for _, grp := range groups {
				tuple, err := g.drawFK(t, grp, columns, index, pools, own, start+i)
				if err != nil {
					return written, err
				}
				for k, child := range grp.ChildColumns {
					at := index[child]
					row[at] = tuple[k]
					filled[at] = true
				}
			}

			// Then everything else.
			for j, c := range columns {
				if filled[j] {
					continue
				}
				if c.IsPrimaryKey {
					row[j] = g.PKValue(t.Name, c)
					continue
				}
				row[j] = g.Value(t.Name, c)
			}

			// Publish this row's key tuples for children, and for this table's
			// own later rows when it references itself.
			for _, r := range recorders {
				if tuple, ok := gather(row, r.offsets); ok {
					pools[r.key] = append(pools[r.key], tuple)
					own[r.key] = append(own[r.key], tuple)
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

// writable reports whether every column of a constraint is being written.
func writable(grp db.FKGroup, index map[string]int) bool {
	for _, c := range grp.ChildColumns {
		if _, ok := index[c]; !ok {
			return false
		}
	}
	return true
}

// offsetsFor resolves a column set to row offsets, reporting false if any column
// is absent.
func offsetsFor(key []string, index map[string]int) ([]int, bool) {
	offsets := make([]int, 0, len(key))
	for _, name := range key {
		at, ok := index[name]
		if !ok {
			return nil, false
		}
		offsets = append(offsets, at)
	}
	return offsets, true
}

// gather extracts a tuple from a row. A tuple containing a NULL is not pooled:
// nothing can legally reference it.
func gather(row []any, offsets []int) ([]any, bool) {
	tuple := make([]any, len(offsets))
	for i, at := range offsets {
		if row[at] == nil {
			return nil, false
		}
		tuple[i] = row[at]
	}
	return tuple, true
}

// nullTuple is an all-NULL tuple of the given width. A composite reference is
// all-or-nothing: half a key is not a reference.
func nullTuple(width int) []any {
	return make([]any, width)
}

// drawFK draws one whole foreign key tuple from the parent's generated keys.
//
// It never invents a value (build spec section 12.5). An empty pool yields NULL
// when every column of the constraint allows it, and a named error when one does
// not, because a fabricated key produces data that looks fine and is a lie.
func (g *Generator) drawFK(
	t *db.Table,
	grp db.FKGroup,
	columns []db.Column,
	index map[string]int,
	pools map[string][][]any,
	own map[string][][]any,
	rowIndex int,
) ([]any, error) {
	width := len(grp.ChildColumns)

	// A cycle-breaking reference is deliberately left empty on this pass.
	if grp.Deferred {
		return nullTuple(width), nil
	}

	// Treated as nullable only when every column of the constraint is nullable.
	// Postgres leaves a partially NULL composite key unenforced, which would
	// quietly drop the relationship instead of modeling it.
	nullable := true
	for _, child := range grp.ChildColumns {
		if !columns[index[child]].Nullable {
			nullable = false
			break
		}
	}

	source := pools
	if grp.ParentTable == t.Name {
		// Self-reference: only rows already generated in this batch exist.
		source = own
		if rowIndex == 0 {
			return nullTuple(width), nil
		}
	}

	candidates := source[grp.Key()]
	if len(candidates) == 0 {
		if nullable {
			return nullTuple(width), nil
		}
		return nil, fmt.Errorf(
			"%s (%s) must reference %s (%s) but no rows were generated for %s: cannot invent a foreign key",
			t.Name, strings.Join(grp.ChildColumns, ", "),
			grp.ParentTable, strings.Join(grp.ParentColumns, ", "), grp.ParentTable)
	}

	// A nullable reference is sometimes empty, which is what real data looks
	// like and what exposes unguarded joins.
	if nullable && g.rng.Float64() < g.opts.NullRate {
		return nullTuple(width), nil
	}

	return candidates[g.rng.Intn(len(candidates))], nil
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
				"%s.%s has type %s, which Loam does not generate; leaving it to the database",
				t.Name, c.Name, c.DataType))
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s.%s is NOT NULL with type %s and no default; Loam cannot generate a value for it",
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

// referencedKeys reports, per table, which of its column sets another table (or
// the table itself) references. Only those need pooling, and a composite key is
// one set rather than several independent columns.
func referencedKeys(s *db.Schema) map[string][][]string {
	seen := map[string]map[string]bool{}
	out := map[string][][]string{}

	for _, name := range s.SortedTableNames() {
		for _, grp := range s.Tables[name].FKGroups() {
			if _, ok := s.Tables[grp.ParentTable]; !ok {
				continue
			}
			signature := strings.Join(grp.ParentColumns, ",")
			if seen[grp.ParentTable] == nil {
				seen[grp.ParentTable] = map[string]bool{}
			}
			if seen[grp.ParentTable][signature] {
				continue
			}
			seen[grp.ParentTable][signature] = true
			out[grp.ParentTable] = append(out[grp.ParentTable], grp.ParentColumns)
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

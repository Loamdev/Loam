package seed

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shivansh-source/stateful-playwright/internal/db"
)

// testDBEnv points the integration tests at a live Postgres holding the
// testapp/ fixture schema:
//
//	playbrige up --dir testapp
//	PLAYBRIGE_TEST_DB=postgres://appuser:apppass@localhost:5433/appdb go test ./internal/seed/...
//
// Without it these tests skip, so `go test ./...` needs no Docker.
const testDBEnv = "PLAYBRIGE_TEST_DB"

// liveFixture connects, introspects, orders, and empties the fixture database,
// leaving it ready to seed.
func liveFixture(t *testing.T) (context.Context, *pgxpool.Pool, *db.Schema, Options) {
	t.Helper()

	url := os.Getenv(testDBEnv)
	if url == "" {
		t.Skipf("%s is not set; skipping the live seeding test", testDBEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	pool, err := db.Connect(ctx, url, db.ReadyTimeout)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	schema, err := db.Introspect(ctx, pool)
	if err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	if _, err := db.TopoSort(schema); err != nil {
		t.Fatalf("TopoSort: %v", err)
	}

	allowed, err := db.AllowedValues(ctx, pool, schema)
	if err != nil {
		t.Fatalf("AllowedValues: %v", err)
	}

	// Start from empty so counts and integrity checks mean something.
	truncateAll(ctx, t, pool, schema)

	return ctx, pool, schema, Options{DefaultRows: 10000, AllowedValues: allowed, Seed: 1}
}

// truncateAll empties every table in the schema.
func truncateAll(ctx context.Context, t *testing.T, pool *pgxpool.Pool, schema *db.Schema) {
	t.Helper()

	names := schema.SortedTableNames()
	if len(names) == 0 {
		return
	}
	list := ""
	for i, n := range names {
		if i > 0 {
			list += ", "
		}
		list += `"` + n + `"`
	}
	if _, err := pool.Exec(ctx, "TRUNCATE "+list+" RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func TestSeedFixtureLeavesNoDanglingForeignKeys(t *testing.T) {
	ctx, pool, schema, opts := liveFixture(t)

	result, err := Seed(ctx, pool, schema, opts)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	for _, w := range result.Warnings {
		t.Logf("warning: %s", w)
	}
	t.Logf("seeded %d rows across %d tables in %s", result.TotalRows, len(result.RowsByTable), result.Duration)

	// Every foreign key in the schema, checked against the real rows. This is
	// the build spec's sacred rule (section 12.5), so it is verified rather
	// than assumed.
	checked := 0
	for _, table := range schema.SortedTableNames() {
		for _, fk := range schema.Tables[table].FKs {
			if fk.Deferred {
				continue
			}
			query := fmt.Sprintf(`
SELECT count(*) FROM %q child
LEFT JOIN %q parent ON parent.%q = child.%q
WHERE child.%q IS NOT NULL AND parent.%q IS NULL`,
				table, fk.ParentTable, fk.ParentColumn, fk.ChildColumn, fk.ChildColumn, fk.ParentColumn)

			var dangling int
			if err := pool.QueryRow(ctx, query).Scan(&dangling); err != nil {
				t.Fatalf("integrity check for %s.%s: %v", table, fk.ChildColumn, err)
			}
			if dangling != 0 {
				t.Errorf("%s.%s has %d values with no matching %s.%s row",
					table, fk.ChildColumn, dangling, fk.ParentTable, fk.ParentColumn)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no foreign keys were checked; the fixture should have several")
	}
	t.Logf("verified %d foreign keys, all intact", checked)
}

func TestSeedFixtureMeetsThePerformanceTarget(t *testing.T) {
	ctx, pool, schema, opts := liveFixture(t)

	result, err := Seed(ctx, pool, schema, opts)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}

	// Build spec: about 10,000 rows in under 2 seconds. Row-by-row INSERT
	// cannot reach this, so the assertion is really "COPY is still being used".
	if result.TotalRows < 10000 {
		t.Fatalf("seeded only %d rows; the target is measured at ~10,000", result.TotalRows)
	}

	perRow := result.Duration / time.Duration(result.TotalRows)
	budget := 2 * time.Second / 10000
	if perRow > budget {
		t.Errorf("seeded %d rows in %s (%.1fµs/row); the spec's 10k-in-2s target allows %.1fµs/row - is this still using COPY?",
			result.TotalRows, result.Duration,
			float64(perRow.Nanoseconds())/1000, float64(budget.Nanoseconds())/1000)
	}
	t.Logf("%d rows in %s (%.1fµs/row)", result.TotalRows, result.Duration, float64(perRow.Nanoseconds())/1000)
}

func TestSeedFixtureRespectsConstraintsAndPopulatesEveryTable(t *testing.T) {
	ctx, pool, schema, opts := liveFixture(t)

	result, err := Seed(ctx, pool, schema, opts)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}

	for _, table := range schema.SortedTableNames() {
		if result.RowsByTable[table] == 0 {
			t.Errorf("%s got no rows", table)
			continue
		}
		var actual int
		if err := pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %q", table)).Scan(&actual); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if actual != result.RowsByTable[table] {
			t.Errorf("%s reports %d rows but holds %d", table, result.RowsByTable[table], actual)
		}
	}

	// A status column must be skewed, not evenly spread: that is what makes the
	// rare cases rare and the data worth testing against.
	var distinct, commonest int
	err = pool.QueryRow(ctx, `
SELECT count(*), max(n) FROM (SELECT count(*) AS n FROM orders GROUP BY status) c`).Scan(&distinct, &commonest)
	if err != nil {
		t.Fatalf("status distribution: %v", err)
	}
	if distinct < 2 {
		t.Errorf("orders.status used %d distinct values, want a mix", distinct)
	}
	share := float64(commonest) / float64(result.RowsByTable["orders"])
	if share < 0.2 {
		t.Errorf("the commonest orders.status is only %.0f%% of rows; the mix looks uniform", share*100)
	}
}

func TestSeedFixtureLeavesTheAppAbleToInsert(t *testing.T) {
	ctx, pool, schema, opts := liveFixture(t)

	if _, err := Seed(ctx, pool, schema, opts); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	// Supplying explicit primary keys leaves every serial sequence at 1, so
	// without a reset the application's first insert collides with seeded data.
	// This is the difference between data you can read and an app you can use.
	var id int64
	err := pool.QueryRow(ctx, `
INSERT INTO users (email, full_name, status, is_verified, created_at)
VALUES ('integration-probe@example.com', 'Probe User', 'active', true, now())
RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatalf("the app cannot insert after seeding, so the sequences were not reset: %v", err)
	}

	var seeded int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&seeded); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if id < seeded {
		t.Errorf("new row got id %d with %d rows present; the sequence is behind the seeded data", id, seeded)
	}
}

func TestSeedFixtureIsReproducible(t *testing.T) {
	ctx, pool, schema, opts := liveFixture(t)

	fingerprint := func() string {
		truncateAll(ctx, t, pool, schema)
		if _, err := Seed(ctx, pool, schema, opts); err != nil {
			t.Fatalf("Seed: %v", err)
		}
		var sum string
		err := pool.QueryRow(ctx, `
SELECT md5(string_agg(row_data, '|' ORDER BY row_data))
FROM (SELECT u::text AS row_data FROM users u) s`).Scan(&sum)
		if err != nil {
			t.Fatalf("fingerprint: %v", err)
		}
		return sum
	}

	first := fingerprint()
	second := fingerprint()
	if first != second {
		t.Errorf("the same seed produced different data (%s vs %s); reproducing a bug from a seed would be impossible",
			first, second)
	}
}

package seed

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/shivansh-source/stateful-playwright/internal/config"
	"github.com/shivansh-source/stateful-playwright/internal/db"
)

// gen returns a generator with a fixed seed.
func gen(t *testing.T) *Generator {
	t.Helper()
	// NullRate 0 would be replaced by the default, so tests that need every
	// value populated pass a nullable=false column instead.
	return NewGenerator(Options{Seed: 99, DefaultRows: 100})
}

// sample collects n non-nil values for a column.
func sample(g *Generator, table string, c db.Column, n int) []any {
	out := make([]any, 0, n)
	for i := 0; i < n; i++ {
		if v := g.Value(table, c); v != nil {
			out = append(out, v)
		}
	}
	return out
}

func TestValueIsNameAwareForTextColumns(t *testing.T) {
	g := gen(t)

	tests := []struct {
		column  string
		pattern *regexp.Regexp
		desc    string
	}{
		{"email", regexp.MustCompile(`^[a-z.]+\d+@example\.com$`), "an email address"},
		{"phone", regexp.MustCompile(`^\+91[6-9]\d{8,9}$`), "an Indian mobile number"},
		{"mobile", regexp.MustCompile(`^\+91[6-9]\d{8,9}$`), "an Indian mobile number"},
		{"pincode", regexp.MustCompile(`^[1-8]\d{5}$`), "a 6-digit Indian PIN code"},
		{"zip", regexp.MustCompile(`^[1-8]\d{5}$`), "a 6-digit Indian PIN code"},
		{"signup_url", regexp.MustCompile(`^https?://`), "a URL"},
		{"bio", regexp.MustCompile(`\w+ \w+`), "a sentence"},
	}

	for _, tc := range tests {
		t.Run(tc.column, func(t *testing.T) {
			c := db.Column{Name: tc.column, DataType: db.TypeText}
			for _, v := range sample(g, "users", c, 40) {
				s, ok := v.(string)
				if !ok {
					t.Fatalf("%s produced %T, want a string", tc.column, v)
				}
				if !tc.pattern.MatchString(s) {
					t.Errorf("%s produced %q, want %s matching %v", tc.column, s, tc.desc, tc.pattern)
				}
			}
		})
	}
}

func TestValueUsesIndianLocaleForNames(t *testing.T) {
	g := gen(t)

	first := map[string]bool{}
	for _, n := range indianFirstNames {
		first[n] = true
	}
	last := map[string]bool{}
	for _, n := range indianLastNames {
		last[n] = true
	}
	cities := map[string]bool{}
	for _, n := range indianCities {
		cities[n] = true
	}

	for _, v := range sample(g, "users", db.Column{Name: "first_name", DataType: db.TypeVarchar}, 30) {
		if !first[v.(string)] {
			t.Errorf("first_name produced %q, which is not in the Indian first-name list", v)
		}
	}
	for _, v := range sample(g, "users", db.Column{Name: "last_name", DataType: db.TypeVarchar}, 30) {
		if !last[v.(string)] {
			t.Errorf("last_name produced %q, which is not in the Indian surname list", v)
		}
	}
	for _, v := range sample(g, "users", db.Column{Name: "city", DataType: db.TypeVarchar}, 30) {
		if !cities[v.(string)] {
			t.Errorf("city produced %q, which is not in the Indian city list", v)
		}
	}
	for _, v := range sample(g, "users", db.Column{Name: "full_name", DataType: db.TypeVarchar}, 30) {
		parts := strings.Fields(v.(string))
		if len(parts) != 2 || !first[parts[0]] || !last[parts[1]] {
			t.Errorf("full_name produced %q, want an Indian given name and surname", v)
		}
	}
}

func TestValueRespectsTheDeclaredTypeOverTheName(t *testing.T) {
	// The name rules must never win over the column's actual type: a column
	// called "status" that happens to be an integer still has to get an
	// integer, or the COPY fails (build spec section 5).
	g := gen(t)

	cases := []struct {
		name     string
		column   db.Column
		wantKind string
	}{
		{"integer status", db.Column{Name: "status", DataType: db.TypeInt}, "int32"},
		{"integer name", db.Column{Name: "name", DataType: db.TypeBigint}, "int64"},
		{"boolean status", db.Column{Name: "status", DataType: db.TypeBool}, "bool"},
		{"timestamp named total", db.Column{Name: "total", DataType: db.TypeTimestamptz}, "time.Time"},
		{"numeric named city", db.Column{Name: "city", DataType: db.TypeNumeric}, "float64"},
		{"uuid named email", db.Column{Name: "email", DataType: db.TypeUUID}, "string"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < 20; i++ {
				v := g.Value("t", tc.column)
				if v == nil {
					continue
				}
				got := kindOf(v)
				if got != tc.wantKind {
					t.Fatalf("%s produced %s (%v), want %s", tc.name, got, v, tc.wantKind)
				}
			}
		})
	}
}

// kindOf names a generated value's Go type for assertions.
func kindOf(v any) string {
	switch v.(type) {
	case int16:
		return "int16"
	case int32:
		return "int32"
	case int64:
		return "int64"
	case bool:
		return "bool"
	case float64:
		return "float64"
	case string:
		return "string"
	case time.Time:
		return "time.Time"
	default:
		return "unknown"
	}
}

func TestValueTruncatesToMaxLength(t *testing.T) {
	g := gen(t)

	// A varchar(6) pincode and a varchar(3) name both have to fit, or Postgres
	// rejects the whole batch.
	for _, c := range []db.Column{
		{Name: "pincode", DataType: db.TypeVarchar, MaxLength: 6},
		{Name: "full_name", DataType: db.TypeVarchar, MaxLength: 3},
		{Name: "bio", DataType: db.TypeVarchar, MaxLength: 10},
		{Name: "email", DataType: db.TypeVarchar, MaxLength: 12},
	} {
		for _, v := range sample(g, "users", c, 30) {
			if got := len(v.(string)); got > c.MaxLength {
				t.Errorf("%s produced %d characters for a varchar(%d): %q", c.Name, got, c.MaxLength, v)
			}
		}
	}
}

func TestValueRespectsNumericPrecision(t *testing.T) {
	g := gen(t)

	// numeric(5,2) tops out just under 1000. Generating money-shaped values
	// into it without clamping overflows and fails the COPY.
	c := db.Column{Name: "discount_pct", DataType: db.TypeNumeric, NumericPrecision: 5, NumericScale: 2}
	for _, v := range sample(g, "promotions", c, 200) {
		f := v.(float64)
		if f >= 1000 || f < 0 {
			t.Errorf("numeric(5,2) got %v, which does not fit", f)
		}
	}

	// A price column with room to breathe should still look like money.
	price := db.Column{Name: "price", DataType: db.TypeNumeric, NumericPrecision: 10, NumericScale: 2}
	sawRealistic := false
	for _, v := range sample(g, "products", price, 100) {
		f := v.(float64)
		if f >= 100000000 {
			t.Errorf("numeric(10,2) got %v, which does not fit", f)
		}
		if f > 1 {
			sawRealistic = true
		}
	}
	if !sawRealistic {
		t.Error("price values were all tiny; money should look like money")
	}
}

func TestValueClampsSmallint(t *testing.T) {
	g := gen(t)
	c := db.Column{Name: "quantity", DataType: db.TypeSmallint}
	for _, v := range sample(g, "t", c, 200) {
		if _, ok := v.(int16); !ok {
			t.Fatalf("smallint produced %T", v)
		}
	}
}

func TestValueDrawsEnumsFromAllowedValues(t *testing.T) {
	// An enum value that is not in the type is rejected by Postgres, so this is
	// not a nicety.
	allowed := []string{"active", "trial", "churned", "suspended"}
	g := NewGenerator(Options{
		Seed:          5,
		AllowedValues: map[string][]string{"users.status": allowed},
	})

	legal := map[string]bool{}
	for _, v := range allowed {
		legal[v] = true
	}

	seen := map[string]int{}
	c := db.Column{Name: "status", DataType: db.TypeEnum, UDTName: "user_status"}
	for i := 0; i < 2000; i++ {
		v := g.Value("users", c)
		s := v.(string)
		if !legal[s] {
			t.Fatalf("generated %q, which the enum does not permit", s)
		}
		seen[s]++
	}
	if len(seen) < 2 {
		t.Errorf("only %v appeared; the mix should use more of the enum", seen)
	}
	if seen["active"] <= seen["suspended"] {
		t.Errorf("distribution is not realistic: active=%d suspended=%d", seen["active"], seen["suspended"])
	}
}

func TestValueIntersectsConfiguredDistributionWithConstraints(t *testing.T) {
	// A configured value the CHECK rejects would fail the entire COPY, not one
	// row, so configuration has to be filtered by what is actually legal.
	g := NewGenerator(Options{
		Seed:          3,
		AllowedValues: map[string][]string{"orders.status": {"paid", "pending"}},
		Distributions: map[string]config.Distribution{
			"orders.status": {"paid": 0.5, "pending": 0.3, "teleported": 0.2},
		},
	})

	c := db.Column{Name: "status", DataType: db.TypeVarchar}
	for i := 0; i < 500; i++ {
		got := g.Value("orders", c).(string)
		if got == "teleported" {
			t.Fatal("generated a value the CHECK constraint forbids")
		}
		if got != "paid" && got != "pending" {
			t.Fatalf("generated unexpected value %q", got)
		}
	}
}

func TestValueFallsBackToDefaultStatusProfile(t *testing.T) {
	// No enum, no CHECK, just a column called status.
	g := NewGenerator(Options{Seed: 11})
	c := db.Column{Name: "status", DataType: db.TypeVarchar}

	seen := map[string]int{}
	for i := 0; i < 3000; i++ {
		seen[g.Value("things", c).(string)]++
	}
	for want := range DefaultStatus {
		if seen[want] == 0 {
			t.Errorf("%q never appeared: %v", want, seen)
		}
	}
	if seen["active"] <= seen["suspended"] {
		t.Errorf("not skewed: %v", seen)
	}
}

func TestValueProducesNullsForNullableColumns(t *testing.T) {
	g := NewGenerator(Options{Seed: 21, NullRate: 0.5})
	c := db.Column{Name: "bio", DataType: db.TypeText, Nullable: true}

	nulls := 0
	const n = 2000
	for i := 0; i < n; i++ {
		if g.Value("users", c) == nil {
			nulls++
		}
	}
	rate := float64(nulls) / n
	if rate < 0.4 || rate > 0.6 {
		t.Errorf("null rate %.2f, want about 0.5", rate)
	}
}

func TestValueNeverNullsANotNullColumn(t *testing.T) {
	g := NewGenerator(Options{Seed: 21, NullRate: 0.9})
	c := db.Column{Name: "bio", DataType: db.TypeText, Nullable: false}

	for i := 0; i < 500; i++ {
		if g.Value("users", c) == nil {
			t.Fatal("generated NULL for a NOT NULL column")
		}
	}
}

func TestValueProducesValidJSON(t *testing.T) {
	g := gen(t)
	for _, c := range []db.Column{
		{Name: "metadata", DataType: db.TypeJSONB},
		{Name: "payload", DataType: db.TypeJSON},
	} {
		for _, v := range sample(g, "products", c, 20) {
			var out map[string]any
			if err := json.Unmarshal([]byte(v.(string)), &out); err != nil {
				t.Errorf("%s produced invalid JSON %q: %v", c.Name, v, err)
			}
		}
	}
}

func TestValueProducesPlausibleRecentDates(t *testing.T) {
	g := gen(t)

	for _, c := range []db.Column{
		{Name: "created_at", DataType: db.TypeTimestamptz},
		{Name: "last_synced_at", DataType: db.TypeTimestamp},
		{Name: "hired_on", DataType: db.TypeDate},
		{Name: "starts_on", DataType: db.TypeDate},
	} {
		for _, v := range sample(g, "t", c, 50) {
			ts := v.(time.Time)
			if ts.Year() < 2020 || ts.Year() > 2030 {
				t.Errorf("%s produced %v, which is not a plausible recent date", c.Name, ts)
			}
			if c.DataType == db.TypeDate {
				if ts.Hour() != 0 || ts.Minute() != 0 || ts.Second() != 0 {
					t.Errorf("%s is a date column but got a time component: %v", c.Name, ts)
				}
			}
		}
	}
}

func TestPKValueIsSequentialAndUnique(t *testing.T) {
	g := gen(t)

	for _, c := range []db.Column{
		{Name: "id", DataType: db.TypeBigint, IsPrimaryKey: true},
		{Name: "id", DataType: db.TypeInt, IsPrimaryKey: true},
		{Name: "id", DataType: db.TypeSmallint, IsPrimaryKey: true},
		{Name: "id", DataType: db.TypeUUID, IsPrimaryKey: true},
		{Name: "code", DataType: db.TypeVarchar, IsPrimaryKey: true, MaxLength: 40},
	} {
		table := "t_" + c.DataType
		seen := map[any]bool{}
		for i := 0; i < 500; i++ {
			v := g.PKValue(table, c)
			if v == nil {
				t.Fatalf("%s primary key produced nil", c.DataType)
			}
			if seen[v] {
				t.Fatalf("%s primary key repeated %v after %d rows", c.DataType, v, i)
			}
			seen[v] = true
		}
	}
}

func TestPKValueSequencesPerTable(t *testing.T) {
	g := gen(t)
	c := db.Column{Name: "id", DataType: db.TypeBigint, IsPrimaryKey: true}

	if got := g.PKValue("users", c); got != int64(1) {
		t.Errorf("first users key = %v, want 1", got)
	}
	if got := g.PKValue("orders", c); got != int64(1) {
		t.Errorf("first orders key = %v, want 1: counters are per table", got)
	}
	if got := g.PKValue("users", c); got != int64(2) {
		t.Errorf("second users key = %v, want 2", got)
	}
}

func TestSupported(t *testing.T) {
	g := gen(t)

	for _, ty := range []string{
		db.TypeInt, db.TypeBigint, db.TypeSmallint, db.TypeText, db.TypeVarchar,
		db.TypeChar, db.TypeBool, db.TypeTimestamp, db.TypeTimestamptz, db.TypeTime,
		db.TypeDate, db.TypeUUID, db.TypeNumeric, db.TypeFloat, db.TypeJSON, db.TypeJSONB, db.TypeEnum,
	} {
		if !g.Supported(db.Column{DataType: ty}) {
			t.Errorf("Supported(%s) = false", ty)
		}
	}

	// Honest about what it cannot do, rather than writing something wrong.
	for _, ty := range []string{db.TypeBytea, db.TypeInet, db.TypeInterval, db.TypeUnknown} {
		if g.Supported(db.Column{DataType: ty}) {
			t.Errorf("Supported(%s) = true, but nothing generates it", ty)
		}
	}
}

func TestGenerationIsReproducibleForASeed(t *testing.T) {
	// Same seed, same schema, same data. This is what makes "reproduce the bug"
	// possible at all.
	columns := []db.Column{
		{Name: "email", DataType: db.TypeVarchar, MaxLength: 255},
		{Name: "full_name", DataType: db.TypeVarchar, MaxLength: 120},
		{Name: "status", DataType: db.TypeVarchar},
		{Name: "created_at", DataType: db.TypeTimestamptz},
		{Name: "total", DataType: db.TypeNumeric, NumericPrecision: 12, NumericScale: 2},
		{Name: "is_verified", DataType: db.TypeBool},
		{Name: "bio", DataType: db.TypeText, Nullable: true},
	}

	run := func() []any {
		g := NewGenerator(Options{Seed: 12345})
		var out []any
		for i := 0; i < 100; i++ {
			for _, c := range columns {
				out = append(out, g.Value("users", c))
			}
		}
		return out
	}

	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("runs produced %d and %d values", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("value %d differed between runs with the same seed: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestDifferentSeedsProduceDifferentData(t *testing.T) {
	c := db.Column{Name: "full_name", DataType: db.TypeVarchar}

	first := NewGenerator(Options{Seed: 1})
	second := NewGenerator(Options{Seed: 2})

	same := 0
	const n = 100
	for i := 0; i < n; i++ {
		if first.Value("users", c) == second.Value("users", c) {
			same++
		}
	}
	if same == n {
		t.Error("two different seeds produced identical data")
	}
}

func TestIsStatusName(t *testing.T) {
	for _, name := range []string{"status", "STATUS", "state", "type", "kind", "order_status", "payment_state", "user_role"} {
		if !isStatusName(name) {
			t.Errorf("isStatusName(%q) = false", name)
		}
	}
	for _, name := range []string{"email", "created_at", "statuses", "typename", "total"} {
		if isStatusName(name) {
			t.Errorf("isStatusName(%q) = true", name)
		}
	}
}

func TestUniqueValuesSurviveNarrowColumns(t *testing.T) {
	// Regression: these values carry a per-row counter, but they used to go
	// through fitString, which trims from the right and so discarded the counter
	// first. An email in a varchar(12) collided by row 14 and a varchar(4) sku by
	// row 160 - and a duplicate in a UNIQUE column fails the entire COPY, not one
	// row.
	const rows = 5000

	tests := []struct {
		column    string
		maxLength int
	}{
		{"email", 255}, {"email", 32}, {"email", 12},
		{"sku", 40}, {"sku", 8}, {"sku", 5},
		{"promo_code", 6},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s/varchar(%d)", tc.column, tc.maxLength), func(t *testing.T) {
			g := NewGenerator(Options{Seed: 7})
			c := db.Column{Name: tc.column, DataType: db.TypeVarchar, MaxLength: tc.maxLength}

			seen := make(map[string]int, rows)
			for i := 1; i <= rows; i++ {
				v, ok := g.Value("t", c).(string)
				if !ok {
					t.Fatalf("row %d produced a non-string", i)
				}
				if len(v) > tc.maxLength {
					t.Fatalf("row %d produced %q, longer than varchar(%d)", i, v, tc.maxLength)
				}
				if first, dup := seen[v]; dup {
					t.Fatalf("duplicate %q at row %d (first seen at row %d): the counter was truncated away",
						v, i, first)
				}
				seen[v] = i
			}
		})
	}
}

func TestFitUniqueGivesUpTheCounterLast(t *testing.T) {
	g := NewGenerator(Options{Seed: 1})
	col := func(width int) db.Column { return db.Column{DataType: db.TypeVarchar, MaxLength: width} }

	tests := []struct {
		name   string
		got    string
		want   string
		reason string
	}{
		{"room for everything", g.fitUnique("ABC-", 42, 5, "", col(40)), "ABC-00042", "padded when it fits"},
		{"unbounded column", g.fitUnique("ABC-", 42, 5, "", col(0)), "ABC-00042", "no limit means no trimming"},
		{"padding dropped", g.fitUnique("ABC-", 42, 5, "", col(6)), "ABC-42", "padding goes before the counter"},
		{"suffix dropped", g.fitUnique("priya.sharma", 42, 0, "@example.com", col(14)), "priya.sharma42", "the domain goes before the counter"},
		{"prefix trimmed", g.fitUnique("priya.sharma", 42, 0, "@example.com", col(6)), "priy42", "the prefix is trimmed to exactly fill the width"},
		{"counter only", g.fitUnique("ABCDEF-", 12345, 5, "", col(3)), "345", "too narrow for anything but the counter's low digits"},
	}

	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q (%s)", tc.name, tc.got, tc.want, tc.reason)
		}
	}
}

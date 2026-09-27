package seed

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/shivansh-source/stateful-playwright/internal/config"
	"github.com/shivansh-source/stateful-playwright/internal/db"
)

// DefaultNullRate is how often a nullable, non-foreign-key column is left empty.
// Real tables are not uniformly populated, and code that has never seen a NULL
// is code that breaks the first time it does.
const DefaultNullRate = 0.1

// Options controls generation. The zero value is not useful; go through
// NewGenerator, which fills in defaults.
type Options struct {
	// DefaultRows is the base row count for a leaf table.
	DefaultRows int
	// TableRows overrides the row count for named tables.
	TableRows map[string]int
	// Distributions overrides the value mix for columns, keyed "table.column".
	Distributions map[string]config.Distribution
	// AllowedValues lists the values a column may hold, keyed "table.column",
	// as read from enum types and CHECK constraints.
	AllowedValues map[string][]string
	// Seed makes a run reproducible. Two runs with the same seed and schema
	// produce byte-identical data.
	Seed int64
	// NullRate is how often a nullable column is left empty. Zero means
	// DefaultNullRate.
	NullRate float64
}

// Indian-locale name, city and address data, so generated rows read like an
// Indian product's customer list rather than a US one (build spec section 5).
var (
	indianFirstNames = []string{
		"Aarav", "Aditya", "Advait", "Akash", "Ananya", "Anika", "Arjun", "Aryan",
		"Avni", "Bhavya", "Chetan", "Darshan", "Deepika", "Dhruv", "Divya", "Farhan",
		"Gaurav", "Harsh", "Ishaan", "Ishita", "Kabir", "Kavya", "Kiran", "Krishna",
		"Lakshmi", "Manish", "Meera", "Mohit", "Naina", "Neha", "Nikhil", "Nisha",
		"Pooja", "Pranav", "Priya", "Rahul", "Rajesh", "Rani", "Reyansh", "Riya",
		"Rohan", "Sahil", "Sanjay", "Sanya", "Saurabh", "Shreya", "Siddharth",
		"Simran", "Sneha", "Suresh", "Swati", "Tanvi", "Tarun", "Uday", "Vaishnavi",
		"Varun", "Vihaan", "Vikram", "Vivaan", "Yash", "Zoya",
	}
	indianLastNames = []string{
		"Agarwal", "Bansal", "Bhat", "Bose", "Chatterjee", "Chauhan", "Chopra",
		"Desai", "Dixit", "Dutta", "Gandhi", "Ghosh", "Gupta", "Iyer", "Jain",
		"Joshi", "Kapoor", "Kaur", "Khanna", "Kulkarni", "Kumar", "Malhotra",
		"Mehta", "Menon", "Mishra", "Mukherjee", "Nair", "Nayak", "Pandey",
		"Patel", "Pillai", "Prasad", "Rao", "Reddy", "Saxena", "Sen", "Shah",
		"Sharma", "Shetty", "Singh", "Sinha", "Trivedi", "Verma", "Yadav",
	}
	indianCities = []string{
		"Agra", "Ahmedabad", "Amritsar", "Aurangabad", "Bengaluru", "Bhopal",
		"Bhubaneswar", "Chandigarh", "Chennai", "Coimbatore", "Dehradun", "Delhi",
		"Faridabad", "Ghaziabad", "Guwahati", "Gurugram", "Hyderabad", "Indore",
		"Jaipur", "Jodhpur", "Kanpur", "Kochi", "Kolkata", "Lucknow", "Ludhiana",
		"Madurai", "Mangaluru", "Meerut", "Mumbai", "Mysuru", "Nagpur", "Nashik",
		"Noida", "Patna", "Pune", "Raipur", "Rajkot", "Ranchi", "Surat",
		"Thiruvananthapuram", "Thane", "Vadodara", "Varanasi", "Vijayawada", "Visakhapatnam",
	}
	// Real Indian mobile numbers start with 6, 7, 8 or 9.
	indianMobilePrefixes = []string{"63", "70", "72", "73", "74", "80", "81", "82", "84", "88", "90", "91", "94", "95", "97", "98", "99"}
	// Leading digits of real Indian PIN codes (1-8; 0 and 9 are not used).
	indianPincodeLeading = []string{"1", "2", "3", "4", "5", "6", "7", "8"}
)

// Generator produces column values. It is not safe for concurrent use: one
// generator belongs to one seeding run.
type Generator struct {
	opts  Options
	rng   *rand.Rand
	fake  *gofakeit.Faker
	now   time.Time
	seqs  map[string]int64               // per-table primary key counter
	dists map[string]config.Distribution // resolved per "table.column"
	known map[string]bool                // whether a distribution was resolved
}

// NewGenerator returns a generator seeded for reproducibility.
func NewGenerator(opts Options) *Generator {
	if opts.NullRate <= 0 {
		opts.NullRate = DefaultNullRate
	}
	if opts.DefaultRows <= 0 {
		opts.DefaultRows = config.DefaultRows
	}

	return &Generator{
		opts: opts,
		rng:  rand.New(rand.NewSource(opts.Seed)), //nolint:gosec // synthetic data, not cryptography
		fake: gofakeit.New(uint64(opts.Seed)),     //nolint:gosec // ditto
		// Fixed instant so a seeded run is reproducible: a moving "now" would
		// change every timestamp between runs.
		now:   time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC),
		seqs:  map[string]int64{},
		dists: map[string]config.Distribution{},
		known: map[string]bool{},
	}
}

// PKValue returns a fresh primary key value for a table.
//
// Integer keys are sequential from 1, which keeps them compact, readable, and
// trivially unique; uuid keys are random. Both are recorded by the seeder so
// children can draw foreign keys from them.
func (g *Generator) PKValue(table string, c db.Column) any {
	switch {
	case c.IsInteger():
		g.seqs[table]++
		n := g.seqs[table]
		switch c.DataType {
		case db.TypeSmallint:
			return int16(n)
		case db.TypeInt:
			return int32(n)
		default:
			return n
		}
	case c.DataType == db.TypeUUID:
		return g.fake.UUID()
	default:
		// A text or otherwise unusual key still has to be unique.
		g.seqs[table]++
		return g.fitString(fmt.Sprintf("%s-%06d", table, g.seqs[table]), c)
	}
}

// Supported reports whether the generator can produce a value for a column.
// The seeder uses this to leave exotic types to their database default rather
// than guessing and failing the whole COPY.
func (g *Generator) Supported(c db.Column) bool {
	switch c.DataType {
	case db.TypeInt, db.TypeBigint, db.TypeSmallint, db.TypeText, db.TypeVarchar,
		db.TypeChar, db.TypeBool, db.TypeTimestamp, db.TypeTimestamptz, db.TypeTime,
		db.TypeDate, db.TypeUUID, db.TypeNumeric, db.TypeFloat, db.TypeJSON,
		db.TypeJSONB, db.TypeEnum:
		return true
	default:
		// bytea, inet, interval, arrays and anything unrecognized.
		return false
	}
}

// Value produces a value for a column that is neither a primary key nor a
// foreign key. Name-aware rules are tried first and only when the column's type
// can actually hold the result, so a column called "status" that happens to be
// an integer still gets an integer (build spec section 5).
func (g *Generator) Value(table string, c db.Column) any {
	if c.Nullable && g.rng.Float64() < g.opts.NullRate {
		return nil
	}
	if v, ok := g.byName(table, c); ok {
		return v
	}
	return g.byType(c)
}

// byName applies the column-name rules from the build spec's mapping table.
func (g *Generator) byName(table string, c db.Column) (any, bool) {
	name := strings.ToLower(c.Name)

	// An enum column must come from its own value set; nothing else is legal.
	if dist, ok := g.distributionFor(table, c); ok {
		if v := Pick(g.rng, dist); v != "" {
			return g.fitString(v, c), true
		}
	}

	switch {
	case isTextual(c.DataType):
		switch {
		case strings.Contains(name, "email"):
			// A per-row counter keeps these unique, which matters because a
			// real users table usually has a unique index on email. fitUnique,
			// not fitString: a narrow column must give up the domain rather
			// than the counter.
			g.seqs[table+".email"]++
			local := strings.ToLower(g.pick(indianFirstNames) + "." + g.pick(indianLastNames))
			return g.fitUnique(local, g.seqs[table+".email"], 0, "@example.com", c), true
		case strings.Contains(name, "first_name"), strings.Contains(name, "firstname"), name == "fname":
			return g.fitString(g.pick(indianFirstNames), c), true
		case strings.Contains(name, "last_name"), strings.Contains(name, "lastname"), name == "lname", strings.Contains(name, "surname"):
			return g.fitString(g.pick(indianLastNames), c), true
		case strings.Contains(name, "phone"), strings.Contains(name, "mobile"), strings.Contains(name, "contact_no"):
			return g.fitString("+91"+g.pick(indianMobilePrefixes)+g.fake.DigitN(8), c), true
		case strings.Contains(name, "pincode"), strings.Contains(name, "pin_code"),
			strings.Contains(name, "zip"), strings.Contains(name, "postal"):
			return g.fitString(g.pick(indianPincodeLeading)+g.fake.DigitN(5), c), true
		case strings.Contains(name, "city"), strings.Contains(name, "town"):
			return g.fitString(g.pick(indianCities), c), true
		case strings.Contains(name, "url"), strings.Contains(name, "link"), strings.Contains(name, "website"):
			return g.fitString(g.fake.URL(), c), true
		case strings.Contains(name, "description"), strings.Contains(name, "bio"),
			strings.Contains(name, "about"), strings.Contains(name, "note"),
			strings.Contains(name, "comment"), strings.Contains(name, "summary"):
			return g.fitString(g.fake.Sentence(g.rng.Intn(12)+6), c), true
		case strings.Contains(name, "slug"):
			return g.fitString(strings.ToLower(g.fake.Word()+"-"+g.fake.LetterN(5)), c), true
		case strings.Contains(name, "sku"), strings.Contains(name, "code"):
			// Zero-padded when the column has room, bare counter when it does
			// not: these often carry a unique index.
			g.seqs[table+"."+name]++
			return g.fitUnique(strings.ToUpper(g.fake.LetterN(3))+"-", g.seqs[table+"."+name], 5, "", c), true
		// Things that are named but are not people. These must come before the
		// generic "name" rule below, which would otherwise claim them: a column
		// called product_name matched it and got "Rohan Gupta", which is valid
		// data and obviously wrong the moment anyone looks at it.
		case strings.Contains(name, "product_name"), strings.Contains(name, "item_name"),
			name == "product", name == "item":
			return g.fitString(g.fake.ProductName(), c), true
		case strings.Contains(name, "company"), strings.Contains(name, "organization"),
			strings.Contains(name, "organisation"), strings.Contains(name, "employer"),
			strings.Contains(name, "vendor"), strings.Contains(name, "supplier"),
			strings.Contains(name, "merchant"), strings.Contains(name, "brand"):
			return g.fitString(g.fake.Company(), c), true
		case strings.Contains(name, "category"), strings.Contains(name, "department"):
			return g.fitString(g.fake.ProductCategory(), c), true
		case strings.Contains(name, "title"), strings.Contains(name, "subject"),
			strings.Contains(name, "headline"), strings.Contains(name, "label"):
			return g.fitString(capitalize(g.fake.Adjective())+" "+g.fake.NounConcrete(), c), true

		case strings.Contains(name, "full_name"), strings.Contains(name, "fullname"), strings.Contains(name, "name"):
			return g.fitString(g.pick(indianFirstNames)+" "+g.pick(indianLastNames), c), true
		}

	case isTemporal(c.DataType):
		// created_at, updated_at, *_at, *_date, *_on: a plausible recent moment.
		if strings.HasSuffix(name, "_at") || strings.HasSuffix(name, "_date") ||
			strings.HasSuffix(name, "_on") || strings.Contains(name, "date") ||
			strings.Contains(name, "time") {
			return g.temporal(c), true
		}

	case isRealNumber(c.DataType):
		if strings.Contains(name, "price") || strings.Contains(name, "amount") ||
			strings.Contains(name, "total") || strings.Contains(name, "cost") ||
			strings.Contains(name, "balance") || strings.Contains(name, "fee") ||
			strings.Contains(name, "salary") || strings.Contains(name, "revenue") {
			return g.money(c), true
		}
		if strings.Contains(name, "pct") || strings.Contains(name, "percent") ||
			strings.Contains(name, "rate") || strings.Contains(name, "discount") {
			return g.roundTo(g.fake.Float64Range(0, 90), c), true
		}

	case c.DataType == db.TypeBool:
		// is_verified, has_photo: weighted true, because most flags are set.
		if strings.HasPrefix(name, "is_") || strings.HasPrefix(name, "has_") ||
			strings.HasPrefix(name, "can_") || strings.HasPrefix(name, "should_") {
			return g.rng.Float64() < 0.75, true
		}

	case c.IsInteger():
		if strings.Contains(name, "qty") || strings.Contains(name, "quantity") ||
			strings.Contains(name, "count") {
			return g.integer(c, 1, 20), true
		}
		if strings.Contains(name, "age") {
			return g.integer(c, 18, 75), true
		}
	}

	return nil, false
}

// byType is the fallback: a value that merely has to be valid for the column.
func (g *Generator) byType(c db.Column) any {
	switch c.DataType {
	case db.TypeText:
		return g.fake.Sentence(g.rng.Intn(6) + 3)
	case db.TypeVarchar, db.TypeChar:
		return g.fitString(g.fake.Word(), c)
	case db.TypeInt, db.TypeBigint, db.TypeSmallint:
		return g.integer(c, 1, 10000)
	case db.TypeBool:
		return g.fake.Bool()
	case db.TypeTimestamp, db.TypeTimestamptz, db.TypeDate, db.TypeTime:
		return g.temporal(c)
	case db.TypeUUID:
		return g.fake.UUID()
	case db.TypeNumeric, db.TypeFloat:
		return g.money(c)
	case db.TypeJSON, db.TypeJSONB:
		return g.jsonObject()
	case db.TypeEnum:
		// An enum with no known values cannot be guessed; NULL is the only
		// safe answer, and a NOT NULL enum is caught by the seeder up front.
		return nil
	default:
		return nil
	}
}

// distributionFor resolves the value mix for a column, caching the answer.
//
// Precedence: an explicit playbrige.yml distribution, then the column's real
// permitted values, then the generic status profile for a status-like name. A
// configured distribution is intersected with the permitted values, because a
// value the CHECK rejects would fail the entire COPY rather than one row.
func (g *Generator) distributionFor(table string, c db.Column) (config.Distribution, bool) {
	key := table + "." + c.Name
	if resolved, ok := g.known[key]; ok {
		if !resolved {
			return nil, false
		}
		return g.dists[key], true
	}

	dist, ok := g.resolveDistribution(key, c)
	g.known[key] = ok
	if ok {
		g.dists[key] = dist
	}
	return dist, ok
}

// resolveDistribution computes a column's value mix from scratch.
func (g *Generator) resolveDistribution(key string, c db.Column) (config.Distribution, bool) {
	allowed := g.opts.AllowedValues[key]

	if configured, ok := g.opts.Distributions[key]; ok && len(configured) > 0 {
		if len(allowed) == 0 {
			return configured, true
		}
		legal := make(config.Distribution, len(configured))
		for _, v := range allowed {
			if w, ok := configured[v]; ok {
				legal[v] = w
			}
		}
		if len(legal) > 0 {
			return legal, true
		}
		// Everything configured is illegal for this column; the constraint wins.
		return Profile(allowed), true
	}

	if len(allowed) > 0 {
		return Profile(allowed), true
	}
	if isStatusName(c.Name) && isTextual(c.DataType) {
		return DefaultStatus, true
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// value helpers
// ---------------------------------------------------------------------------

// pick chooses one element deterministically from the generator's rng.
func (g *Generator) pick(values []string) string {
	return values[g.rng.Intn(len(values))]
}

// fitString truncates a value to the column's declared length. Postgres rejects
// an over-long value outright, which would fail the whole batch.
//
// Do NOT use this for a value whose uniqueness matters: it trims from the right,
// so a per-row counter on the end is the first thing it discards. Use fitUnique.
func (g *Generator) fitString(s string, c db.Column) string {
	if c.MaxLength > 0 && len(s) > c.MaxLength {
		return s[:c.MaxLength]
	}
	return s
}

// fitUnique renders prefix + counter + suffix into the column's width, giving up
// everything else before it truncates the counter.
//
// The counter is the only thing making these values distinct, and a duplicate in
// a UNIQUE column fails the entire COPY rather than one row - so it is the last
// thing to go. Plain fitString would chop it off first: an email in a
// varchar(12) collided by row 14, and a varchar(4) sku by row 160.
//
// padTo zero-pads the counter when there is room for it, purely so the common
// case looks like a real SKU rather than a bare integer.
//
// A column too narrow to hold even the counter cannot be unique across a large
// seed - varchar(2) has a few hundred possible values and no scheme fixes that.
// The value is kept as wide as possible and the caller lives with arithmetic.
func (g *Generator) fitUnique(prefix string, n int64, padTo int, suffix string, c db.Column) string {
	counter := strconv.FormatInt(n, 10)

	// Widest form first: padded counter, full suffix.
	if padTo > 0 {
		padded := fmt.Sprintf("%0*d", padTo, n)
		if v := prefix + padded + suffix; c.MaxLength <= 0 || len(v) <= c.MaxLength {
			return v
		}
	} else if v := prefix + counter + suffix; c.MaxLength <= 0 || len(v) <= c.MaxLength {
		return v
	}

	// Then drop the padding, then the suffix, then trim the prefix.
	if v := prefix + counter + suffix; len(v) <= c.MaxLength {
		return v
	}
	if v := prefix + counter; len(v) <= c.MaxLength {
		return v
	}
	if len(counter) < c.MaxLength {
		return prefix[:c.MaxLength-len(counter)] + counter
	}
	return counter[len(counter)-c.MaxLength:]
}

// temporal returns a plausible recent moment in the column's own type.
func (g *Generator) temporal(c db.Column) any {
	// Two years back is recent enough to look live, wide enough to sort well.
	t := g.fake.DateRange(g.now.AddDate(-2, 0, 0), g.now)
	switch c.DataType {
	case db.TypeDate:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	case db.TypeTime:
		return time.Date(2000, time.January, 1, t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
	default:
		return t.UTC()
	}
}

// money returns a realistic amount that fits the column's declared precision.
func (g *Generator) money(c db.Column) any {
	const (
		lowest  = 49.0
		highest = 25000.0
	)
	high := highest
	if limit := numericLimit(c); limit > 0 && limit < high {
		high = limit
	}
	low := lowest
	if low >= high {
		low = 0
	}
	return g.roundTo(g.fake.Price(low, high), c)
}

// roundTo rounds a float to the column's declared scale, defaulting to 2.
func (g *Generator) roundTo(v float64, c db.Column) float64 {
	scale := c.NumericScale
	if c.DataType == db.TypeFloat || scale < 0 {
		scale = 2
	}
	if c.NumericPrecision == 0 && c.DataType == db.TypeNumeric {
		scale = 2 // unconstrained numeric: money-shaped is the useful default
	}

	// Clamp to what the column can physically hold.
	if limit := numericLimit(c); limit > 0 && v > limit {
		v = limit
	}

	factor := math.Pow(10, float64(scale))
	return math.Round(v*factor) / factor
}

// numericLimit is the largest value a numeric(p,s) column can hold, or 0 when
// the column is unconstrained. numeric(5,2) tops out just under 1000.
func numericLimit(c db.Column) float64 {
	if c.DataType != db.TypeNumeric || c.NumericPrecision <= 0 {
		return 0
	}
	digits := c.NumericPrecision - c.NumericScale
	if digits <= 0 {
		return 0
	}
	return math.Pow(10, float64(digits)) - 1
}

// integer returns a value inside both the requested range and the range the
// column's own integer width allows.
func (g *Generator) integer(c db.Column, low, high int) any {
	switch c.DataType {
	case db.TypeSmallint:
		if high > math.MaxInt16 {
			high = math.MaxInt16
		}
		if low > high {
			low = 0
		}
		return int16(g.fake.IntRange(low, high))
	case db.TypeInt:
		if high > math.MaxInt32 {
			high = math.MaxInt32
		}
		if low > high {
			low = 0
		}
		return int32(g.fake.IntRange(low, high))
	default:
		return int64(g.fake.IntRange(low, high))
	}
}

// jsonObject returns a small, valid JSON document.
func (g *Generator) jsonObject() any {
	doc := map[string]any{
		"source":  g.pick([]string{"web", "ios", "android", "import", "api"}),
		"version": g.fake.IntRange(1, 5),
		"tags":    []string{g.fake.Word(), g.fake.Word()},
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// ---------------------------------------------------------------------------
// type predicates
// ---------------------------------------------------------------------------

// isTextual reports whether a column can hold a generated string.
func isTextual(dataType string) bool {
	switch dataType {
	case db.TypeText, db.TypeVarchar, db.TypeChar, db.TypeEnum:
		return true
	default:
		return false
	}
}

// isTemporal reports whether a column holds a date or time.
func isTemporal(dataType string) bool {
	switch dataType {
	case db.TypeTimestamp, db.TypeTimestamptz, db.TypeDate, db.TypeTime:
		return true
	default:
		return false
	}
}

// isRealNumber reports whether a column holds a fractional number.
func isRealNumber(dataType string) bool {
	return dataType == db.TypeNumeric || dataType == db.TypeFloat
}

// statusNames are the exact column names that mean "a state from a small set".
var statusNames = map[string]bool{
	"status": true, "state": true, "type": true, "kind": true,
	"stage": true, "phase": true, "tier": true, "plan": true, "role": true,
}

// isStatusName reports whether a column name looks like a status-like column,
// either exactly or as a suffix such as order_status.
func isStatusName(column string) bool {
	name := strings.ToLower(column)
	if statusNames[name] {
		return true
	}
	for suffix := range statusNames {
		if strings.HasSuffix(name, "_"+suffix) {
			return true
		}
	}
	return false
}

// capitalize upper-cases the first letter of a word, leaving the rest alone.
// strings.Title is deprecated and title-cases every word, which is wrong here.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

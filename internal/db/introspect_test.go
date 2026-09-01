package db

import (
	"strings"
	"testing"
)

func TestNormalizeType(t *testing.T) {
	tests := []struct {
		dataType string
		typtype  string
		want     string
	}{
		// The spellings information_schema.columns actually reports.
		{"integer", "b", TypeInt},
		{"bigint", "b", TypeBigint},
		{"smallint", "b", TypeSmallint},
		{"text", "b", TypeText},
		{"character varying", "b", TypeVarchar},
		{"character", "b", TypeChar},
		{"boolean", "b", TypeBool},
		{"timestamp without time zone", "b", TypeTimestamp},
		{"timestamp with time zone", "b", TypeTimestamptz},
		{"time without time zone", "b", TypeTime},
		{"date", "b", TypeDate},
		{"uuid", "b", TypeUUID},
		{"numeric", "b", TypeNumeric},
		{"double precision", "b", TypeFloat},
		{"real", "b", TypeFloat},
		{"json", "b", TypeJSON},
		{"jsonb", "b", TypeJSONB},
		{"bytea", "b", TypeBytea},
		{"inet", "b", TypeInet},
		{"interval", "b", TypeInterval},

		// A user-defined type is an enum only when pg_type says so.
		{"USER-DEFINED", "e", TypeEnum},
		{"USER-DEFINED", "c", TypeUnknown},
		{"USER-DEFINED", "", TypeUnknown},

		// Anything unrecognized must be explicit, never silently wrong.
		{"ARRAY", "b", TypeUnknown},
		{"tsvector", "b", TypeUnknown},

		// Case and whitespace should not matter.
		{"  Integer ", "b", TypeInt},
	}

	for _, tc := range tests {
		t.Run(tc.dataType+"/"+tc.typtype, func(t *testing.T) {
			if got := NormalizeType(tc.dataType, tc.typtype); got != tc.want {
				t.Errorf("NormalizeType(%q, %q) = %q, want %q", tc.dataType, tc.typtype, got, tc.want)
			}
		})
	}
}

func TestParseCheckAllowedValues(t *testing.T) {
	tests := []struct {
		name       string
		def        string
		wantColumn string
		wantValues []string
		wantOK     bool
	}{
		{
			name: "how postgres actually renders IN on a varchar",
			// This is verbatim what pg_get_constraintdef returns for
			// CHECK (status IN ('pending','paid','shipped','cancelled','refunded'))
			def: `CHECK (((status)::text = ANY ((ARRAY['pending'::character varying, ` +
				`'paid'::character varying, 'shipped'::character varying, ` +
				`'cancelled'::character varying, 'refunded'::character varying])::text[])))`,
			wantColumn: "status",
			wantValues: []string{"pending", "paid", "shipped", "cancelled", "refunded"},
			wantOK:     true,
		},
		{
			name:       "text column, no cast",
			def:        `CHECK ((kind = ANY (ARRAY['a'::text, 'b'::text])))`,
			wantColumn: "kind",
			wantValues: []string{"a", "b"},
			wantOK:     true,
		},
		{
			name:       "plain IN rendering",
			def:        `CHECK (tier IN ('free', 'pro'))`,
			wantColumn: "tier",
			wantValues: []string{"free", "pro"},
			wantOK:     true,
		},
		{
			name:       "escaped quote inside a value",
			def:        `CHECK ((label = ANY (ARRAY['it''s'::text, 'plain'::text])))`,
			wantColumn: "label",
			wantValues: []string{"it's", "plain"},
			wantOK:     true,
		},
		{
			// A range check constrains nothing we can enumerate. Reporting
			// false makes the caller fall back to a distribution profile
			// rather than inventing values.
			name:   "numeric range is not an allowed-value set",
			def:    `CHECK ((qty > 0))`,
			wantOK: false,
		},
		{
			name:   "not null style check",
			def:    `CHECK ((email IS NOT NULL))`,
			wantOK: false,
		},
		{
			name:   "nonsense",
			def:    `something that is not a check at all`,
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			column, values, ok := ParseCheckAllowedValues(tc.def)
			if ok != tc.wantOK {
				t.Fatalf("ParseCheckAllowedValues() ok = %v, want %v (column=%q values=%v)",
					ok, tc.wantOK, column, values)
			}
			if !ok {
				return
			}
			if column != tc.wantColumn {
				t.Errorf("column = %q, want %q", column, tc.wantColumn)
			}
			if strings.Join(values, ",") != strings.Join(tc.wantValues, ",") {
				t.Errorf("values = %v, want %v", values, tc.wantValues)
			}
		})
	}
}

func TestSchemaHelpers(t *testing.T) {
	s := &Schema{Tables: map[string]*Table{
		"users":  {Name: "users", Columns: []Column{{Name: "id", IsPrimaryKey: true}}, PrimaryKey: []string{"id"}},
		"orders": {Name: "orders", FKs: []FK{{ChildColumn: "user_id", ParentTable: "users", ParentColumn: "id"}}},
		"order_items": {Name: "order_items", FKs: []FK{
			{ChildColumn: "order_id", ParentTable: "orders", ParentColumn: "id"},
			{ChildColumn: "product_id", ParentTable: "products", ParentColumn: "id"},
		}},
		"products": {Name: "products"},
	}}

	if got := s.FKCount(); got != 3 {
		t.Errorf("FKCount() = %d, want 3", got)
	}
	if got := strings.Join(s.SortedTableNames(), ","); got != "order_items,orders,products,users" {
		t.Errorf("SortedTableNames() = %s", got)
	}
	if got := strings.Join(s.Children("users"), ","); got != "orders" {
		t.Errorf("Children(users) = %v, want [orders]", s.Children("users"))
	}
	if got := strings.Join(s.Children("orders"), ","); got != "order_items" {
		t.Errorf("Children(orders) = %v, want [order_items]", s.Children("orders"))
	}
	if got := s.Children("order_items"); len(got) != 0 {
		t.Errorf("Children(order_items) = %v, want none", got)
	}
}

func TestTableHelpers(t *testing.T) {
	tbl := &Table{
		Name: "employees",
		Columns: []Column{
			{Name: "id", DataType: TypeBigint, IsPrimaryKey: true},
			{Name: "manager_id", DataType: TypeBigint, Nullable: true},
		},
		FKs:        []FK{{ChildColumn: "manager_id", ParentTable: "employees", ParentColumn: "id"}},
		PrimaryKey: []string{"id"},
	}

	if c, ok := tbl.Column("manager_id"); !ok || c.DataType != TypeBigint {
		t.Errorf("Column(manager_id) = (%+v, %v)", c, ok)
	}
	if _, ok := tbl.Column("nope"); ok {
		t.Error("Column(nope) reported a column that does not exist")
	}
	if fk, ok := tbl.FKFor("manager_id"); !ok || fk.ParentTable != "employees" {
		t.Errorf("FKFor(manager_id) = (%+v, %v)", fk, ok)
	}
	if _, ok := tbl.FKFor("id"); ok {
		t.Error("FKFor(id) reported a foreign key on the primary key")
	}
	if !tbl.SelfReferencing() {
		t.Error("SelfReferencing() = false, want true for manager_id -> employees.id")
	}
}

func TestColumnIsInteger(t *testing.T) {
	for _, ty := range []string{TypeInt, TypeBigint, TypeSmallint} {
		if !(Column{DataType: ty}).IsInteger() {
			t.Errorf("IsInteger() = false for %s", ty)
		}
	}
	for _, ty := range []string{TypeText, TypeNumeric, TypeUUID, TypeBool, TypeFloat} {
		if (Column{DataType: ty}).IsInteger() {
			t.Errorf("IsInteger() = true for %s", ty)
		}
	}
}

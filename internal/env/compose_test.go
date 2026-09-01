package env

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// writeCompose drops a compose file into a fresh temp dir and returns its path.
func writeCompose(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write compose file: %v", err)
	}
	return path
}

func TestParseComposeAcceptsBothEnvironmentForms(t *testing.T) {
	path := writeCompose(t, `
services:
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: appuser
      POSTGRES_PASSWORD: apppass
      POSTGRES_DB: appdb
    ports:
      - "5433:5432"
  web:
    image: nginx:alpine
    environment:
      - APP_ENV=production
      - FEATURE_FLAG=
    ports:
      - "8080:80"
`)

	c, err := ParseCompose(path)
	if err != nil {
		t.Fatalf("ParseCompose: %v", err)
	}

	db, ok := c.Services["db"]
	if !ok {
		t.Fatal("db service missing")
	}
	if got := db.Environment["POSTGRES_USER"]; got != "appuser" {
		t.Errorf("mapping form: POSTGRES_USER = %q, want %q", got, "appuser")
	}
	if got := db.Image; got != "postgres:16-alpine" {
		t.Errorf("image = %q, want postgres:16-alpine", got)
	}

	web := c.Services["web"]
	if got := web.Environment["APP_ENV"]; got != "production" {
		t.Errorf("list form: APP_ENV = %q, want %q", got, "production")
	}
	if got, ok := web.Environment["FEATURE_FLAG"]; !ok || got != "" {
		t.Errorf(`list form: FEATURE_FLAG = (%q, %v), want ("", true)`, got, ok)
	}
}

func TestParseComposeNormalizesLongPortSyntax(t *testing.T) {
	path := writeCompose(t, `
services:
  db:
    image: postgres:16
    ports:
      - target: 5432
        published: "5433"
        protocol: tcp
  cache:
    image: redis:7
    ports:
      - target: 6379
        published: "6380"
        host_ip: 127.0.0.1
`)

	c, err := ParseCompose(path)
	if err != nil {
		t.Fatalf("ParseCompose: %v", err)
	}

	if got, ok := c.Services["db"].HostPort("5432"); !ok || got != "5433" {
		t.Errorf(`db.HostPort("5432") = (%q, %v), want ("5433", true)`, got, ok)
	}
	if got, ok := c.Services["cache"].HostPort("6379"); !ok || got != "6380" {
		t.Errorf(`cache.HostPort("6379") = (%q, %v), want ("6380", true)`, got, ok)
	}
}

func TestServiceHostPort(t *testing.T) {
	tests := []struct {
		name          string
		ports         []string
		containerPort string
		want          string
		wantOK        bool
	}{
		{"plain mapping", []string{"5433:5432"}, "5432", "5433", true},
		{"bind address", []string{"127.0.0.1:8080:80"}, "80", "8080", true},
		{"protocol suffix", []string{"5433:5432/tcp"}, "5432", "5433", true},
		{"container port only", []string{"5432"}, "5432", "", false},
		{"not published", []string{"5433:5432"}, "6379", "", false},
		{"picks the right one", []string{"8080:80", "5433:5432"}, "5432", "5433", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Service{Ports: tc.ports}.HostPort(tc.containerPort)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("HostPort(%q) = (%q, %v), want (%q, %v)",
					tc.containerPort, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestParseComposeInterpolatesVariables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	body := `
services:
  db:
    image: postgres:${PG_TAG:-16-alpine}
    environment:
      POSTGRES_USER: ${DB_USER}
      POSTGRES_PASSWORD: ${DB_PASSWORD:-fallbackpass}
      POSTGRES_DB: ${DB_NAME-appdb}
    ports:
      - "${DB_PORT:-5433}:5432"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	// A .env sitting next to the compose file, as docker compose itself reads.
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("DB_USER=fromdotenv\n# comment\nDB_PORT=\"5544\"\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	c, err := ParseCompose(path)
	if err != nil {
		t.Fatalf("ParseCompose: %v", err)
	}

	db := c.Services["db"]
	if db.Image != "postgres:16-alpine" {
		t.Errorf("default expansion: image = %q, want postgres:16-alpine", db.Image)
	}
	if got := db.Environment["POSTGRES_USER"]; got != "fromdotenv" {
		t.Errorf(".env expansion: POSTGRES_USER = %q, want fromdotenv", got)
	}
	if got := db.Environment["POSTGRES_PASSWORD"]; got != "fallbackpass" {
		t.Errorf("default expansion: POSTGRES_PASSWORD = %q, want fallbackpass", got)
	}
	if got := db.Environment["POSTGRES_DB"]; got != "appdb" {
		t.Errorf("dash-default expansion: POSTGRES_DB = %q, want appdb", got)
	}
	if got, ok := db.HostPort("5432"); !ok || got != "5544" {
		t.Errorf(`quoted .env value: HostPort("5432") = (%q, %v), want ("5544", true)`, got, ok)
	}
}

func TestParseComposeProcessEnvironmentBeatsDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(path, []byte("services:\n  db:\n    image: postgres:${PG_TAG}\n"), 0o600); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("PG_TAG=from-dotenv\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Setenv("PG_TAG", "from-process")

	c, err := ParseCompose(path)
	if err != nil {
		t.Fatalf("ParseCompose: %v", err)
	}
	if got := c.Services["db"].Image; got != "postgres:from-process" {
		t.Errorf("image = %q, want postgres:from-process", got)
	}
}

func TestParseComposeErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := ParseCompose(filepath.Join(t.TempDir(), "nope.yml"))
		if err == nil {
			t.Fatal("want an error for a missing compose file")
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("error should wrap os.ErrNotExist so the CLI can hint; got %v", err)
		}
	})

	t.Run("no services", func(t *testing.T) {
		if _, err := ParseCompose(writeCompose(t, "volumes:\n  data:\n")); err == nil {
			t.Fatal("want an error for a compose file with no services")
		}
	})

	t.Run("malformed yaml", func(t *testing.T) {
		if _, err := ParseCompose(writeCompose(t, "services:\n  db:\n   image: [unclosed\n")); err == nil {
			t.Fatal("want an error for malformed yaml")
		}
	})
}

func TestServiceNamesIsSorted(t *testing.T) {
	c := &ComposeFile{Services: map[string]Service{
		"web": {Name: "web"}, "db": {Name: "db"}, "cache": {Name: "cache"},
	}}
	got := c.ServiceNames()
	want := []string{"cache", "db", "web"}
	if len(got) != len(want) {
		t.Fatalf("ServiceNames() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ServiceNames() = %v, want %v", got, want)
		}
	}
}

var projectNamePattern = regexp.MustCompile(`^pb_\d+_[0-9a-f]{8}$`)

// projectNameDraws is how many same-second names this test demands be distinct.
//
// This is a birthday-bound problem, so the number is not arbitrary. With the
// suffix at 4 bytes (2^32 values), 200 draws collide with probability about
// 200^2 / (2 * 2^32), roughly 1 in 200,000 - rare enough that a failure here
// means the entropy shrank, not that the test got unlucky. Raising this count
// or shrinking projectRandomBytes makes the test flaky; do neither without
// redoing the arithmetic.
const projectNameDraws = 200

func TestNewProjectNameIsUniqueAndWellFormed(t *testing.T) {
	now := time.Unix(1720001234, 0)

	seen := map[string]bool{}
	for i := 0; i < projectNameDraws; i++ {
		// Same clock every time: only the random suffix can keep these apart,
		// which is exactly the isolation guarantee two quick `up` runs need.
		name := NewProjectName(now)
		if !projectNamePattern.MatchString(name) {
			t.Fatalf("project name %q does not match %v", name, projectNamePattern)
		}
		if seen[name] {
			t.Fatalf("duplicate project name %q on iteration %d", name, i)
		}
		seen[name] = true
	}
}

func TestMaskURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"masks the password",
			"postgres://appuser:apppass@localhost:5433/appdb",
			"postgres://appuser:****@localhost:5433/appdb",
		},
		{
			"keeps query parameters",
			"postgres://u:secret@localhost:5432/db?sslmode=disable",
			"postgres://u:****@localhost:5432/db?sslmode=disable",
		},
		{"no password to mask", "postgres://appuser@localhost:5433/appdb", "postgres://appuser@localhost:5433/appdb"},
		{"no credentials at all", "postgres://localhost:5433/appdb", "postgres://localhost:5433/appdb"},
		{"empty", "", ""},
		{"http app url passes through", "http://localhost:8080", "http://localhost:8080"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaskURL(tc.in); got != tc.want {
				t.Errorf("MaskURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMaskURLNeverLeaksAPassword(t *testing.T) {
	// The whole point of the function: whatever the input shape, the secret must
	// not survive into the output (build spec section 12.2).
	const password = "sup3rs3cr3t"
	inputs := []string{
		"postgres://user:" + password + "@localhost:5432/db",
		"postgres://user:" + password + "@localhost:5432/db?sslmode=require",
		"://user:" + password + "@broken",
		"user:" + password + "@host/db",
	}
	for _, in := range inputs {
		if got := MaskURL(in); regexp.MustCompile(regexp.QuoteMeta(password)).MatchString(got) {
			t.Errorf("MaskURL(%q) leaked the password: %q", in, got)
		}
	}
}

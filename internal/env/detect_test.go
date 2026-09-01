package env

import "testing"

func TestFindPostgres(t *testing.T) {
	tests := []struct {
		name     string
		services map[string]Service
		want     string
		wantOK   bool
	}{
		{
			name: "the obvious case",
			services: map[string]Service{
				"db":  {Name: "db", Image: "postgres:16-alpine"},
				"web": {Name: "web", Image: "nginx:alpine"},
			},
			want: "db", wantOK: true,
		},
		{
			name: "flavors count",
			services: map[string]Service{
				"tsdb": {Name: "tsdb", Image: "timescale/timescaledb:latest-pg16"},
			},
			want: "tsdb", wantOK: true,
		},
		{
			name: "pgvector counts",
			services: map[string]Service{
				"vec": {Name: "vec", Image: "pgvector/pgvector:pg16"},
			},
			want: "vec", wantOK: true,
		},
		{
			name: "no postgres at all",
			services: map[string]Service{
				"web":   {Name: "web", Image: "nginx:alpine"},
				"cache": {Name: "cache", Image: "redis:7"},
			},
			wantOK: false,
		},
		{
			name: "the one publishing 5432 wins",
			services: map[string]Service{
				// Alphabetically first, but publishes nothing.
				"analytics": {Name: "analytics", Image: "postgres:16"},
				"primary":   {Name: "primary", Image: "postgres:16", Ports: []string{"5433:5432"}},
			},
			want: "primary", wantOK: true,
		},
		{
			name: "deterministic when neither publishes",
			services: map[string]Service{
				"zebra": {Name: "zebra", Image: "postgres:16"},
				"alpha": {Name: "alpha", Image: "postgres:16"},
			},
			want: "alpha", wantOK: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &ComposeFile{Path: "docker-compose.yml", Services: tc.services}
			got, ok := c.FindPostgres()
			if ok != tc.wantOK {
				t.Fatalf("FindPostgres() ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got.Name != tc.want {
				t.Errorf("FindPostgres() = %q, want %q", got.Name, tc.want)
			}
		})
	}
}

func TestFindPostgresIsDeterministic(t *testing.T) {
	// Map iteration order is randomized, so the same compose file must not
	// resolve to a different service between runs.
	c := &ComposeFile{Services: map[string]Service{
		"zebra": {Name: "zebra", Image: "postgres:16"},
		"alpha": {Name: "alpha", Image: "postgres:16"},
		"mango": {Name: "mango", Image: "postgres:16"},
	}}
	first, _ := c.FindPostgres()
	for i := 0; i < 50; i++ {
		got, _ := c.FindPostgres()
		if got.Name != first.Name {
			t.Fatalf("FindPostgres() returned %q then %q on iteration %d", first.Name, got.Name, i)
		}
	}
}

func TestPostgresURL(t *testing.T) {
	tests := []struct {
		name    string
		service Service
		want    string
		wantErr bool
	}{
		{
			name: "full credentials",
			service: Service{
				Name:  "db",
				Image: "postgres:16-alpine",
				Environment: map[string]string{
					"POSTGRES_USER": "appuser", "POSTGRES_PASSWORD": "apppass", "POSTGRES_DB": "appdb",
				},
				Ports: []string{"5433:5432"},
			},
			want: "postgres://appuser:apppass@localhost:5433/appdb",
		},
		{
			name: "official image defaults: user postgres, db follows user",
			service: Service{
				Name: "db", Image: "postgres:16",
				Environment: map[string]string{"POSTGRES_PASSWORD": "secret"},
				Ports:       []string{"5432:5432"},
			},
			want: "postgres://postgres:secret@localhost:5432/postgres",
		},
		{
			name: "trust auth, no password",
			service: Service{
				Name: "db", Image: "postgres:16",
				Environment: map[string]string{"POSTGRES_USER": "dev", "POSTGRES_DB": "devdb"},
				Ports:       []string{"5555:5432"},
			},
			want: "postgres://dev@localhost:5555/devdb",
		},
		{
			name: "bind address on the mapping",
			service: Service{
				Name: "db", Image: "postgres:16",
				Environment: map[string]string{"POSTGRES_USER": "u", "POSTGRES_PASSWORD": "p", "POSTGRES_DB": "d"},
				Ports:       []string{"127.0.0.1:6000:5432"},
			},
			want: "postgres://u:p@localhost:6000/d",
		},
		{
			name: "not published to the host",
			service: Service{
				Name: "db", Image: "postgres:16",
				Environment: map[string]string{"POSTGRES_PASSWORD": "p"},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &ComposeFile{Path: "docker-compose.yml", Services: map[string]Service{tc.service.Name: tc.service}}
			got, err := c.PostgresURL()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("PostgresURL() = %q, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("PostgresURL(): %v", err)
			}
			if got != tc.want {
				t.Errorf("PostgresURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPostgresURLWithNoPostgresService(t *testing.T) {
	c := &ComposeFile{Path: "docker-compose.yml", Services: map[string]Service{
		"web": {Name: "web", Image: "nginx:alpine", Ports: []string{"8080:80"}},
	}}
	if _, err := c.PostgresURL(); err == nil {
		t.Fatal("PostgresURL() with no Postgres service returned nil, want an error")
	}
}

func TestPostgresURLIsMaskable(t *testing.T) {
	// The URL goes straight into printed output, so it must survive masking
	// with the password gone (spec section 12.2).
	c := &ComposeFile{Services: map[string]Service{"db": {
		Name: "db", Image: "postgres:16",
		Environment: map[string]string{"POSTGRES_USER": "appuser", "POSTGRES_PASSWORD": "hunter2", "POSTGRES_DB": "appdb"},
		Ports:       []string{"5433:5432"},
	}}}

	url, err := c.PostgresURL()
	if err != nil {
		t.Fatalf("PostgresURL(): %v", err)
	}
	masked := MaskURL(url)
	if masked != "postgres://appuser:****@localhost:5433/appdb" {
		t.Errorf("MaskURL(PostgresURL()) = %q", masked)
	}
}

func TestAppURL(t *testing.T) {
	tests := []struct {
		name     string
		services map[string]Service
		want     string
	}{
		{
			name: "skips the database",
			services: map[string]Service{
				"db":  {Name: "db", Image: "postgres:16", Ports: []string{"5433:5432"}},
				"web": {Name: "web", Image: "nginx:alpine", Ports: []string{"8080:80"}},
			},
			want: "http://localhost:8080",
		},
		{
			name: "nothing published but the database",
			services: map[string]Service{
				"db":     {Name: "db", Image: "postgres:16", Ports: []string{"5433:5432"}},
				"worker": {Name: "worker", Image: "myapp/worker"},
			},
			want: "",
		},
		{
			name:     "no services publish anything",
			services: map[string]Service{"worker": {Name: "worker", Image: "myapp/worker"}},
			want:     "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &ComposeFile{Services: tc.services}
			if got := c.AppURL(); got != tc.want {
				t.Errorf("AppURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

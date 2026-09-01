package env

import (
	"fmt"
	"net/url"
	"strings"
)

// postgresImageHints are the image-name fragments that identify a service as
// Postgres. Flavors like timescale and postgis speak the same wire protocol
// and introspect identically, so they count.
var postgresImageHints = []string{"postgres", "postgis", "timescale", "pgvector"}

// The container-side port and environment variables of the official Postgres
// image, which nearly every Compose+Postgres repo uses verbatim.
const (
	postgresContainerPort = "5432"
	envPostgresUser       = "POSTGRES_USER"
	envPostgresPassword   = "POSTGRES_PASSWORD"
	envPostgresDB         = "POSTGRES_DB"

	// Defaults the official image applies when the variable is unset.
	defaultPostgresUser = "postgres"
)

// FindPostgres returns the compose service running Postgres.
//
// When several services qualify, the one whose published port is the
// conventional 5432 mapping wins, then the alphabetically first, so the answer
// is deterministic rather than dependent on Go's map iteration order.
func (c *ComposeFile) FindPostgres() (Service, bool) {
	var candidates []Service
	for _, name := range c.ServiceNames() {
		if isPostgresImage(c.Services[name].Image) {
			candidates = append(candidates, c.Services[name])
		}
	}
	if len(candidates) == 0 {
		return Service{}, false
	}

	for _, s := range candidates {
		if _, ok := s.HostPort(postgresContainerPort); ok {
			return s, true
		}
	}
	return candidates[0], true
}

// isPostgresImage reports whether an image reference looks like Postgres.
func isPostgresImage(image string) bool {
	image = strings.ToLower(image)
	for _, hint := range postgresImageHints {
		if strings.Contains(image, hint) {
			return true
		}
	}
	return false
}

// PostgresURL derives the connection URL for the compose file's Postgres
// service: postgres://user:password@localhost:<hostPort>/<database>.
//
// Credentials and database name come from the service's POSTGRES_* environment
// variables, falling back to the official image's own defaults, and the port
// comes from the published mapping of container port 5432. This is what makes
// the zero-config path work (build spec section 12.4).
func (c *ComposeFile) PostgresURL() (string, error) {
	svc, ok := c.FindPostgres()
	if !ok {
		return "", fmt.Errorf("no Postgres service found in %s", c.Path)
	}

	port, ok := svc.HostPort(postgresContainerPort)
	if !ok {
		return "", fmt.Errorf("service %q does not publish port %s to the host", svc.Name, postgresContainerPort)
	}

	user := svc.Environment[envPostgresUser]
	if user == "" {
		user = defaultPostgresUser
	}
	password := svc.Environment[envPostgresPassword]
	// The official image defaults POSTGRES_DB to POSTGRES_USER.
	database := svc.Environment[envPostgresDB]
	if database == "" {
		database = user
	}

	u := &url.URL{
		Scheme: "postgres",
		Host:   "localhost:" + port,
		Path:   "/" + database,
	}
	if password == "" {
		u.User = url.User(user)
	} else {
		u.User = url.UserPassword(user, password)
	}
	return u.String(), nil
}

// AppURL is a best-effort guess at the URL a developer would open in a browser:
// the first published host port that does not belong to the Postgres service.
// It returns "" when nothing obvious is published, and callers simply omit the
// line rather than printing a guess that does not work.
func (c *ComposeFile) AppURL() string {
	pg, hasPostgres := c.FindPostgres()

	for _, name := range c.ServiceNames() {
		if hasPostgres && name == pg.Name {
			continue
		}
		if ports := c.Services[name].PublishedPorts(); len(ports) > 0 {
			return "http://localhost:" + ports[0]
		}
	}
	return ""
}

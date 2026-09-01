package env

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Service is one service of a compose file, reduced to the fields Playbrige
// needs: which image it runs, its environment, and its published ports.
type Service struct {
	// Name is the service key in the compose file.
	Name string
	// Image is the image reference, e.g. "postgres:16-alpine".
	Image string
	// Environment holds the service's environment variables. Both the mapping
	// form and the "- KEY=VALUE" list form parse into this.
	Environment map[string]string
	// Ports holds the published port mappings in short syntax, normalized from
	// either the short or the long form, e.g. "5433:5432" or "127.0.0.1:8080:80".
	Ports []string
}

// ComposeFile is a parsed docker-compose.yml.
type ComposeFile struct {
	// Path is the file the definition was read from.
	Path string
	// Services is keyed by service name.
	Services map[string]Service
}

// ParseCompose reads and parses a compose file.
//
// Compose's `${VAR}` interpolation is applied first, using the process
// environment plus a `.env` file sitting next to the compose file, because that
// is what `docker compose` itself does and real repositories rely on it. An
// unresolved variable expands to the empty string, exactly as Compose does.
func ParseCompose(path string) (*ComposeFile, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path comes from the user's own --file flag
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no compose file at %s: %w", path, os.ErrNotExist)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	vars := dotEnv(filepath.Join(filepath.Dir(path), ".env"))
	interpolated := interpolate(string(raw), vars)

	var doc struct {
		Services map[string]rawService `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(interpolated), &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(doc.Services) == 0 {
		return nil, fmt.Errorf("parse %s: no services defined", path)
	}

	c := &ComposeFile{Path: path, Services: make(map[string]Service, len(doc.Services))}
	for name, rs := range doc.Services {
		c.Services[name] = Service{
			Name:        name,
			Image:       rs.Image,
			Environment: rs.Environment.values,
			Ports:       rs.Ports.values,
		}
	}
	return c, nil
}

// ServiceNames returns every service name in deterministic alphabetical order.
func (c *ComposeFile) ServiceNames() []string {
	names := make([]string, 0, len(c.Services))
	for name := range c.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// hostPortPattern matches a normalized short-syntax port mapping, capturing the
// optional bind address, the published host port, and the container port.
var hostPortPattern = regexp.MustCompile(`^(?:(\[[^\]]+\]|[0-9.]+):)?(\d+):(\d+)(?:/\w+)?$`)

// HostPort returns the host port that publishes the given container port.
// It reports false when the container port is not published, which includes the
// short form that names only a container port (Docker then picks a random host
// port that Playbrige cannot predict).
func (s Service) HostPort(containerPort string) (string, bool) {
	for _, p := range s.Ports {
		m := hostPortPattern.FindStringSubmatch(strings.TrimSpace(p))
		if m == nil {
			continue
		}
		if m[3] == containerPort {
			return m[2], true
		}
	}
	return "", false
}

// PublishedPorts returns every host port this service publishes, in declaration
// order.
func (s Service) PublishedPorts() []string {
	var out []string
	for _, p := range s.Ports {
		if m := hostPortPattern.FindStringSubmatch(strings.TrimSpace(p)); m != nil {
			out = append(out, m[2])
		}
	}
	return out
}

// projectRandomBytes is the amount of entropy appended to a project name.
//
// Four bytes, not two. Collisions only matter within a single second, since the
// timestamp separates everything else, but the birthday bound is unforgiving:
// with 2 bytes (65,536 values) just 200 names in the same second collide about
// 26% of the time, and a collision means two environments share a project - so
// one `down` tears down both. 4 bytes takes that to roughly 1 in 200,000.
const projectRandomBytes = 4

// NewProjectName returns a fresh, isolated compose project name of the form
// "pb_<unix>_<random>".
//
// The timestamp makes a name readable and sortable; the random suffix is what
// actually guarantees the build spec's isolation requirement, since two `up`
// runs within the same second would otherwise collide and share a stack. The
// clock is passed in so tests can pin it.
func NewProjectName(now time.Time) string {
	buf := make([]byte, projectRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand cannot fail in practice; if it ever does, the timestamp
		// alone is still a valid project name.
		return fmt.Sprintf("pb_%d", now.Unix())
	}
	return fmt.Sprintf("pb_%d_%s", now.Unix(), hex.EncodeToString(buf))
}

// maskedPassword is what replaces a password in any user-visible URL.
const maskedPassword = "****"

// credentialPattern matches an embedded "user:password@" credential, either
// after a "//" or at the start of a string.
//
// This is deliberately string surgery rather than url.Parse: a string like
// "user:pass@host/db" parses as an *opaque* URL whose User is nil, so a
// Parse-based mask would hand the password straight back. Password characters
// stop at "@", "/", "?", "#" or whitespace, which is exactly what a URL allows
// unencoded.
var credentialPattern = regexp.MustCompile(`(^|//)([^/?#@:\s]+):([^/?#@\s]*)@`)

// MaskURL returns rawurl with any embedded password replaced by "****", for
// printing. URLs with no credentials, or with a username but no password, are
// returned unchanged.
//
// Every path that prints a connection string goes through here: leaking the
// user's database password into a terminal, a log, or a screenshot is the one
// unrecoverable mistake this tool could make (build spec section 12.2).
func MaskURL(rawurl string) string {
	if rawurl == "" {
		return ""
	}
	return credentialPattern.ReplaceAllString(rawurl, "${1}${2}:"+maskedPassword+"@")
}

// Up starts the compose stack in the background under the given project name.
// Output is captured rather than streamed so a successful run stays quiet and
// matches the build spec section 7 summary; on failure the whole of Docker's
// output is attached to the error, which is what the developer needs.
func Up(ctx context.Context, file, project string) error {
	_, err := runDocker(ctx, "compose", "-p", project, "-f", file, "up", "-d")
	return err
}

// Down stops the stack and removes its containers, networks, and volumes.
// The -v flag is what makes teardown a genuine clean slate, and
// --remove-orphans catches containers left by an edited compose file.
func Down(ctx context.Context, file, project string) error {
	_, err := runDocker(ctx, "compose", "-p", project, "-f", file, "down", "-v", "--remove-orphans")
	return err
}

// Logs streams the stack's container logs to stdout, optionally following them
// and optionally narrowed to the named services. It is a thin passthrough: the
// child process inherits the terminal so Ctrl-C behaves as the user expects.
func Logs(ctx context.Context, file, project string, follow bool, services []string) error {
	args := []string{"compose", "-p", project, "-f", file, "logs"}
	if follow {
		args = append(args, "-f")
	}
	args = append(args, services...)

	cmd := exec.CommandContext(ctx, dockerBinary, args...) //nolint:gosec // fixed binary, arguments are not shell-interpreted
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		// Following logs and pressing Ctrl-C is a normal exit, not a failure:
		// the context is canceled and docker is killed, which is what the user
		// asked for.
		if ctx.Err() != nil {
			return nil //nolint:nilerr // cancellation is the user pressing Ctrl-C
		}
		return fmt.Errorf("docker compose logs: %w", err)
	}
	return nil
}

// RunningContainers returns the names of the project's running containers.
func RunningContainers(ctx context.Context, project string) ([]string, error) {
	return dockerLines(ctx, "ps",
		"--filter", "label=com.docker.compose.project="+project,
		"--format", "{{.Names}}")
}

// ProjectResources returns the project's containers (running or not), networks,
// and volumes. `down` uses it to prove the teardown left nothing behind.
func ProjectResources(ctx context.Context, project string) (containers, networks, volumes []string, err error) {
	label := "label=com.docker.compose.project=" + project

	if containers, err = dockerLines(ctx, "ps", "-a", "--filter", label, "--format", "{{.Names}}"); err != nil {
		return nil, nil, nil, err
	}
	if networks, err = dockerLines(ctx, "network", "ls", "--filter", label, "--format", "{{.Name}}"); err != nil {
		return nil, nil, nil, err
	}
	if volumes, err = dockerLines(ctx, "volume", "ls", "--filter", label, "--format", "{{.Name}}"); err != nil {
		return nil, nil, nil, err
	}
	return containers, networks, volumes, nil
}

// CheckDocker reports whether the docker CLI is present and its engine is
// reachable, so failures can be explained before anything else is attempted.
func CheckDocker(ctx context.Context) error {
	if _, err := exec.LookPath(dockerBinary); err != nil {
		return fmt.Errorf("docker CLI not found on PATH: %w", err)
	}
	if _, err := runDocker(ctx, "version", "--format", "{{.Server.Version}}"); err != nil {
		return fmt.Errorf("docker engine is not reachable: %w", err)
	}
	return nil
}

// dockerBinary is the CLI Playbrige shells out to. The build spec (section 3)
// deliberately chooses the CLI over the Docker SDK for the MVP.
const dockerBinary = "docker"

// runDocker runs a docker subcommand and returns its combined output. Errors
// carry the command and Docker's own message, which is the actionable part.
func runDocker(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, dockerBinary, args...) //nolint:gosec // fixed binary, arguments are not shell-interpreted
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return "", fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("docker %s: %w\n%s", strings.Join(args, " "), err, msg)
	}
	return string(out), nil
}

// dockerLines runs a docker subcommand and splits its output into non-empty,
// trimmed lines.
func dockerLines(ctx context.Context, args ...string) ([]string, error) {
	out, err := runDocker(ctx, args...)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// ---------------------------------------------------------------------------
// YAML shapes
// ---------------------------------------------------------------------------

// rawService mirrors the subset of a compose service Playbrige reads.
type rawService struct {
	Image       string      `yaml:"image"`
	Environment environment `yaml:"environment"`
	Ports       ports       `yaml:"ports"`
}

// environment accepts both compose environment forms: the mapping
// (`KEY: value`) and the list (`- KEY=value`, or bare `- KEY` meaning
// "inherit from the host").
type environment struct {
	values map[string]string
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (e *environment) UnmarshalYAML(node *yaml.Node) error {
	e.values = map[string]string{}

	switch node.Kind {
	case yaml.MappingNode:
		var m map[string]*string
		if err := node.Decode(&m); err != nil {
			return fmt.Errorf("environment: %w", err)
		}
		for k, v := range m {
			if v == nil {
				e.values[k] = os.Getenv(k)
				continue
			}
			e.values[k] = *v
		}
	case yaml.SequenceNode:
		var list []string
		if err := node.Decode(&list); err != nil {
			return fmt.Errorf("environment: %w", err)
		}
		for _, item := range list {
			key, value, found := strings.Cut(item, "=")
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			if !found {
				e.values[key] = os.Getenv(key)
				continue
			}
			e.values[key] = value
		}
	default:
		// No environment, or a shape compose itself would reject. Either way
		// there is nothing to read; auto-detection falls back to its defaults.
	}
	return nil
}

// ports accepts both compose port forms: the short string ("5433:5432") and the
// long mapping ({target: 5432, published: "5433"}). Long entries are normalized
// into short syntax so the rest of the package only handles one shape.
type ports struct {
	values []string
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (p *ports) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return nil
	}

	for _, item := range node.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			p.values = append(p.values, item.Value)
		case yaml.MappingNode:
			var long struct {
				Target    string `yaml:"target"`
				Published string `yaml:"published"`
				Protocol  string `yaml:"protocol"`
				HostIP    string `yaml:"host_ip"`
			}
			if err := item.Decode(&long); err != nil {
				return fmt.Errorf("ports: %w", err)
			}
			if long.Target == "" {
				continue
			}
			var b strings.Builder
			if long.HostIP != "" {
				b.WriteString(long.HostIP)
				b.WriteByte(':')
			}
			if long.Published != "" {
				b.WriteString(long.Published)
				b.WriteByte(':')
			}
			b.WriteString(long.Target)
			if long.Protocol != "" && long.Protocol != "tcp" {
				b.WriteByte('/')
				b.WriteString(long.Protocol)
			}
			p.values = append(p.values, b.String())
		default:
			// Not a shape compose accepts for a port entry; skip it.
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// ${VAR} interpolation
// ---------------------------------------------------------------------------

// variablePattern matches the compose interpolation forms Playbrige supports:
// $VAR, ${VAR}, ${VAR:-default} and ${VAR-default}.
var variablePattern = regexp.MustCompile(`\$(?:(\w+)|\{(\w+)(?::?-([^}]*))?\})`)

// interpolate expands compose variables in src, preferring the process
// environment and falling back to vars (the .env file). This mirrors what
// `docker compose` does, so a compose file that works for the user works here.
func interpolate(src string, vars map[string]string) string {
	return variablePattern.ReplaceAllStringFunc(src, func(match string) string {
		m := variablePattern.FindStringSubmatch(match)
		name := m[1]
		if name == "" {
			name = m[2]
		}
		if v, ok := os.LookupEnv(name); ok && v != "" {
			return v
		}
		if v, ok := vars[name]; ok && v != "" {
			return v
		}
		return m[3] // the default, or "" when there was none
	})
}

// dotEnv reads a .env file into a map. A missing or malformed file yields an
// empty map: it is an optional convenience, never a hard requirement.
func dotEnv(path string) map[string]string {
	vars := map[string]string{}

	data, err := os.ReadFile(path) //nolint:gosec // path is derived from the user's own compose file location
	if err != nil {
		return vars
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if key != "" {
			vars[key] = value
		}
	}
	return vars
}

# Playbrige

Type one command in a repo that has a `docker-compose.yml` with Postgres in it, and about half a
minute later the app is running in front of you with a database full of data that looks like it came
from real customers — thousands of users with plausible names and emails, orders that belong to those
users, line items that belong to those orders, a status column where "churned" is rare instead of
exactly one-quarter of the rows. Every foreign key points at a row that actually exists. When you are
finished, one more command and it is all gone: containers, networks, volumes, nothing left behind.
Playbrige is a single Go binary that does this locally against your own Docker and your own compose
file — it starts an isolated copy of your stack, reads your schema, works out which tables are
parents, and bulk-loads FK-correct synthetic data with Postgres `COPY`. Targets: the whole cycle in
under ~30 seconds, ~10,000 rows seeded in under ~2 seconds.

Nothing leaves your machine. There is no server, no cloud, no account, no daemon.

---

## Status

**MVP under construction.** This repo is being built phase by phase against
[`docs/PLAYBRIGE_MVP_BUILD_SPEC.md`](docs/PLAYBRIGE_MVP_BUILD_SPEC.md), which is the source of truth
for behavior. Today the command tree exists and the output printers are real;
the real behavior lands in the phases below, so expect `up` to print placeholders until Phase 6.

- [x] **Phase 0** — Skeleton CLI: cobra root, six subcommands, package skeletons, CI, fixture app
- [x] **Phase 1** — Compose up/down with isolation (unique project name, `.playbrige/state.json`)
- [ ] **Phase 2** — Connect to Postgres (auto-detect from compose, readiness retry loop)
- [ ] **Phase 3** — Schema introspection (tables, columns, types, PKs, FKs)
- [ ] **Phase 4** — Topological ordering (parents before children, cycle breaking)
- [ ] **Phase 5** — FK-aware seeding via `COPY` — the core
- [ ] **Phase 6** — Wire it together: the `up` chain, `reset`, `status`, output polish

Each phase is one branch and one PR, and ticks its own box on the way in.

---

## Install / build

The module path is `github.com/shivansh-source/stateful-playwright`, but the binary is `playbrige`.
The root package is `main`, so a bare `go install` produces a binary named after the module — either
rename it, or use the Makefile, which names it correctly:

```sh
# Makefile route (recommended) — produces ./bin/playbrige
make build

# same thing by hand
go build -o bin/playbrige .

# go install route — installs $GOBIN/stateful-playwright, rename it to playbrige
go install github.com/shivansh-source/stateful-playwright@latest
```

Put `bin/` on your `PATH` or invoke `./bin/playbrige` directly. While developing, `go run . up` works
too.

## Usage

```sh
playbrige up          # start an isolated stack, wait for Postgres, seed it, print the summary
playbrige down        # tear the stack down (containers, networks, volumes) and clear state
playbrige status      # what is running: project, services, tables, rows seeded, URLs
playbrige reset       # truncate every table and re-seed, same stack
playbrige seed        # seed only, against a stack that is already up
playbrige logs [svc]  # stream container logs (passthrough to docker compose logs)
```

Global flags, valid on every command:

| Flag | Default | Meaning |
|---|---|---|
| `--file`, `-f` | `docker-compose.yml` | Compose file to run |
| `--dir` | `.` | Project directory — where `.playbrige/` and `playbrige.yml` live |
| `--db-url` | auto-detected | Override the Postgres connection string |
| `--rows` | `10000` | Default rows per table, before per-table heuristics |

`seed` additionally takes `--truncate`, to empty the tables before loading new rows.

### What `playbrige up` prints on success

```
  ✔ Environment pb_1720001234 started (3 services)
  ✔ Database ready
  ✔ Schema read: 14 tables, 9 foreign keys
  ✔ Seeded 10,000 rows across 14 tables in 0.8s
  → App:  http://localhost:8080
  → DB:   postgres://user:****@localhost:5433/appdb   (password masked)
  Ready. Run 'playbrige down' when finished.
```

Passwords are masked in every printed URL. On failure you get the step that broke plus something
actionable, e.g. `Database not ready after 30s — is the postgres service healthy? Run 'playbrige logs db'`.

## Requirements

| | |
|---|---|
| Go | 1.25 or newer — to build; not needed to run the binary |
| Docker | with Compose v2. Playbrige shells out to `docker compose`, not `docker-compose` |
| Your repo | a `docker-compose.yml` with a Postgres service that publishes a host port |

Developed on Windows, CI runs ubuntu-latest; the tool is OS-portable.

## Zero config

The happy path takes no configuration at all. Playbrige reads your compose file, finds the service
whose image looks like Postgres, pulls `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` out of
its environment, reads the published host port out of its `ports` mapping, and connects. Row counts
and value distributions have sane defaults.

`playbrige.yml` in the project directory is the escape hatch, for what auto-detection cannot work out
or when you want different volumes of data. Every field is optional and a missing file is not an
error:

```yaml
# All fields optional. Auto-detection is the default path.
database:
  url: postgres://user:pass@localhost:5433/appdb   # override connection
app:
  url: http://localhost:8080                        # what to print as the App URL
seed:
  default_rows: 5000
  tables:
    users: 2000          # per-table row count override
    orders: 20000
  distributions:
    users.status: { active: 0.6, trial: 0.3, churned: 0.1 }
```

## Try it against the bundled fixture

`testapp/` is the deliberately boring Compose + Postgres repo every phase's acceptance test runs
against: a `db` service on host port `5433`, a placeholder `web` service on `8080` so App URL
detection has a non-db port to find, and a schema (`categories`, `users`, `employees`, `products`,
`promotions`, `orders`, `order_items`) that exercises multi-level FK chains, a self-referencing FK, a
status column with a skewed distribution, timestamps, money, and name/email generation.

```sh
make build
cd testapp
../bin/playbrige up
psql "postgres://appuser:apppass@localhost:5433/appdb" -c 'table orders limit 5;'
../bin/playbrige down
```

Until Phase 6 lands the commands run but the chain is not wired end to end — see Status above.

## What this is NOT

Deliberately out of scope for the MVP. These are not oversights; see [`LATER.md`](LATER.md) for the
full parking lot and the reasoning.

- **Not Kubernetes.** Local Docker Compose only.
- **Not multi-database.** Postgres only — no MySQL, no SQLite, no data graphs spanning several
  databases.
- **Not a cloud product.** No hosted control plane, runner agent, tunnel, public URL, or deployment.
- **Not a dashboard.** No web UI, accounts, auth, teams, or billing. It is a CLI.
- **Not a daemon.** Nothing runs in the background between commands.
- **Not a production-data tool.** It generates synthetic data; it never samples, subsets, or
  anonymizes a real database.
- **Not test infrastructure.** No CI integration, GitHub App, PR environments, MCP server, agent API,
  login/session injection, snapshots, branching, or flaky-test triage.

## Contributing

- One branch per phase, one PR per phase, in phase order. A phase merges only once its acceptance
  test in the spec passes.
- Stay inside the phase you are on. Anything tempting from a later phase, or from `LATER.md`, stays
  out.
- Package signatures are frozen by the build contract — implement them, do not rename or reshape
  them.
- CI must be green before merge: **fmt, vet, lint, test, tidy, build**. Locally that is:

```sh
gofmt -l .          # must print nothing
go vet ./...
go test ./...
go mod tidy         # must leave go.mod and go.sum unchanged
go build ./...
```

- Never log or persist a real password; mask them in printed output. Use `COPY`, never row-by-row
  `INSERT`. Every foreign key value must come from a parent row that exists.

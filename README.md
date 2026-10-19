# Loam

Type one command in a repo that has a `docker-compose.yml` with Postgres in it, and about half a
minute later the app is running in front of you with a database full of data that looks like it came
from real customers — thousands of users with plausible names and emails, orders that belong to those
users, line items that belong to those orders, a status column where "churned" is rare instead of
exactly one-quarter of the rows. Every foreign key points at a row that actually exists. When you are
finished, one more command and it is all gone: containers, networks, volumes, nothing left behind.
Loam is a single Go binary that does this locally against your own Docker and your own compose
file — it starts an isolated copy of your stack, reads your schema, works out which tables are
parents, and bulk-loads FK-correct synthetic data with Postgres `COPY`. Targets: the whole cycle in
under ~30 seconds, ~10,000 rows seeded in under ~2 seconds.

Nothing leaves your machine. There is no server, no cloud, no account, no daemon.

---

## Status

**MVP complete.** All seven phases are in, each verified against its own acceptance test in
[`docs/BUILD_SPEC.md`](docs/BUILD_SPEC.md), which is the source of truth
for behavior. `loam up` on a Compose + Postgres repo brings the stack up, seeds it with
foreign-key-correct data, and prints where to reach it; `loam down` leaves nothing behind.

- [x] **Phase 0** — Skeleton CLI: cobra root, six subcommands, package skeletons, CI, fixture app
- [x] **Phase 1** — Compose up/down with isolation (unique project name, `.loam/state.json`)
- [x] **Phase 2** — Connect to Postgres (auto-detect from compose, readiness retry loop)
- [x] **Phase 3** — Schema introspection (tables, columns, types, PKs, FKs)
- [x] **Phase 4** — Topological ordering (parents before children, cycle breaking)
- [x] **Phase 5** — FK-aware seeding via `COPY` — the core
- [x] **Phase 6** — Wire it together: the `up` chain, `reset`, `status`, output polish

Each phase was one branch and one PR, merged only once its acceptance test passed.

---

## Install / build

The module path is `github.com/loamdev/loam`, but the binary is `loam`.
The root package is `main`, so a bare `go install` produces a binary named after the module — either
rename it, or use the Makefile, which names it correctly:

```sh
# Makefile route (recommended) — produces ./bin/loam
make build

# same thing by hand
go build -o bin/loam .

# go install route — installs $GOBIN/loam, rename it to loam
go install github.com/loamdev/loam@latest
```

Put `bin/` on your `PATH` or invoke `./bin/loam` directly. While developing, `go run . up` works
too.

## Usage

```sh
loam up          # start an isolated stack, wait for Postgres, seed it, print the summary
loam down        # tear the stack down (containers, networks, volumes) and clear state
loam status      # what is running: project, services, tables, rows seeded, URLs
loam reset       # truncate every table and re-seed, same stack
loam seed        # seed only, against a stack that is already up
loam logs [svc]  # stream container logs (passthrough to docker compose logs)
```

Global flags, valid on every command:

| Flag | Default | Meaning |
|---|---|---|
| `--file`, `-f` | `docker-compose.yml` | Compose file to run |
| `--dir` | `.` | Project directory — where `.loam/` and `loam.yml` live |
| `--db-url` | auto-detected | Override the Postgres connection string |
| `--rows` | `10000` | Default rows per table, before per-table heuristics |

`seed` additionally takes `--truncate`, to empty the tables before loading new rows.

### What `loam up` prints on success

```
  ✔ Environment pb_1788263615_dbd4a91c started (2 services)
  ✔ Database ready
  ✔ Schema read: 9 tables, 8 foreign keys
  ✔ Seeded 46,800 rows across 9 tables in 1.2s
  → App:      http://localhost:8080
  → DB:       postgres://appuser:****@localhost:5433/appdb   (password masked)
  Ready. Run 'loam down' when finished.
```

Passwords are masked in every printed URL. On failure you get the step that broke plus something
actionable, e.g. `Database not ready after 30s — is the postgres service healthy? Run 'loam logs db'`.

## Requirements

| | |
|---|---|
| Go | 1.25 or newer — to build; not needed to run the binary |
| Docker | with Compose v2. Loam shells out to `docker compose`, not `docker-compose` |
| Your repo | a `docker-compose.yml` with a Postgres service that publishes a host port |

Developed on Windows, CI runs ubuntu-latest; the tool is OS-portable.

## Zero config

The happy path takes no configuration at all. Loam reads your compose file, finds the service
whose image looks like Postgres, pulls `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` out of
its environment, reads the published host port out of its `ports` mapping, and connects. Row counts
and value distributions have sane defaults.

`loam.yml` in the project directory is the escape hatch, for what auto-detection cannot work out
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

`testapp/` is the deliberately boring Compose + Postgres repo the acceptance tests run against: a `db`
service on host port `5433`, a placeholder `web` service on `8080` so App URL detection has a non-db
port to find, and a nine-table schema (`categories`, `users`, `employees`, `products`, `promotions`,
`orders`, `order_items`, `tenant_users`, `tenant_events`).

It is built to force code paths open rather than to look realistic: a real enum type *and* a
CHECK-constraint status column, a self-referencing FK, a nullable FK, a `uuid` PK, a `smallint` FK,
`jsonb`, `numeric` money, a `date` and a naive `timestamp`, a three-level FK chain, and a composite
PK/FK pair. See [`testapp/README.md`](testapp/README.md).

```sh
make build
cd testapp
../bin/loam up
psql "postgres://appuser:apppass@localhost:5433/appdb" -c 'table orders limit 5;'
../bin/loam status
../bin/loam down
```

On a warm machine that whole cycle takes about 15 seconds and seeds roughly 47,000 rows.

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

## Documentation

| I want to… | Read |
|---|---|
| Understand how the code works, before changing it | [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) |
| Set up, run the tests, open a PR | [`docs/CONTRIBUTING.md`](docs/CONTRIBUTING.md) |
| Know what the tool is required to do | [`docs/BUILD_SPEC.md`](docs/BUILD_SPEC.md) |
| Know why something was built this way | [`docs/BUILD_SESSION_LOG.md`](docs/BUILD_SESSION_LOG.md) |
| Know what was deliberately left out | [`LATER.md`](LATER.md) |

[`docs/README.md`](docs/README.md) indexes all of it and suggests a reading order.

## Contributing

Full guide in [`docs/CONTRIBUTING.md`](docs/CONTRIBUTING.md). The short version:

- Read [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) before changing `internal/`. Several parts
  look wrong until you know why they are that way — two of them deliberately depart from the spec
  because the spec's suggestion was incorrect.
- Three test tiers. Only the first needs no Docker:

```sh
go test ./...                                        # unit
LOAM_TEST_DB=… go test ./... -run Fixture       # integration, needs a live Postgres
go test -tags=e2e -timeout 15m .                     # end-to-end, needs Docker
make ci                                              # fmt + vet + test + build
```

- CI runs eight jobs and all must be green: **fmt, vet, lint, test, tidy, build, integration, e2e**.
  Pin golangci-lint to **v2.12.2** locally to match it.
- The rules that never bend: never log or persist a real password, mask them in printed output; use
  `COPY`, never row-by-row `INSERT`; every foreign key value must come from a parent row that exists;
  the zero-config path must keep working. Anything you decide not to do goes in `LATER.md` with the
  reason.

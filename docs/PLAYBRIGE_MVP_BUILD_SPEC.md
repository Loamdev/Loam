# Playbrige MVP — Build Specification

> **For the implementing agent (Claude Code or an engineer):** This document fully defines the Playbrige MVP. Build exactly what is in the **IN SCOPE** section. Do not build anything in **OUT OF SCOPE**, even if it seems natural — those are deliberately deferred. When a choice is under-specified, prefer the simplest thing that passes the acceptance test in that phase. Build phase by phase; each phase ends with a runnable, testable result. Do not skip the acceptance tests.

---

## 0. What we are building (one paragraph)

Playbrige is a Go command-line tool. A developer runs `playbrige up` inside a repository that contains a `docker-compose.yml` with a Postgres service. Playbrige starts an isolated copy of that Compose stack, waits for Postgres to be ready, reads the database schema (tables + foreign keys), fills every table with realistic, foreign-key-correct synthetic data using fast bulk `COPY`, prints a connection string and the app URL, and tears everything down cleanly on `playbrige down`. The magic moment is: **one command → the app running with believable, relationally-consistent data → gone when done.** Target: this whole cycle in under ~30 seconds, with seeding of ~10,000 rows in under ~2 seconds.

This MVP runs **entirely locally** on the developer's own machine and Docker. There is **no server, no cloud, no web dashboard, no accounts, no network calls to any Playbrige backend.** It holds no real data (all data is synthetic) and no secrets beyond what the user already has in their own compose file.

---

## 1. IN SCOPE (build all of this)

- A Go CLI named `playbrige` with commands: `up`, `down`, `status`, `reset`, `seed`, `logs`.
- Parse the repo's `docker-compose.yml` and run it under a unique, isolated project name.
- Detect the Postgres service and connect to it.
- Introspect the Postgres schema: tables, columns (name + type + nullability), primary keys, foreign keys.
- Build a dependency graph of tables and topologically sort it (parents before children).
- Generate realistic synthetic data, foreign-key-aware, inserted via `COPY`.
- Name-aware + type-aware fake value generation (email→email, created_at→timestamp, etc.), with an Indian-locale flavor for names/phones.
- Basic distribution profiles for status-like columns (e.g. active/trial/churned mix) rather than uniform values.
- Auto-teardown on `down` (remove containers, networks, volumes).
- `reset` = truncate all tables and re-seed (fast path via template DB is a stretch goal, see §9).
- Persist environment state to a local `.playbrige/state.json` so `down`/`status`/`logs` know what to act on.
- A `logs` command that streams container logs (thin wrapper over Docker).
- Config via an optional `playbrige.yml` (fallback/escape hatch only — NOT required for the happy path).
- Clear, friendly CLI output with the summary block shown in §7.

## 2. OUT OF SCOPE (do NOT build — deferred by design)

- Kubernetes, multi-service data graphs across multiple databases, non-Postgres databases.
- Any hosted control plane, Runner agent, tunnel, public URLs, or cloud deployment.
- Web dashboard, accounts, authentication, teams, billing.
- Session Vault / `storageState` / persona login injection.
- GitHub App, PR environments, CI integration, MCP server, agent APIs.
- Production-data sampling/subsetting or anonymization.
- Copy-on-write snapshots / point-in-time branching.
- Migration-safety analysis, failure intelligence, flaky-test triage.
- Webhook routing / third-party integration handling.
- Any always-on background process or daemon.

If any of these feels needed, write it on a `LATER.md` list and move on. The MVP is the single-Postgres local happy path.

---

## 3. Tech stack (use exactly these)

- **Language:** Go 1.22+.
- **CLI framework:** `github.com/spf13/cobra`.
- **Config parsing:** `gopkg.in/yaml.v3` (for compose file + optional `playbrige.yml`).
- **Postgres driver:** `github.com/jackc/pgx/v5` (use `pgxpool` for the pool and `pgx.CopyFrom` for bulk insert).
- **Fake data:** `github.com/brianvoe/gofakeit/v7`.
- **Docker control:** shell out to the `docker` CLI via `os/exec` for `compose up/down/logs` (simplest, reliable). Do **not** pull in the Docker SDK for the MVP — the CLI is enough. (Note for later: the SDK gives finer control; not needed now.)
- **Module path:** `github.com/playbrige/playbrige` (or the user's chosen path — ask once, then use consistently).

No other dependencies without a clear reason.

---

## 4. Repository / file layout (create this structure)

```
playbrige/
  go.mod
  go.sum
  main.go                      # cobra root + command wiring only
  internal/
    env/
      compose.go               # parse docker-compose.yml, up/down/logs via docker CLI
      state.go                 # read/write .playbrige/state.json
    db/
      connect.go               # pgxpool connect + readiness retry loop
      introspect.go            # read tables, columns, PKs, FKs from information_schema
      model.go                 # Table, Column, FK, Schema structs
      toposort.go              # dependency graph + topological sort (Kahn's)
    seed/
      generate.go              # per-column fake value generation (name/type aware)
      distributions.go         # status-like column distribution profiles
      seeder.go                # orchestrates: order -> generate rows -> COPY -> record IDs
    config/
      config.go                # optional playbrige.yml loading + defaults
    cli/
      up.go down.go status.go reset.go seed.go logs.go   # command implementations
      output.go                # the friendly summary/printing helpers
  LATER.md                     # anything tempting but out of scope
  README.md
```

Keep `main.go` thin: it only builds the cobra command tree and delegates to `internal/cli`.

---

## 5. Data model (implement these structs in `internal/db/model.go`)

```go
type Column struct {
    Name     string
    DataType string // canonical: "int", "bigint", "text", "varchar", "bool",
                    // "timestamp", "timestamptz", "uuid", "numeric", "date", "json/jsonb", etc.
    Nullable bool
    IsPrimaryKey bool
}

type FK struct {
    ChildColumn  string // column on THIS table
    ParentTable  string
    ParentColumn string
}

type Table struct {
    Name    string
    Columns []Column
    FKs     []FK
    PrimaryKey []string // column name(s)
}

type Schema struct {
    Tables map[string]*Table // keyed by table name
    Order  []string          // topologically sorted table names (parents first) — filled by toposort
}
```

---

## 6. Exact behavior of each phase / component

### Phase 0 — Skeleton CLI
- `main.go` builds a cobra root command `playbrige`.
- Register all six subcommands; each initially prints a TODO line.
- **Acceptance:** `go run . up` prints a placeholder; `playbrige --help` lists all commands.

### Phase 1 — Compose up/down with isolation (`internal/env/compose.go`, `state.go`)
- On `up`: locate `docker-compose.yml` in the current directory (or `--file` flag). Generate a unique project name: `pb_<unix_timestamp>` (or `pb_<short-random>`). Run:
  `docker compose -p <project> -f <file> up -d`
- Isolation requirement: two `up` runs must produce two fully independent stacks (separate networks/volumes/containers) that do not collide. Verify with `docker ps`.
- On `down`: read the project name from state, run `docker compose -p <project> -f <file> down -v` (the `-v` removes volumes — clean slate). Then delete `.playbrige/state.json`.
- `state.go`: write/read a small JSON at `.playbrige/state.json`:
  ```json
  { "project": "pb_1720000000", "composeFile": "docker-compose.yml", "createdAt": "...", "dbUrl": "postgres://..." }
  ```
- **Never** log or persist any secret value beyond what is already in the user's compose file. Do not print full passwords in summaries (mask them).
- **Acceptance:** `up` twice → two independent stacks in `docker ps`. `down` → `docker ps -a` and `docker volume ls` show nothing left over.

### Phase 2 — Connect to Postgres (`internal/db/connect.go`)
- Determine the Postgres connection details. Order of resolution:
  1. If `playbrige.yml` specifies `database.url` or components, use that.
  2. Else parse the compose file: find the service using a `postgres` image; read `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB` from its `environment`; read the published host port from its `ports` mapping (e.g. `5433:5432` → connect to `localhost:5433`).
  3. Else accept a `--db-url` flag as override.
- Build a URL: `postgres://user:pass@localhost:<port>/<db>`.
- Connect with `pgxpool.New`. **Readiness retry loop:** containers aren't instantly ready — retry connecting for up to 30s, sleeping 1s between attempts, before failing. This is mandatory (prevents flaky runs).
- **Acceptance:** after `up`, the tool connects and prints the list of public tables (`SELECT tablename FROM pg_tables WHERE schemaname='public'`).

### Phase 3 — Schema introspection (`internal/db/introspect.go`)
- Query columns + types + nullability + PKs from `information_schema.columns` and the PK constraints.
- Query foreign keys with (parent = referenced, child = referencing):
  ```sql
  SELECT
    tc.table_name  AS child_table,
    kcu.column_name AS child_column,
    ccu.table_name  AS parent_table,
    ccu.column_name AS parent_column
  FROM information_schema.table_constraints tc
  JOIN information_schema.key_column_usage kcu
    ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
  JOIN information_schema.constraint_column_usage ccu
    ON ccu.constraint_name = tc.constraint_name AND ccu.table_schema = tc.table_schema
  WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = 'public';
  ```
- Normalize Postgres types to the canonical set in the model (map `character varying`→`varchar`, `integer`→`int`, `timestamp without time zone`→`timestamp`, etc.).
- Populate the `Schema` struct.
- **Acceptance:** for the test app, printing each table shows correct columns, types, PKs, and correctly lists which column references which parent table.

### Phase 4 — Topological ordering (`internal/db/toposort.go`)
- Build a directed graph: edge child → parent (a table depends on the tables it references).
- Topologically sort so that parents are emitted before children (Kahn's algorithm: repeatedly emit tables whose dependencies are already emitted).
- **Cycle handling (MVP):** if a cycle is detected (rare — e.g. self-reference or mutual FK), break it by deferring one FK: emit the table with its cyclic FK column left nullable/empty on first pass. Do NOT over-engineer; a warning log is fine. Self-referencing tables (`manager_id → employees.id`) are inserted in one pass with the self-FK drawn from already-generated PKs in the same batch, or left null.
- Store the result in `Schema.Order`.
- **Acceptance:** the printed order always lists a parent before any table referencing it, on a 3–4 table chained schema.

### Phase 5 — FK-aware seeding (`internal/seed/*`) — THE CORE

This is the heart of the product. Implement carefully.

**Algorithm:**
1. Decide a row count per table. Default: 10,000 for "leaf-ish" tables, scale down for lookup tables; accept a `--rows N` flag and a per-table override in `playbrige.yml`. Keep a sane default map; don't require config.
2. Walk tables in `Schema.Order` (parents first).
3. Maintain an in-memory pool of generated primary-key values per table: `map[tableName][]any`.
4. For each row of a table, for each column:
   - **If the column is a foreign key:** draw its value **from the parent table's PK pool** (which already exists because parents came first). Never invent an FK value. If the FK is nullable, occasionally (e.g. ~10%) set it null for realism.
   - **Else if the column is a primary key:** generate a fresh value appropriate to its type (sequential int, or uuid). Record it into this table's PK pool.
   - **Else generate by name-then-type** (see mapping below).
5. Insert the generated rows for the table using `pgx.CopyFrom` (bulk `COPY`), **not** row-by-row `INSERT`. This is the performance requirement.
6. Continue to the next table.

**Column → value mapping (`generate.go`) — name-aware beats type-aware:**

| Column name matches (case-insensitive, substring) | Generate |
|---|---|
| `email` | `gofakeit.Email()` |
| `first_name`/`firstname` | `gofakeit.FirstName()` |
| `last_name`/`lastname` | `gofakeit.LastName()` |
| `name`/`full_name` | `gofakeit.Name()` (Indian-locale flavored if feasible) |
| `phone`/`mobile` | Indian-format 10-digit number, e.g. `+91` + realistic prefix |
| `city` | `gofakeit.City()` (Indian cities list preferred) |
| `pincode`/`zip`/`postal` | 6-digit Indian pincode |
| `created_at`/`updated_at`/`*_at`/`*_date` | `gofakeit.Date()` within a sane recent range |
| `price`/`amount`/`total`/`cost` | realistic money value, 2 decimals, sensible range |
| `status`/`state`/`type` | draw from a distribution profile (see below) |
| `description`/`bio`/`about` | `gofakeit.Sentence()` |
| `url`/`link` | `gofakeit.URL()` |
| `is_*`/`has_*` (bool) | weighted bool |
| (fallback, by type) | text→`gofakeit.Word()`, int→random int in range, bool→bool, uuid→`gofakeit.UUID()`, numeric→float, json/jsonb→`{}` |

Respect nullability: for nullable non-FK columns, occasionally emit null. Respect the declared type in the end (a value written into an int column must be int-compatible).

**Distributions (`distributions.go`):** for `status`-like columns, don't spread uniformly. Use a weighted profile, default e.g.: `active 70%, trial 15%, churned 10%, suspended 5%` — but if the column has a known enum/check constraint set, draw from those allowed values instead (query the enum/check if easy; otherwise use the generic profile). Distribution realism is part of the "catches edge-case bugs" value — a few status values should be rare, not evenly split.

**`seeder.go`** ties it together: order → for each table generate N rows drawing FKs from pools → `CopyFrom` → record PKs → next.

- **Acceptance:** seed the test app, open it, log in / browse — every FK points to a real parent row, lists show believable data, nothing is broken by dangling references. **Time it: ~10,000 rows in < 2 seconds** (COPY, not INSERT). If it's slower, you're probably using INSERT — fix it.

### Phase 6 — Wire together + reset + polish (`internal/cli/*`, `output.go`)
- `up` runs the full chain: compose up (P1) → wait for DB (P2) → introspect (P3) → order (P4) → seed (P5) → print the summary (§7). Write state after a successful up.
- `seed` runs seeding only against the current environment (assumes `up` already ran).
- `reset` = truncate all tables (in reverse dependency order to satisfy FKs, or `TRUNCATE ... CASCADE`) then re-run seeding. (Stretch: use a Postgres template DB for near-instant reset — see §9.)
- `status` reads state and reports: project name, running/stopped, table count, seeded row count if known, the URLs.
- `logs` = `docker compose -p <project> logs -f [service]` passthrough.
- `down` = teardown (P1) + delete state.
- **Acceptance:** a clean clone of the test app goes from `playbrige up` to a working, seeded, browsable app in **under ~30 seconds** with a single command; `down` leaves nothing behind.

---

## 7. CLI output contract (match this shape)

`playbrige up` on success prints:

```
  ✔ Environment pb_1720001234 started (3 services)
  ✔ Database ready
  ✔ Schema read: 14 tables, 9 foreign keys
  ✔ Seeded 10,000 rows across 14 tables in 0.8s
  → App:  http://localhost:8080
  → DB:   postgres://user:****@localhost:5433/appdb   (password masked)
  Ready. Run 'playbrige down' when finished.
```

Rules: mask passwords in any printed URL. Use ✔ for completed steps, → for resources. On failure, print which step failed and the actionable reason (e.g. "Database not ready after 30s — is the postgres service healthy? Run 'playbrige logs db'").

---

## 8. Optional `playbrige.yml` (escape hatch — NOT required)

The happy path must work with **zero config**. `playbrige.yml` only overrides detection when auto-detection can't figure something out:

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

If the file is absent, everything uses auto-detection and sane defaults. Never require the user to write this to get the happy path working.

---

## 9. Stretch goals (only if the core is done and passing)

- **Template-DB fast reset:** after the first seed, `CREATE DATABASE <name>_template TEMPLATE <name>` isn't directly usable while connected; instead seed once into a template database and instantiate copies with `CREATE DATABASE x TEMPLATE seeded_template` for near-instant fresh databases. Only attempt after the basic truncate+reseed reset works.
- **Pre-pull images** on first run so `up` is faster.
- **`--with` targeted state** (e.g. `--with "users:status=churned:count=5"`) to force specific edge-case rows — this is the seed of the "reproduce this bug" feature; implement only if core is solid.
- Parallelize container startup and seeding (seed while app containers boot).

Do not start any stretch goal until §6 acceptance tests all pass.

---

## 10. A test target to build against (create if the user doesn't provide one)

If no test app is supplied, scaffold a minimal one under `testapp/`:
- `docker-compose.yml` with a `db` service (`postgres:16`, env vars set, port `5433:5432`) and a trivial `web` service (any small image that connects to the DB, or omit web and just prove seeding on the DB).
- A migration/SQL file creating a chained schema to exercise FKs, e.g.:
  - `users(id PK, email, name, status, created_at)`
  - `orders(id PK, user_id FK→users.id, total, status, created_at)`
  - `order_items(id PK, order_id FK→orders.id, product_name, price, qty)`
  - `products(id PK, name, price)` and optionally `order_items.product_id FK→products.id`
- This exercises: multi-level FK chains, a status column with distribution, timestamps, money, and name/email generation.

---

## 11. Definition of done (the single acceptance test for the whole MVP)

> Hand the machine to someone. They `git clone` a Postgres-backed app they've never seen, `cd` in, and type `playbrige up`. Within ~30 seconds they have a running app full of realistic, foreign-key-correct data they can log into and click through. They type `playbrige down` and everything vanishes cleanly — no leftover containers or volumes. Seeding 10k rows took under ~2 seconds.

If that works, the MVP is done. **Stop and do not add features past this line** — the next step is showing it to real developers, not building more.

---

## 12. Non-negotiable constraints (read before coding)

1. **Local only.** No network calls to any Playbrige server. No cloud. No daemon.
2. **Never log or persist secrets.** Mask passwords in output. Don't upload the compose file anywhere.
3. **COPY, not INSERT.** The seeding speed is the product's demo moment; row-by-row insert fails the acceptance test.
4. **Zero-config happy path.** `playbrige up` must work on a standard Compose+Postgres repo with no `playbrige.yml`.
5. **FK correctness is sacred.** Every foreign key must reference a row that exists. Incoherent data = the product has no value.
6. **Stay in scope.** Anything in §2 is forbidden in the MVP. Put temptations in `LATER.md`.
7. **Build phase by phase.** Each of the seven phases must run and pass its acceptance test before the next begins.

---

## 13. Clarifications resolved during the build

The sections above left seven things unpinned. Each is answered here, with what the
implementation actually does, so nobody has to guess again.

### 13.1 Where the schema comes from — **assume it already exists**

Playbrige does **not** run migrations and does not wait for an app to run them. It connects,
introspects whatever is in the `public` schema, and seeds that. The bundled fixture loads its
schema from an init SQL file that Postgres executes on first boot
(`testapp/initdb/001_schema.sql` mounted at `/docker-entrypoint-initdb.d`), which is the
supported pattern for the MVP.

An empty `public` schema is therefore a normal, non-fatal outcome — a container whose migrations
have not run. `up` warns twice and continues:

```
  ! the database has no tables in the public schema, so there is nothing to seed
  ! if this app creates its schema with migrations, run them and then 'playbrige seed'
```

Running the app's own migration command is **out of scope** (`LATER.md`). The workflow for a
migration-based app is: `playbrige up`, run your migrations, then `playbrige seed`.

### 13.2 A parent with no rows — never invent a key

Row counts are floored at 50 (`seed.minRows`), so a parent always has rows for its children to
reference. If a pool is nevertheless empty, the rule is absolute: **never fabricate a foreign
key** (§12.5). A nullable reference is left NULL; a NOT NULL reference fails with a named error
identifying both sides. Fabricating a key would produce data that looks fine and is a lie.

### 13.3 Composite primary and foreign keys — **supported**

Foreign keys are grouped by constraint name (`db.FKGroup`), and a composite key is drawn as one
whole tuple from a pool keyed on the parent's column set.

This matters more than it sounds: drawing each column independently produces pairs that exist in
no parent row, and Postgres rejects the entire COPY. That was a real failure before it was fixed —
any schema with a composite FK aborted `up`.

A partially nullable composite key counts as NOT NULL, because Postgres leaves a partly-NULL
composite reference unenforced, which would silently drop the relationship rather than model it.

Composite primary keys are generated unique but with correlated values (consecutive counters per
row). They satisfy the constraint; making the leading column realistically low-cardinality is
deferred (`LATER.md`).

### 13.4 Unique constraints — best effort, bounded by the column's own width

Columns whose names imply a natural key (`email`, `sku`, `code`) get a per-row counter woven in.
The counter is the last thing given up when a value has to be trimmed to fit: a narrow column loses
its padding, then its domain or prefix, and only then digits off the counter. So these stay distinct
as long as the column is wide enough to hold the counter at all.

Where it genuinely cannot hold: a `varchar(2)` has a few hundred possible values, so a 10,000-row
seed cannot be unique in it no matter how the value is built. That is arithmetic, not a bug.

Unique constraints are **not** introspected, so a `UNIQUE` index on some other random-valued column
can still fail the batch. Deferred (`LATER.md`).

### 13.5 NOT NULL columns are never nulled

`Generator.Value` returns NULL only when `Column.Nullable` is true. A NOT NULL column always gets
a value, and this is asserted by a test. The ~10% null rate applies exclusively to nullable
columns.

### 13.6 Where the App URL comes from

In order: `app.url` in `playbrige.yml`, otherwise the first published host port belonging to a
service that is **not** the Postgres one, rendered as `http://localhost:<port>`. When nothing
qualifies the line is omitted entirely rather than printing a guess that does not resolve.

### 13.7 Which service is "the Postgres one"

A service qualifies when its image name contains `postgres`, `postgis`, `timescale`, or
`pgvector`. When several qualify, the one publishing container port 5432 to the host wins;
remaining ties break alphabetically, so the choice is deterministic rather than dependent on Go's
map iteration order. When none qualifies, `up` fails before starting anything, naming the fix
(`--db-url`, or `database.url` in `playbrige.yml`).

Non-Postgres databases in the same compose file are ignored: they are simply services Playbrige
does not introspect. Seeding a non-Postgres engine is out of scope (§2).

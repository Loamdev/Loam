# Playbrige MVP — Build Session Log

A complete record of the session that built the Playbrige MVP, kept so the reasoning behind the
code is recoverable later. Paste or import this straight into Notion.

- **Repo:** `shivansh-source/stateful-playwright` (module path `github.com/shivansh-source/stateful-playwright`, binary `playbrige`)
- **Spec:** `docs/PLAYBRIGE_MVP_BUILD_SPEC.md` — the source of truth
- **Outcome:** MVP complete, all seven phases merged to `main` (through PR #12)
- **Size:** 37 Go files, ~7,970 lines
- **Final state:** whole cycle 15.2s (target ~30s), 46,800 rows seeded in 1.2s (target ~10k in 2s)

---

## 1. What Playbrige does

One command inside a repo that has a `docker-compose.yml` with Postgres in it:

```
$ playbrige up
  ✔ Environment pb_1788263615_dbd4 started (2 services)
  ✔ Database ready
  ✔ Schema read: 9 tables, 8 foreign keys
  ✔ Seeded 46,800 rows across 9 tables in 1.2s
  → App:      http://localhost:8080
  → DB:       postgres://appuser:****@localhost:5433/appdb   (password masked)
  Ready. Run 'playbrige down' when finished.
```

It starts an isolated copy of the Compose stack, waits for Postgres, reads the schema, fills every
table with foreign-key-correct synthetic data via bulk `COPY`, and prints where to reach it.
`playbrige down` removes containers, networks, and volumes.

Six commands: `up`, `down`, `status`, `reset`, `seed`, `logs`.

---

## 2. Decisions taken at the start

| Decision | Choice | Why |
|---|---|---|
| Module path | `github.com/shivansh-source/stateful-playwright` | Must match the repo URL for Go tooling, even though the product is named Playbrige. Binary is still `playbrige` via the Makefile. |
| Existing code | Discarded | The prior "Week 1 Docker orchestration layer" scaffold was replaced wholesale, at the user's direction. |
| Branching | One branch + one PR per spec phase | The spec demands phase-by-phase delivery with acceptance tests; the user reviews each as its own PR. |
| Pushing | The user pushes and merges, always | Claude's credentials cannot reach the repo (see §8). |
| Dependencies | Exactly cobra, yaml.v3, pgx/v5, gofakeit/v7 | Spec §3 names these and forbids others. No Docker SDK — the `docker` CLI is shelled out to. |

---

## 3. Phase-by-phase log with acceptance results

Every phase was verified against the spec's own acceptance criterion, live, before being called done.

### Phase 0 — Skeleton CLI + CI
Thin `main.go`, cobra root with all six subcommands, `output.go` implemented for real (it is stable
across all phases), the `testapp/` fixture, and CI.

**Acceptance:** `--help` lists all six commands; `go run . up` prints a placeholder and exits 0. ✅

### Phase 1 — Compose up/down with isolation
`internal/env`: compose parsing (both environment forms, both port forms, `${VAR}` interpolation from
the process env plus a sibling `.env`), project names `pb_<unix>_<random>`, Docker CLI wrappers,
atomic `.playbrige/state.json`.

**Acceptance:** two `up` runs produced two fully independent stacks — distinct containers, networks,
and volumes, confirmed via `docker ps` / `network ls` / `volume ls`. `down` left **0 containers,
0 networks, 0 volumes** and removed `.playbrige/`. A second `down` exited 0. ✅

### Phase 2 — Connect to Postgres
`internal/config` (optional `playbrige.yml`), `internal/db/connect.go` with the mandatory 30s × 1s
readiness retry loop, and URL resolution: `--db-url` → `playbrige.yml` → compose auto-detect.

**Acceptance:** `up` on the fixture with **zero config** connected in 6.8s and listed the tables,
auto-deriving `postgres://appuser:apppass@localhost:5433/appdb` and `http://localhost:8080`. ✅

### Phase 3 — Schema introspection
Columns, types, nullability, PKs, FKs, type normalisation, plus allowed values from both enum types
and `CHECK (col IN (...))` constraints.

**Acceptance:** all tables found; 21 column type/nullability/max-length assertions passed, covering
enum, jsonb, numeric, uuid, date, naive vs tz timestamp, smallint, bounded varchar; every PK correct;
every FK correct including the `employees.manager_id` self-reference; allowed values resolved from
both the enum and the CHECK. ✅

### Phase 4 — Topological ordering
Kahn's algorithm over the child→parent graph. Self-references are not dependencies. A real cycle is
broken by deferring one FK — the nullable side, ties broken alphabetically — with a warning.

**Acceptance:** the fixture orders as `categories → employees → products → promotions → users →
orders → order_items`; every parent before every child. ✅

### Phase 5 — FK-aware seeding (the core)
`internal/seed`: name-then-type generation, weighted distributions, FK pools, `pgx.CopyFrom`.

**Acceptance:** **34,800 rows in 1.1s** — 28.6µs/row against a 200µs/row budget. All 6 FKs checked
with `LEFT JOIN`s: **zero dangling references**. Distributions properly skewed:
`users.status` active 70.8% / trial 17.3% / churned 6.5% / suspended 5.5% (from the enum);
`orders.status` paid 46.7% / shipped 28.9% / pending 16.1% / cancelled 5.6% / **refunded 2.8%**
(from the CHECK). Null rates ~9.7%, weighted bool 74.5% true. ✅

### Phase 6 — Wire it together
`environment.go` (one helper for `seed`/`reset`/`status`, deliberately never touching Docker),
the real `seed`/`reset`/`status`, `db.Truncate`, `db.RowCounts`, the §7 summary block, and the
build-tagged `e2e_test.go`.

**Acceptance (spec §11, the definition of done):** whole cycle **12.5s**, later **15.2s** with the
larger fixture. `status` reported the stack running with correct table and row counts; `reset`
emptied and refilled in ~1.2s without restarting a container; `logs db` passed through; `down`
removed everything; a second `down` was harmless; 0 containers / 0 networks / 0 volumes remained. ✅

---

## 4. Bugs found and fixed — the most valuable part of this log

These were all found by testing rather than by reading, and several would have shipped silently.

### 4.1 `MaskURL` leaked passwords for opaque URLs
`url.Parse("user:pass@host/db")` yields an **opaque** URL whose `User` is `nil`, so a Parse-based
mask handed the password straight back. Rewritten as string surgery over a credential regex, with a
test asserting the secret cannot survive whatever the input shape. **Spec §12.2.**

### 4.2 `numeric` precision overflow
`promotions.discount_pct` is `numeric(5,2)` — it tops out at 999.99. Money-shaped values overflowed
and failed the whole COPY. Precision and scale are now introspected and generated values clamped.

### 4.3 Sequences left at 1 — the seeded app couldn't be used
Supplying explicit primary keys leaves every serial sequence at 1, so the application's **first**
`INSERT` collided with seeded row id 1. Sequences are now advanced past the seeded data after
seeding. Verified: a probe INSERT gets id 401 with 400 rows present. This is the difference between
data you can read and an app you can use.

### 4.4 The spec's own FK query is wrong for composite keys
Spec §6 phase 3 sketches an `information_schema` join of `key_column_usage` to
`constraint_column_usage`. That **cross-products** a multi-column foreign key, inventing column
pairings that do not exist. Replaced with `pg_catalog` + `unnest(conkey, confkey)`, which pairs them
positionally. Also namespace-qualified the `pg_type` join via `udt_schema`, since matching on
`typname` alone duplicates rows when a same-named type exists in another schema.

### 4.5 Composite foreign keys aborted `up` entirely
The model could not *represent* a multi-column key, so a two-column constraint looked like two
independent references and each column was drawn from its parent pool separately. The resulting pairs
existed in no parent row:

```
✗ Seeding failed: bulk load tenant_events (10000 rows): ERROR: insert or update on table
  "tenant_events" violates foreign key constraint "tenant_events_tenant_id_user_ref_fkey"
  (SQLSTATE 23503)
```

Any schema with a composite FK was unusable. Fixed properly rather than skipped with a warning,
because a warning still leaves the schema unseedable and §12.5 makes FK correctness sacred:
`FK.ConstraintName` from `pg_constraint.conname`, `Table.FKGroups()` to group by it, and pools that
hold **tuples** keyed on the parent column set. A partially nullable composite key counts as NOT
NULL, because Postgres leaves a partly-NULL composite reference unenforced — nulling one column would
silently drop the relationship. Verified: 0 dangling pairs, 2000 rows / 2000 distinct composite keys.

### 4.6 A failed `up` stranded its containers
State was written only after seeding succeeded, so any earlier failure left a live stack that `down`
could not find — it reported *"No environment is running"* over running containers with port 5433
still bound. `docker compose up` also creates the network and volumes before starting a service, so
even its own failure leaves resources. State is now written immediately after the project name is
chosen, before anything can fail, and updated with the row count at the end.

### 4.7 CI never ran on stacked PRs
`ci.yml` had `pull_request: branches: [main]`. Because the phase branches are stacked, most PRs had
another phase branch as their base, matched no trigger, and **no workflow ran at all** — which reads
as "checks are broken" when CI was never asked. Fixed by dropping the branch filter. Action versions
were also bumped to current majors (checkout v7, setup-go v7, upload-artifact v7,
golangci-lint-action v9), superseding four Dependabot PRs.

### 4.8 A false alarm worth recording
I initially reported that every Go file was committed with CRLF and would fail `gofmt` on Linux.
That was **wrong** — `git ls-files --eol` showed `i/lf w/lf` and blob sizes matched exactly. The
earlier check was a false positive caused by Git Bash translating a pipe. Nothing was changed. Use
`git ls-files --eol`, not `grep $'\r'` through a pipe, on Windows.

---

## 5. Spec gaps closed (now recorded as spec §13)

The spec left seven things unpinned. All are now answered in `docs/PLAYBRIGE_MVP_BUILD_SPEC.md` §13.

| § | Question | Resolution |
|---|---|---|
| 13.1 | Where does the schema come from? | Playbrige does **not** run migrations. It seeds whatever is in `public`. An empty schema is normal and non-fatal, and `up` explains the two-step workflow: `up`, run your migrations, then `seed`. |
| 13.2 | A parent with no rows? | Row counts floored at 50. If a pool is empty anyway: NULL if nullable, **named error** if NOT NULL. Never fabricates a key. |
| 13.3 | Composite PKs and FKs? | **Supported** — drawn as whole tuples. See §4.5. |
| 13.4 | Unique constraints? | `email`/`sku`/`code` get a per-row counter so they cannot collide. Other UNIQUE columns are a documented deferral. |
| 13.5 | Can a NOT NULL column be nulled? | No. `Value()` nulls only when `Column.Nullable`; asserted by test. |
| 13.6 | Where does the App URL come from? | `playbrige.yml app.url`, else the first published non-Postgres port. Prints nothing if neither. |
| 13.7 | Which service is Postgres? | Image contains `postgres`/`postgis`/`timescale`/`pgvector`; the one publishing 5432 wins; ties alphabetical for determinism. |

---

## 6. Verification and CI

**Eight CI jobs**, all green: `fmt`, `vet`, `lint` (golangci-lint v2.12.2, 0 issues), `test`
(race + coverage), `tidy`, `build` (cross-compiles linux/windows/darwin), `integration`, `e2e`.

The last two are the ones that matter:

- **`integration`** — a live `postgres:16-alpine` service with the fixture schema applied, then tests
  that check every FK with real `LEFT JOIN`s and enforce a **µs/row budget**. That budget fails if
  anyone ever replaces `COPY` with `INSERT`.
- **`e2e`** — real Docker, the whole definition of done through the built binary, then an
  **independent** check that no container, network, or volume survived.

Both live suites are gated so `go test ./...` never needs Docker: integration on
`PLAYBRIGE_TEST_DB`, e2e behind the `e2e` build tag.

### Running things locally

```sh
go test ./...                                     # unit tests, no Docker needed
go test -tags=e2e -v -timeout 15m .               # full definition of done, needs Docker

playbrige up --dir testapp                        # bring the fixture up
PLAYBRIGE_TEST_DB=postgres://appuser:apppass@localhost:5433/appdb go test ./... -run Fixture -v
playbrige down --dir testapp

make ci                                           # fmt + vet + test + build
```

### Constraint checks done by hand (spec §12)

- **§12.1 local only** — no HTTP client, no dialer, no telemetry, no goroutines in shipped code
- **§12.2 no secrets** — `printEndpoints` is the single choke point for rendering a connection
  string and it always masks; `Connect` deliberately does not echo the URL into errors
- **§12.3 COPY not INSERT** — zero `INSERT` statements anywhere in `internal/`; one `CopyFrom`
- **§12.5 FK correctness** — `drawFK` has six return paths: four all-NULL tuples, one error, and
  exactly one returning a value, drawn from the parent pool. No path constructs an FK value.

---

## 7. Known limitations (full list in `LATER.md`)

Deliberately deferred, with reasons:

- **Two stacks of the same repo collide on fixed published host ports.** Containers, networks, and
  volumes isolate correctly; host ports cannot, because spec §6 phase 2 requires reading the port out
  of the `ports:` mapping.
- **Unsupported column types** — `bytea`, `inet`/`cidr`, `interval`, arrays are left to their
  database default, with a warning; a NOT NULL one with no default is reported rather than guessed.
- **`GENERATED ALWAYS AS IDENTITY`** primary keys are skipped with a warning (Postgres rejects a
  supplied value). `serial`/`bigserial` and `GENERATED BY DEFAULT` work normally.
- **Unique constraints are not introspected** beyond the name-based heuristic.
- **Running the app's own migrations** is out of scope — hence the two-step workflow.
- **Composite PKs get correlated values** (e.g. `tenant_id=1237, user_ref=1238`): valid and unique,
  but unrealistic. Realism polish, not correctness.

Plus everything from spec §2 (Kubernetes, cloud, dashboards, accounts, session vault, GitHub App,
production-data sampling, snapshots, migration-safety analysis, webhooks, daemons) and the §9 stretch
goals (template-DB fast reset, image pre-pulling, `--with` targeted state, parallelised seeding) —
none of which were started, by design.

---

## 8. Environment constraints hit during the build

Worth knowing before the next session.

- **No repo access from the CLI.** `gh` is authenticated as `shivanshCapmob`, which cannot see
  `shivansh-source/stateful-playwright`. `git fetch`, `git push`, `gh pr view`, and `gh pr checks`
  all fail with "Repository not found"; the raw URL 404s. Consequences: **CI logs cannot be read**
  (reproduce each job locally instead), and `origin/*` / `main` refs go stale because only GitHub
  Desktop can fetch. Never conclude a branch is unmerged from a local ref alone.
- **GitHub Desktop auto-stashes on branch switch.** It switched branches mid-task three times,
  stashing uncommitted work each time. Everything was recovered, but it cost time — commit early
  rather than holding a large uncommitted tree.
- **Applying a leftover stash wrote conflict markers into the working tree**, which broke the build
  with `expected statement, found '<<'` while the *commit* was clean. If that happens, check
  `git status` for `AA` (unmerged) files and `git stash list` before assuming the commit is bad.
- **Docker Desktop stopped twice mid-session**, so live acceptance tests had to be re-run.

---

## 9. Git history

```
2316b1e  Phase 0: reset to Playbrige MVP scaffold, add CI checks
b994791  Phase 1: isolated compose up/down and .playbrige/state.json
7acc84e  ci: run checks on every PR, not just PRs targeting main
102b694  Merge CI trigger fix into the phase 1 branch
d9cf0cd  added some changes          ← Phase 2, committed by the user
d1d883e  Update README.md
b0dff77  some things
4e89bce  Phase 3: schema introspection with enum and CHECK allowed values
082c6ec  Phases 4 and 5: table ordering and FK-aware seeding via COPY
9550619  Phase 6: wire it together - reset, status, seed, and the summary block
14e63e3  Support composite foreign keys, and stop stranding failed environments
```

Merged to `main` through **PR #12**. PRs #3, #4 were Dependabot action bumps; #7, #9, #10, #11, #12
were the phase branches.

**Branches safe to delete** (all merged): `chore/scaffold-and-ci`,
`feat/phase1-compose-isolation`, `feat/phase3-schema-introspection`,
`feat/phase4-5-toposort-and-seeding`, `feat/phase6-wire-and-polish`,
`fix/composite-foreign-keys`. Also `feat/phase2-db-connect`, which is **stale** — it points at a
commit `main` never had, so a PR from it would look like a regression.

---

## 10. Architecture

```
main.go                     # thin: only calls cli.Execute()
internal/
  cli/                      # cobra tree, the six commands, output contract
    root.go output.go        environment.go resolve.go
    up.go down.go status.go reset.go seed.go logs.go
  env/                      # compose parsing, Docker CLI, state file
    compose.go detect.go state.go
  db/                       # connect, introspect, order
    connect.go introspect.go model.go toposort.go
  seed/                     # the core
    generate.go distributions.go seeder.go
  config/config.go          # optional playbrige.yml
testapp/                    # the fixture CI runs against
e2e_test.go                 # spec §11 definition of done (build-tagged)
```

**The fixture is built to force branches open**, not to look realistic: a real enum type *and* a
CHECK-constraint status column (different code paths), a self-referencing FK, a nullable FK, a uuid
PK with a default, a smallint FK, `jsonb`, `numeric` money, a `date` and a naive `timestamp`, a
3-level FK chain, and a composite PK/FK pair. It deliberately has **no** UNIQUE constraints, since
random generation does not de-duplicate and that would fail the acceptance test for the wrong reason.

---

## 11. Where it stands

Per spec §11 — *"Stop and do not add features past this line"* — the build stopped at the definition
of done. The next step is showing it to real developers, not building more.

Everything deferred is in `LATER.md` with a reason. The most interesting item there is `--with`
targeted state (`playbrige up --with "users:status=churned:count=5"`), which is the seed of the
"reproduce this bug" feature.

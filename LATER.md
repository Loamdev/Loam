# LATER

The parking lot. **The rule: if it is on this list, it does not go in the MVP.**

Everything here was considered and deliberately deferred. Some of it is genuinely good; that is
exactly why it needs to be written down and left alone — the MVP is the single-Postgres local happy
path described in [`docs/PLAYBRIGE_MVP_BUILD_SPEC.md`](docs/PLAYBRIGE_MVP_BUILD_SPEC.md) §11, and the
way that ships is by not building anything else first. When one of these feels necessary mid-phase,
the move is to add a line here and carry on, not to open a branch.

Two kinds of entry: **out of scope** (spec §2 — forbidden in the MVP, revisit only after the MVP is
in real developers' hands) and **stretch goals** (spec §9 — allowed only once every §6 acceptance
test passes, and even then only if the core stays solid).

---

## Out of scope (spec §2)

### Platform reach

| Item | Why deferred |
|---|---|
| Kubernetes environments | A whole second orchestration backend; Compose already covers the local dev machine we are targeting. |
| Non-Postgres databases (MySQL, SQLite, SQL Server, Mongo) | Introspection, type mapping, and bulk load are per-engine rewrites of the core; Postgres `COPY` is what makes the demo fast. |
| Multi-service data graphs spanning several databases | Cross-database FK correctness needs a distributed generation plan; one schema must be right first. |

### Cloud and hosting

| Item | Why deferred |
|---|---|
| Hosted control plane | The MVP is a local binary with no backend; a control plane brings ops, uptime, and a security surface before there is proof anyone wants this. |
| Runner agent | Only exists to serve a control plane that does not exist. |
| Tunnels and public URLs | Sharing an environment is a different product from creating one; also the first thing that turns synthetic data into an exposure risk. |
| Cloud deployment of environments | Same reason — local Docker is the target for the "one command" moment. |

### Product surface

| Item | Why deferred |
|---|---|
| Web dashboard | The CLI output block (spec §7) is the whole UI for now. |
| Accounts and authentication | Nothing to authenticate against; the tool holds no user state. |
| Teams and sharing | Follows from accounts; not before them. |
| Billing | Nothing to bill for until developers use it. |

### Testing and workflow integrations

| Item | Why deferred |
|---|---|
| Session Vault / `storageState` / persona login injection | Pre-authenticated browser sessions are a strong follow-on feature, but they need a stable seeded-user model first. |
| GitHub App | Requires a hosted component and repo permissions; out of the local-only boundary. |
| PR environments | Same — depends on the GitHub App and on hosting. |
| CI integration | The tool must be reliable on a laptop before it is anyone's build gate. |
| MCP server | An agent-facing interface over a CLI whose commands are still being defined. |
| Agent APIs | Same, and would freeze the internals too early. |
| Migration-safety analysis | A separate product (schema diffing and risk scoring), not environment creation. |
| Failure intelligence | Needs a corpus of runs to learn from; there are no runs yet. |
| Flaky-test triage | Belongs to a test-running product; Playbrige does not run tests. |
| Webhook routing / third-party integration handling | Inbound traffic implies a public endpoint, which the local-only constraint rules out. |

### Data handling

| Item | Why deferred |
|---|---|
| Production-data sampling and subsetting | The MVP deliberately touches no real data — that is what makes it safe to run anywhere. |
| Anonymization of real data | Same boundary. Getting anonymization wrong is a breach, not a bug; synthetic generation avoids the class entirely. |
| Copy-on-write snapshots | Filesystem or storage-layer work that only pays off once reset speed is the bottleneck; measure first. |
| Point-in-time branching of an environment | Depends on snapshots, and on a mental model for named environments that the MVP does not have. |

### Runtime shape

| Item | Why deferred |
|---|---|
| Any always-on background process or daemon | Non-negotiable constraint (spec §12.1). A daemon means lifecycle bugs, stale state, and a thing to explain; commands run and exit. |

---

## Stretch goals (spec §9) — only after every §6 acceptance test passes

### Template-DB fast reset

Make `reset` near-instant instead of truncate + re-seed. Sketch:

1. On the first seed, seed into a dedicated database, e.g. `appdb_seed_template`, rather than into
   `appdb` directly.
2. Mark it as a template: `UPDATE pg_database SET datistemplate = true WHERE datname =
   'appdb_seed_template';` (or create it with the right owner and leave it untouched afterwards).
3. `reset` then drops and recreates the working database from it:
   `DROP DATABASE appdb; CREATE DATABASE appdb TEMPLATE appdb_seed_template;`
4. Both statements require **no other session connected** to the databases involved, so the pool must
   be fully closed first and connections opened against `postgres`/`template1`; expect to force-close
   stragglers with `pg_terminate_backend` filtered on `datname`.

Deferred because it only helps after basic truncate + re-seed works and has been timed — and because
the connection-eviction handling is where it will actually be hard.

### Pre-pull images on first run

Kick off `docker compose pull` (or per-image `docker pull`) ahead of, or in parallel with, the first
`up`, so a cold machine does not spend most of its 30-second budget downloading Postgres. Deferred
because it optimizes the cold path while the warm path is still unproven, and it needs care not to
interleave its output with the summary block.

### `--with` targeted state

Force specific edge-case rows on top of the generated data, e.g.
`playbrige up --with "users:status=churned:count=5"`: parse the selector, generate those rows to the
requested spec, and insert them alongside the normal batch while keeping FK correctness. This is the
seed of the "reproduce this bug" feature and the most interesting thing on this list — deferred
because a selector grammar is easy to get wrong and impossible to change later, and because it must
not compromise the plain path.

### Parallelize startup and seeding

Overlap the work: start seeding as soon as Postgres answers, while the app containers are still
booting, and generate rows for independent branches of the dependency graph concurrently. Deferred
because parents must still be committed before children, so it needs the topological order to drive a
worker pool rather than a simple loop — and because the sequential version must first be fast enough
to prove where the time actually goes.

---

## Known limitations found while building

### Two stacks from the same repo collide on published host ports

Phase 1 gives every `up` its own compose project, so containers, networks, and volumes are genuinely
isolated. Host ports are not: a compose file that publishes `5433:5432` binds that port on the
machine, so a second `up` of the *same* repo fails with an address-already-in-use error from Docker
before Playbrige gets a say. Isolation across *different* repos, or across copies with different
published ports, works today.

The fix is to stop publishing the compose file's fixed ports and let Docker assign ephemeral ones,
then read the real port back with `docker compose -p <project> port db 5432`. Deferred because spec
§6 phase 2 explicitly specifies reading the published host port out of the `ports:` mapping
(`5433:5432` → `localhost:5433`), and rewriting the user's port bindings is a larger behavior change
than the MVP should make. Revisit if anyone actually wants two copies of one repo up at once.

### Types Playbrige does not generate yet

`bytea`, `inet`/`cidr`, `interval`, and array columns are left to their database
default. A nullable or defaulted column of one of these types is skipped with a warning; a NOT NULL
one with no default is reported, because writing a guess into it would be worse than saying so.
Deferred because each needs its own realistic-value story and none appears in the common
Compose + Postgres app.

### GENERATED ALWAYS AS IDENTITY primary keys

Postgres rejects an explicitly supplied value for a `GENERATED ALWAYS AS IDENTITY` column, so the
seeder cannot assign those keys - and therefore cannot record them for children to reference. Such a
column is skipped with a warning. `serial`/`bigserial` and `GENERATED BY DEFAULT AS IDENTITY`, which
between them cover almost every real schema, work normally. The fix is to omit the column from the
COPY and read the generated keys back with a follow-up SELECT to fill the pool; deferred because it
costs an extra round trip per table for a case that is rare in practice.

### Unique constraints are not modelled

Generated values are not de-duplicated against a unique index. Columns that look like natural keys -
anything matching `email`, `sku`, or `code` - get a per-row counter woven in, and the counter is the
last thing surrendered when the value has to be trimmed to fit the column, so those stay distinct
wherever the column is wide enough to hold it. Two gaps remain. A `UNIQUE` constraint on some other
random-valued column can still collide and fail the batch. And a column too narrow to hold the
counter cannot be unique across a large seed at all - a `varchar(2)` has a few hundred possible
values - which is arithmetic rather than something a generator can fix.

The fix for the first is to read unique constraints during introspection and guarantee uniqueness for
exactly those columns; deferred until a real schema actually hits it. The second can only be reported,
not solved: the honest response is to warn that the column is too narrow for the requested row count.

### Running the app's own migrations

Playbrige assumes the schema already exists (spec §13.1). An app that creates its schema with
`migrate`/`alembic`/`rails db:migrate` needs those run between `playbrige up` and `playbrige seed`.
Detecting and running a project's migration command means recognizing a dozen tool conventions and
deciding when the schema is "settled"; deferred until the two-step workflow proves annoying enough
to be worth guessing at.

### Composite primary keys get correlated values

A composite PK is generated unique, but its columns come from one counter, so a row looks like
`(tenant_id=1237, user_ref=1238)`. Valid and unique, but unrealistic: real data has many rows per
tenant. Making the leading column low-cardinality and the trailing one unique within it needs the
generator to understand a key's shape rather than its columns; deferred as realism polish, not
correctness.

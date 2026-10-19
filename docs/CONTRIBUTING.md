# Working on Loam

Practical guide to developing this repo. Read [`ARCHITECTURE.md`](ARCHITECTURE.md) first if you are
about to change anything in `internal/` — it explains why the code is shaped the way it is, and
several parts look wrong until you know the reason.

---

## Setup

You need:

- **Go 1.25+**
- **Docker with Compose v2** — only for the live test tiers; unit tests need nothing
- **golangci-lint v2.12.2** — the version CI pins. Match it, or you will chase findings that do not
  reproduce:
  ```sh
  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
  ```

```sh
git clone https://github.com/loamdev/loam
cd loam
make build          # produces bin/loam
```

The module path is `github.com/loamdev/loam` but the binary is `loam`, so
a bare `go install` gives you a binary named after the module. Use `make build`, or
`go build -o bin/loam .`.

---

## The three test tiers

Keep them separate. The fast tier must stay runnable with no Docker.

### Unit — no Docker

```sh
go test ./...
```

### Integration — a live Postgres holding the fixture schema

```sh
./bin/loam up --dir testapp
LOAM_TEST_DB=postgres://appuser:apppass@localhost:5433/appdb go test ./... -run Fixture -v
./bin/loam down --dir testapp
```

These skip silently when `LOAM_TEST_DB` is unset. They assert the things that matter most:
every foreign key checked with a real `LEFT JOIN`, the µs/row performance budget, that the app can
`INSERT` after seeding, and that the same seed reproduces byte-identical data.

### End-to-end — real Docker, the whole definition of done

```sh
go test -tags=e2e -v -timeout 15m .
```

Build-tagged, so it never runs by accident. It drives the real binary against a copy of `testapp/`
(never the repo copy, so your working tree is not dirtied), asserts the spec §7 output line by line,
and independently verifies that `down` left no container, network, or volume.

### Everything CI checks, locally

```sh
make ci             # fmt + vet + test + build
golangci-lint run ./...
go mod tidy && git diff --exit-code -- go.mod go.sum
```

---

## CI

Eight jobs, on every pull request regardless of base branch.

| Job | What fails it |
|---|---|
| `fmt` | `gofmt -l .` prints anything |
| `vet` | `go vet ./...` |
| `lint` | `golangci-lint` (pinned v2.12.2; also schema-checks `.golangci.yml`) |
| `test` | `go test -race` + coverage |
| `tidy` | `go.mod`/`go.sum` are not tidy |
| `build` | fails to cross-compile for linux, windows, or darwin |
| `integration` | live Postgres: a dangling FK, or the µs/row budget blown |
| `e2e` | the full cycle fails, **or** teardown left anything behind |

The `pull_request` trigger has **no branch filter** on purpose. Branches here are often stacked, so a
PR's base is frequently another feature branch rather than `main`; with a filter those PRs match no
trigger and silently get no checks at all. Do not add one back.

Every job prints the exact local command to reproduce it on failure.

---

## Conventions

- **`internal/cli` is the only package that prints.** Everything else returns errors or values. If
  you find yourself wanting `fmt.Println` in `internal/db` or `internal/seed`, return a warning
  string instead — `seed.Result.Warnings` exists for exactly that.
- **Errors name the step and give a hint.** Use `Failf(step, err, hint)`. It returns an error and
  deliberately does not print; `Execute` renders it. A hint should be a command the user can run.
- **Every exported identifier gets a doc comment**, and comments explain *why*, not *what*. The
  linter will not catch a comment that restates the code, but a reviewer will.
- **US spellings** — `misspell` runs with `locale: US`. `cancelled` is whitelisted in
  `.golangci.yml` because it is a value in the fixture's own `orders.status` CHECK, not prose.
- **Tests assert behaviour, not shape.** A test that only proves a function returns something is
  worse than no test, because it looks like coverage.
- **No new dependencies** without a real reason. The set is cobra, yaml.v3, pgx/v5, gofakeit/v7, and
  spec §3 forbids more.

---

## Recipes

### Add a column-name generation rule

`internal/seed/generate.go`, in `byName`. Two things matter:

1. Put it inside the right **type family** branch (`isTextual`, `isTemporal`, `isRealNumber`, bool,
   integer). A name rule must only fire when the column's type can hold the result — a column called
   `status` that happens to be an integer must still get an integer.
2. Run the result through `g.fitString(...)` for strings, or the clamping helpers for numbers, so it
   fits the column's declared limits.

Add a case to `TestValueIsNameAwareForTextColumns` (or the matching test) with a regex that proves the
shape, and if the rule can produce an over-long value, add it to `TestValueTruncatesToMaxLength`.

### Support a new column type

1. Add a `Type*` constant in `internal/db/model.go`.
2. Map every Postgres spelling of it in `NormalizeType` (`internal/db/introspect.go`) — check what
   `information_schema.columns.data_type` actually reports, not what the `CREATE TABLE` said.
3. Add it to `Generator.Supported` and give it a case in `byType`.
4. Add a row to `TestNormalizeType`, and a column of that type to `testapp/initdb/001_schema.sql` plus
   an assertion in `TestIntrospectFixtureColumnTypes`.

Skipping step 4 means the type is untested against a real Postgres, which is where the surprises are.

### Extend the fixture

`testapp/initdb/001_schema.sql`. It is a **branch-forcing** fixture, not a realistic app — add a table
or column when it opens a code path nothing else covers, and say in a comment which one.

Changing it means updating the counts asserted in `e2e_test.go`
(`Schema read: N tables, M foreign keys`, `Emptied N tables`) and in
`internal/db/introspect_integration_test.go`. Run both live tiers afterwards.

Do **not** add a `UNIQUE` constraint casually: random generation does not de-duplicate, so it will
fail the acceptance test for a reason that has nothing to do with the change you made.

### Change the seeding algorithm

Re-read [`ARCHITECTURE.md` §Seeding](ARCHITECTURE.md#seeding-internalseed--the-core) first, then keep
these true:

- `drawFK` keeps exactly **one** path that returns a value, and it comes from a pool.
- Pools stay keyed on the parent **column set** and hold **tuples**, so composite keys can only be
  drawn whole.
- Insertion stays `pgx.CopyFrom`. The integration test's µs/row budget will catch a regression to
  `INSERT`, but it will look like a mysterious performance failure — so do not do it.
- Run the integration tier. Unit tests cannot tell you that Postgres accepted the data.

---

## Git workflow

- Branch per unit of work; **the repo owner pushes and merges**.
- Never add commits to a branch that has already been merged — start a new one.
- End commit messages with the `Co-Authored-By` trailer if a tool helped write them.

Two traps that cost real time on this repo:

- **GitHub Desktop auto-stashes uncommitted work when you switch branches.** Commit before switching,
  or you will go hunting in `git stash list`.
- **Applying a stale stash can write conflict markers into the working tree** while the commit itself
  is clean. If the build suddenly fails with `expected statement, found '<<'`, check `git status` for
  `AA` (unmerged) files before assuming the commit is broken:
  ```sh
  git restore --source=HEAD --staged --worktree <files>
  ```
- On Windows, check line endings with `git ls-files --eol`, **not** `grep $'\r'` through a pipe —
  Git Bash translates pipes and gives false positives.

---

## Before opening a PR

- [ ] `make ci` passes, and `golangci-lint run ./...` reports 0 issues
- [ ] `go mod tidy` leaves `go.mod`/`go.sum` unchanged
- [ ] Integration tier run if you touched `internal/db` or `internal/seed`
- [ ] E2E tier run if you touched `internal/cli`, `internal/env`, or the output contract
- [ ] The rules that must never break still hold: rows go in with `COPY` rather than `INSERT`, no
      password appears in any output, the tool still works with no `loam.yml`, and every foreign key
      value comes from a parent row that was really generated
- [ ] Anything you decided not to do is written down in [`../LATER.md`](../LATER.md) with the reason

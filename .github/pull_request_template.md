## Phase

<!-- Which phase of docs/BUILD_SPEC.md section 6 does this implement? -->

Phase: <!-- 0 Skeleton CLI | 1 Compose up/down | 2 Connect | 3 Introspect | 4 Toposort | 5 Seeding | 6 Wire-up + reset -->

## What changed

<!-- One paragraph. Link the spec section(s) this PR satisfies. -->

## Spec acceptance test

> Quote the acceptance test for this phase from the spec.

- [ ] It passes
- [ ] It does not pass yet (explain why this PR still lands):

## How it was verified

<!-- Exact commands and their outcome. e.g. `make ci`, `go test ./...`, `loam up` against testapp/, timings. -->

```
```

## Section 12 non-negotiables

- [ ] **COPY, not INSERT** - bulk inserts go through `pgx.CopyFrom`; no row-by-row `INSERT` on the seeding path.
- [ ] **No secrets logged or persisted** - passwords masked in every printed URL (`env.MaskURL`); nothing beyond the user's own compose values is written to `.loam/state.json`.
- [ ] **Zero-config path intact** - the happy path still works with no `loam.yml`; a missing config file is not an error.
- [ ] **FK correctness** - every foreign key value is drawn from the parent table's generated PK pool; no invented references; parents seeded before children.
- [ ] **In scope only** - nothing from spec section 2; temptations were written to `LATER.md` instead.
- [ ] **Local only** - no network calls to any Loam backend, no daemon.
- [ ] **OS-portable** - `filepath.Join` for paths, no unix-only syscalls; CI cross-compile job is green.

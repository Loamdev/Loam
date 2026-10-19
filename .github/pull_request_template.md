## What this changes

<!-- One or two sentences in plain language. What is different after this PR? -->

## Why

<!-- The problem it solves. If it closes an issue, write "Fixes #123". -->

## How you tested it

<!-- The commands you ran and what happened. Paste the output if it helps. -->

```

```

## Checklist

- [ ] `make ci` passes (gofmt, go vet, go test, go build)
- [ ] `golangci-lint run ./...` reports no issues
- [ ] New behaviour has a test that would fail without this change
- [ ] Exported functions and types have doc comments
- [ ] `go mod tidy` leaves `go.mod` and `go.sum` unchanged

---

The sections below only apply to some changes. **Delete the ones that don't
apply to yours** — most PRs need none of them.

<details>
<summary><b>If you changed how data is generated or loaded</b></summary>

- [ ] Rows still go in with `pgx.CopyFrom`, never row-by-row `INSERT` — the speed is the whole point, and the integration tests enforce a per-row time budget
- [ ] Every foreign key value still comes from a parent row that was actually generated, never an invented one
- [ ] Ran the integration tests against a real Postgres (see [CONTRIBUTING.md](../docs/CONTRIBUTING.md#integration--a-live-postgres-holding-the-fixture-schema))

</details>

<details>
<summary><b>If you changed the CLI output, or how environments start and stop</b></summary>

- [ ] No password appears in any printed URL, or in `.loam/state.json`
- [ ] `loam up` still works in a repo with no `loam.yml` — a missing config file is not an error
- [ ] Ran the end-to-end tests: `go test -tags=e2e -v -timeout 15m .`

</details>

<details>
<summary><b>If you added a dependency</b></summary>

- [ ] Explained above why an existing dependency or the standard library would not do

Loam keeps a deliberately small dependency set. New ones are not forbidden,
but they need a reason.

</details>

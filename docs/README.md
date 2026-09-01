# Playbrige documentation

Start wherever matches what you need.

| I want to… | Read |
|---|---|
| Use the tool | [`../README.md`](../README.md) — install, the six commands, the flags, `playbrige.yml` |
| Understand how the code works, before changing it | **[`ARCHITECTURE.md`](ARCHITECTURE.md)** |
| Set up, run the tests, open a PR | **[`CONTRIBUTING.md`](CONTRIBUTING.md)** |
| Know what the tool is contractually required to do | [`PLAYBRIGE_MVP_BUILD_SPEC.md`](PLAYBRIGE_MVP_BUILD_SPEC.md) |
| Know why something was built this way, or what broke on the way | [`BUILD_SESSION_LOG.md`](BUILD_SESSION_LOG.md) |
| Know what was deliberately left out, and why | [`../LATER.md`](../LATER.md) |
| Understand the test fixture | [`../testapp/README.md`](../testapp/README.md) |

---

## Reading order for a new contributor

1. **[`../README.md`](../README.md)** — what the tool does. Then actually run it:
   `make build && ./bin/playbrige up --dir testapp`, look around the database, `down`.
2. **[`ARCHITECTURE.md`](ARCHITECTURE.md)** — the `up` pipeline, the package map, and the seeding
   algorithm. The *Invariants* table is the short version of what must never break.
3. **[`CONTRIBUTING.md`](CONTRIBUTING.md)** — the three test tiers and the recipes for common changes.
4. **[`PLAYBRIGE_MVP_BUILD_SPEC.md`](PLAYBRIGE_MVP_BUILD_SPEC.md)** §12 (non-negotiables) and §13
   (clarifications) — the rules, and the seven ambiguities that were resolved during the build.

Everything else is reference.

---

## The four documents, and what each is for

**`PLAYBRIGE_MVP_BUILD_SPEC.md`** is the contract. It defines required behaviour, what is
deliberately out of scope, and the acceptance test for each phase. §13 was added afterwards, recording
the seven questions the original spec left open and how each was answered.

**`ARCHITECTURE.md`** is the map. It explains the pipeline, who owns what, how seeding actually works,
and — importantly — the handful of places where the code deliberately departs from the spec because
the spec's suggestion was wrong. Two departures are called out explicitly, because "simplifying" them
back would reintroduce real bugs.

**`CONTRIBUTING.md`** is the how-to. Setup, the three test tiers, the eight CI jobs, conventions, and
step-by-step recipes for the changes people actually make.

**`BUILD_SESSION_LOG.md`** is the history. Phase-by-phase acceptance results with measured numbers,
the eight bugs found during the build and how each was diagnosed, and the environment constraints that
tripped things up. Read it when you want to know *why*, or when something feels arbitrary.

---

## The very short version

Playbrige runs a repo's `docker-compose.yml` as an isolated throwaway stack, reads the Postgres
schema, and bulk-loads foreign-key-correct synthetic data with `COPY`. One command up, one command
gone.

Two rules govern the whole codebase:

1. **Every foreign key value is drawn from a parent row that was actually generated — never
   invented.** Incoherent data would make the product worthless.
2. **Rows go in with `COPY`, never row-by-row `INSERT`.** The speed is the demo.

Current numbers on the bundled fixture: 9 tables, 8 foreign keys, **46,800 rows seeded in ~1.2s**,
whole `up`-to-`down` cycle **~15s**.

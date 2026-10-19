# testapp — the Loam test target

The fixture from [spec](../docs/BUILD_SPEC.md) section 10. It is the
"Postgres-backed app you've never seen" that every phase's acceptance test runs
`loam` against, and it is deliberately ordinary: a `docker-compose.yml` with
a `db` service and a `web` service, no `loam.yml`, no migrations tool, no
seed rows. If `loam up` needs anything more than this repo, the zero-config
requirement (spec 12.4) is broken.

Two services: `db` (`postgres:16-alpine`, published on host port **5433**,
`appuser` / `apppass` / `appdb` — throwaway local values) and `web`
(`nginx:alpine` on **8080**), which exists only so App URL detection has a
non-database published port to find. `initdb/` is mounted read-only at
`/docker-entrypoint-initdb.d`, so `initdb/001_schema.sql` runs on the first boot
of a fresh `pgdata` volume. There are no fixed `container_name`s, so two stacks
can run side by side under different `-p` project names.

## What each table exercises

| Table | Forces open |
|---|---|
| `categories` | `smallserial` PK; lookup parent with two children, three levels above `order_items` → the row-count scaling heuristic (`seed.RowCountFor`) must scale it down |
| `users` | Name-aware generation (`email`, `full_name`, `phone`, `city`, `pincode`, `bio`, `signup_url`, `*_at`); a real ENUM type (`user_status`); `varchar(6)` for the `MaxLength` branch; NOT NULL vs nullable; DEFAULT vs none |
| `employees` | Self-referencing FK `manager_id → employees.id` — spec section 4: a self-reference is **not** a cycle. Also a `date` and a `timestamp` without time zone |
| `products` | `jsonb`, `numeric(10,2)` money, nullable `boolean`, and a `smallint` FK (an FK that is not `bigint`) |
| `promotions` | `serial` (int, not bigint) PK, NOT NULL `date` + nullable `date`, naive `timestamp`; second child of `categories` |
| `orders` | `CHECK (status IN (...))` — the allowed-values path that is *not* an enum, which `db.AllowedValues` must parse and the seeder must draw from instead of the default distribution |
| `order_items` | `uuid` PK with `DEFAULT gen_random_uuid()`, the tail of the 3-level chain `users → orders → order_items`, and a **nullable** FK (`product_id`) for the occasional-null-FK branch |

Topological order must therefore put `categories` and `users` before `products`,
`promotions` and `orders`, and both `orders` and `products` before `order_items`.

Two things are absent on purpose: **no UNIQUE constraints** (random generation
does not de-duplicate — 10k `gofakeit.Email()` values will collide, and a unique
index would fail the acceptance test for the wrong reason) and **no seed rows**
(filling these tables is the product).

## Run it standalone

```sh
docker compose -f testapp/docker-compose.yml config     # validate, no engine needed
docker compose -f testapp/docker-compose.yml up -d       # start
docker compose -f testapp/docker-compose.yml down -v     # stop and reclaim the volume
```

Check the schema landed (empty counts are expected before seeding):

```sh
docker compose -f testapp/docker-compose.yml exec -T db \
  psql -U appuser -d appdb -c "\dt"
```

Or from the host, if you have `psql`:

```sh
psql "postgres://appuser:apppass@localhost:5433/appdb" -c "\dt"
```

Row counts per table, which is what a seeding run should change:

```sh
psql "postgres://appuser:apppass@localhost:5433/appdb" -c \
  "SELECT relname, n_live_tup FROM pg_stat_user_tables ORDER BY relname;"
```

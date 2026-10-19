-- =====================================================================
-- Loam test schema (spec section 10)
--
-- Runs once, as superuser against appdb, from /docker-entrypoint-initdb.d
-- on the first boot of a fresh pgdata volume.
--
-- Every table here exists to force a specific branch of phases 3-5 open:
--
--   categories   smallserial PK; lookup/parent referenced by TWO children and
--                sitting three levels up the chain (categories -> products ->
--                order_items), so the row-count scaling heuristic
--                (seed.RowCountFor) actually has to scale something down.
--   users        the wide "everything" table: enum type, bool, text, money-ish
--                and short bounded varchars (pincode varchar(6) proves the
--                MaxLength branch), NOT NULL vs nullable, DEFAULT vs no default,
--                and the name-aware generators (email / full_name / phone /
--                city / pincode / bio / signup_url / *_at).
--   employees    self-referencing FK (manager_id -> employees.id). Proves the
--                spec section 4 rule: a self-reference is NOT a cycle; toposort
--                must not deadlock and the seeder must draw manager_id from PKs
--                generated in the same batch (or leave it null).
--   products     jsonb, numeric money, nullable bool, and a smallint FK to
--                categories - an FK whose type is not bigint.
--   promotions   plain `serial` (int, not bigint) PK, a NOT NULL `date` and a
--                nullable `date`, plus a `timestamp` WITHOUT time zone, so both
--                timestamp branches and the date branch are covered. Second
--                child of categories.
--   orders       CHECK (status IN (...)) - the allowed-values path that is NOT
--                an enum type. db.AllowedValues must parse this, and the seeder
--                must draw from it instead of the generic status distribution.
--   order_items  uuid PK with DEFAULT gen_random_uuid(), the tail of a 3-level
--                FK chain (users -> orders -> order_items), and a NULLABLE FK
--                (product_id) so the "~10% null FK" branch has somewhere to fire.
--
-- Deliberately absent:
--   * No UNIQUE constraints anywhere. Random generation does not de-duplicate,
--     and 10k rows of gofakeit.Email() will collide. Adding a UNIQUE index here
--     would make the acceptance test fail for the wrong reason.
--   * No seed rows. Filling these tables is the whole point of Loam.
--   * gen_random_uuid() is core Postgres since 13 - no pgcrypto, no extension.
-- =====================================================================

-- A real Postgres ENUM type (not a CHECK) - exercises Column.UDTName and the
-- pg_enum lookup in db.AllowedValues.
CREATE TYPE user_status AS ENUM ('active', 'trial', 'churned', 'suspended');

-- ---------------------------------------------------------------------
-- categories: lookup / parent table, smallserial PK.
-- ---------------------------------------------------------------------
CREATE TABLE categories (
    id      smallserial PRIMARY KEY,
    name    varchar(60) NOT NULL,
    slug    varchar(60)
);
COMMENT ON TABLE categories IS 'Lookup parent; drives the row-count scaling heuristic.';

-- ---------------------------------------------------------------------
-- users: the wide column-generation table. Root of the main FK chain.
-- ---------------------------------------------------------------------
CREATE TABLE users (
    id          bigserial PRIMARY KEY,
    email       varchar(255) NOT NULL,
    full_name   varchar(120),
    phone       varchar(20),
    city        varchar(80),
    pincode     varchar(6),
    status      user_status NOT NULL DEFAULT 'trial',
    is_verified boolean NOT NULL DEFAULT false,
    bio         text,
    signup_url  text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz
);
COMMENT ON TABLE users IS 'Name-aware generation, enum status, nullability and MaxLength.';

-- ---------------------------------------------------------------------
-- employees: self-referencing FK. Not a cycle.
-- ---------------------------------------------------------------------
CREATE TABLE employees (
    id             bigserial PRIMARY KEY,
    full_name      varchar(120),
    manager_id     bigint NULL REFERENCES employees(id),
    hired_on       date,
    last_synced_at timestamp
);
COMMENT ON TABLE employees IS 'Self-reference (manager_id -> employees.id); date and naive timestamp.';

-- ---------------------------------------------------------------------
-- products: jsonb, money, nullable bool, smallint FK to categories.
-- ---------------------------------------------------------------------
CREATE TABLE products (
    id          bigserial PRIMARY KEY,
    sku         varchar(40) NOT NULL,
    name        varchar(200),
    price       numeric(10,2) NOT NULL,
    metadata    jsonb,
    in_stock    boolean,
    category_id smallint NULL REFERENCES categories(id)
);
COMMENT ON TABLE products IS 'jsonb, numeric money, nullable smallint FK to categories.';

-- ---------------------------------------------------------------------
-- promotions: int (serial) PK, date columns, naive timestamp.
-- Second child of categories.
-- ---------------------------------------------------------------------
CREATE TABLE promotions (
    id           serial PRIMARY KEY,
    code         varchar(20) NOT NULL,
    category_id  smallint NULL REFERENCES categories(id),
    discount_pct numeric(5,2),
    starts_on    date NOT NULL,
    ends_on      date,
    created_at   timestamp
);
COMMENT ON TABLE promotions IS 'serial (int) PK, NOT NULL date, nullable date, timestamp without time zone.';

-- ---------------------------------------------------------------------
-- orders: CHECK-constraint allowed values (not an enum type).
-- ---------------------------------------------------------------------
CREATE TABLE orders (
    id        bigserial PRIMARY KEY,
    user_id   bigint NOT NULL REFERENCES users(id),
    total     numeric(12,2) NOT NULL,
    status    varchar(20) NOT NULL
                  CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled', 'refunded')),
    placed_at timestamptz NOT NULL,
    note      text NULL
);
COMMENT ON TABLE orders IS 'CHECK (status IN (...)) allowed-values path; NOT NULL FK to users.';

-- ---------------------------------------------------------------------
-- order_items: uuid PK, 3-level FK chain, nullable FK.
-- ---------------------------------------------------------------------
CREATE TABLE order_items (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id     bigint NOT NULL REFERENCES orders(id),
    product_id   bigint NULL REFERENCES products(id),
    product_name varchar(200),
    price        numeric(10,2) NOT NULL,
    qty          int NOT NULL
);
COMMENT ON TABLE order_items IS 'uuid PK with DEFAULT, tail of users->orders->order_items, nullable FK.';

-- ---------------------------------------------------------------------
-- Indexes. Non-unique on purpose (see header).
-- ---------------------------------------------------------------------
CREATE INDEX idx_users_status ON users (status);
CREATE INDEX idx_orders_user_id ON orders (user_id);
CREATE INDEX idx_orders_status ON orders (status);
CREATE INDEX idx_order_items_order_id ON order_items (order_id);
CREATE INDEX idx_products_category_id ON products (category_id);

-- ---------------------------------------------------------------------
-- tenant_users / tenant_events: a COMPOSITE primary key and a COMPOSITE
-- foreign key referencing it.
--
-- This pair exists because composite keys used to abort `up` outright: the
-- FK's two columns were drawn from their parent pools independently, so the
-- (tenant_id, user_ref) pairs existed in no parent row and Postgres rejected
-- the whole COPY. The seeder now draws a composite key as one tuple, and
-- these tables keep it that way (spec section 13.3).
-- ---------------------------------------------------------------------
CREATE TABLE tenant_users (
    tenant_id int NOT NULL,
    user_ref  int NOT NULL,
    label     varchar(60),
    PRIMARY KEY (tenant_id, user_ref)
);
COMMENT ON TABLE tenant_users IS 'Composite primary key.';

CREATE TABLE tenant_events (
    id        bigserial PRIMARY KEY,
    tenant_id int NOT NULL,
    user_ref  int NOT NULL,
    kind      varchar(30),
    FOREIGN KEY (tenant_id, user_ref) REFERENCES tenant_users (tenant_id, user_ref)
);
COMMENT ON TABLE tenant_events IS 'Composite foreign key; must be drawn as a whole tuple.';

CREATE INDEX idx_tenant_events_key ON tenant_events (tenant_id, user_ref);

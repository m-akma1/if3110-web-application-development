#!/bin/sh
# Sample schema and data for the demo target, created as the app role so it owns every object.
set -eu

: "${TARGET_APP_USER:=app_target}"

psql -v ON_ERROR_STOP=1 \
  --username "$POSTGRES_USER" \
  --dbname "$POSTGRES_DB" \
  -v app_user="$TARGET_APP_USER" <<'SQL'
SET ROLE :"app_user";

CREATE TABLE customers (
    id          SERIAL PRIMARY KEY,
    full_name   VARCHAR(120) NOT NULL,
    email       VARCHAR(160) NOT NULL UNIQUE,
    segment     VARCHAR(32)  NOT NULL DEFAULT 'SMB' CHECK (segment IN ('SMB', 'ENTERPRISE', 'INDIVIDUAL')),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE products (
    id          SERIAL PRIMARY KEY,
    sku         VARCHAR(32)   NOT NULL UNIQUE,
    name        VARCHAR(120)  NOT NULL,
    price_idr   NUMERIC(14,2) NOT NULL CHECK (price_idr >= 0),
    stock       INTEGER       NOT NULL DEFAULT 0
);

CREATE TABLE orders (
    id           SERIAL PRIMARY KEY,
    customer_id  INTEGER     NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    status       VARCHAR(24) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PAID', 'SHIPPED', 'CANCELLED')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_orders_customer ON orders(customer_id);
CREATE INDEX idx_orders_status ON orders(status);

CREATE TABLE order_items (
    order_id    INTEGER       NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    product_id  INTEGER       NOT NULL REFERENCES products(id),
    quantity    INTEGER       NOT NULL CHECK (quantity > 0),
    unit_price  NUMERIC(14,2) NOT NULL,
    PRIMARY KEY (order_id, product_id)
);

INSERT INTO customers (full_name, email, segment)
SELECT 'Customer ' || g, 'customer' || g || '@example.com',
       (ARRAY['SMB', 'ENTERPRISE', 'INDIVIDUAL'])[1 + g % 3]
FROM generate_series(1, 60) AS g;

INSERT INTO products (sku, name, price_idr, stock)
SELECT 'SKU-' || LPAD(g::text, 4, '0'), 'Product ' || g, (g * 12500)::numeric, g * 3
FROM generate_series(1, 30) AS g;

INSERT INTO orders (customer_id, status, created_at)
SELECT 1 + (g % 60), (ARRAY['PENDING', 'PAID', 'SHIPPED', 'CANCELLED'])[1 + g % 4],
       NOW() - (g || ' hours')::interval
FROM generate_series(1, 200) AS g;

INSERT INTO order_items (order_id, product_id, quantity, unit_price)
SELECT o.id, 1 + (o.id * 7 % 30), 1 + o.id % 5, p.price_idr
FROM orders o
JOIN products p ON p.id = 1 + (o.id * 7 % 30);
SQL

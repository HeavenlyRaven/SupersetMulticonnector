-- Deliberately a duplicate of dev/mysql-init/001-sample.sql — see
-- test/testkit/init/postgres/001-sample.sql's comment for why. Loaded
-- once by the mysql image's standard docker-entrypoint-initdb.d
-- mechanism — a .sql file, not a shell script.
CREATE TABLE IF NOT EXISTS orders (
    id INT PRIMARY KEY,
    customer_id INT NOT NULL,
    amount_cents INT NOT NULL
);

INSERT INTO orders (id, customer_id, amount_cents) VALUES
    (1, 1, 15000),
    (2, 1, 4200),
    (3, 2, 9900),
    (4, 3, 25000);

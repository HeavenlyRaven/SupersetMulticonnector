-- Sample data for local `fedctl up --dev` testing only. Loaded once by
-- the postgres image's standard docker-entrypoint-initdb.d mechanism —
-- a .sql file, not a shell script.
CREATE TABLE IF NOT EXISTS customers (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    region TEXT NOT NULL
);

INSERT INTO customers (id, name, region) VALUES
    (1, 'Acme Corp', 'us-east'),
    (2, 'Globex', 'eu-west'),
    (3, 'Initech', 'us-west');

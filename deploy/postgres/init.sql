CREATE TABLE IF NOT EXISTS services
(
    name TEXT PRIMARY KEY,
    api_key TEXT NOT NULL UNIQUE,
    retention_days INTEGER NOT NULL CHECK (retention_days > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS users
(
    id BIGSERIAL PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('admin', 'viewer'))
);

CREATE TABLE IF NOT EXISTS user_services
(
    user_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    service_name TEXT NOT NULL REFERENCES services (name) ON DELETE CASCADE,
    PRIMARY KEY (user_id, service_name)
);

CREATE TABLE IF NOT EXISTS alert_rules
(
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    service TEXT NOT NULL REFERENCES services (name) ON DELETE CASCADE,
    level TEXT NOT NULL CHECK (level IN ('trace', 'debug', 'info', 'warn', 'error', 'fatal')),
    threshold INTEGER NOT NULL CHECK (threshold > 0),
    window_minutes INTEGER NOT NULL CHECK (window_minutes > 0),
    cooldown_minutes INTEGER NOT NULL CHECK (cooldown_minutes >= 0),
    webhook_url TEXT NOT NULL,
    last_fired_at TIMESTAMPTZ
);

INSERT INTO services (name, api_key, retention_days)
VALUES ('demo-service', 'demo-key-123', 30)
ON CONFLICT (name) DO NOTHING;

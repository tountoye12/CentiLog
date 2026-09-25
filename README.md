# Centilog

Centilog is a centralized logging platform for collecting logs from many servers, normalizing and protecting them, and exploring them across services.

This README describes the project as it exists today. [`SPEC.md`](SPEC.md) remains the authoritative source for the complete goal, architecture, schema, repository layout, and build phases.

## Project status

Implemented project areas:

- Go shared packages for configuration, schema normalization, and sensitive-data redaction.
- Go ingest API with API-key authentication, gzip batches, validation, backpressure, and Redis Stream writes.
- Go agent with JSON/syslog/text parsing, time-zone handling, file tailing, stack-trace assembly, filtering, sampling, disk buffering, and retrying delivery.
- Go Query API with login, JWT authentication, service/user administration, permission-scoped ClickHouse queries, search, trace, and statistics endpoints.
- Next.js App Router dashboard with login, Search, Noise, Cost, and admin Settings pages.
- Docker Compose configuration and SQL initialization for ClickHouse, Redis, and PostgreSQL.

Not implemented yet: the Processor that consumes Redis Streams and writes logs into ClickHouse, alert evaluation/delivery, and chaos testing. Until the Processor is added, the ingest API can accept logs into Redis but they will not flow into ClickHouse.

## Problems Centilog addresses

| Centralized logging problem | Centilog approach |
| --- | --- |
| Volume and cost | Agent-side level filtering and sampling, per-service volume reporting, and compressed ClickHouse storage. |
| Inconsistent formats | Convert JSON, syslog, and plain text into the Centilog Schema. |
| Time-zone mismatch | Per-file time zones, UTC storage, millisecond precision, future-time correction flags, and clock-skew warnings. |
| Sensitive data | Redact passwords, tokens, API keys, email addresses, and Luhn-valid card numbers in both agent and ingest API. |
| Ingest/storage failure | Redis Streams separate ingest from storage; ingest responds with HTTP 429 when the queue is full. |
| Transit loss | Gzip batches are written to the agent's disk buffer before offsets are committed; failed deliveries are retried. |
| Noisy logs | Group similar messages into patterns with counts and last-seen times. |
| Retention | Retention days are stored per service and enforced by ClickHouse table TTL. |
| Access control | Admin/viewer roles and per-service viewer permissions; service restrictions are applied in API/database queries. |
| Missing correlation | `trace_id` supports a cross-service trace view ordered by event time. |

## Architecture

```text
Go Agent
  -> Go Ingest API (:8080)
  -> Redis Stream queue
  -> Go Processor (planned)
  -> ClickHouse

Next.js Dashboard (:3000)
  -> Go Query API (:8081)
  -> ClickHouse (parameterized log and statistics queries)
  -> PostgreSQL (users, services, API keys, retention, alert rules)
```

The Go services share the root module `centilog`. The dashboard is a separate npm package under `web/`. PostgreSQL, Redis, and ClickHouse are defined in `deploy/docker-compose.yml`.

## Requirements

- Go 1.23 or newer.
- Node.js 20 or newer and npm.
- Docker Compose for the included local database stack, or equivalent PostgreSQL, Redis, and ClickHouse services already running locally.

The Compose file uses local development credentials. Do not reuse them in a shared or production environment. Set production credentials and `JWT_SECRET` outside source control.

## Quick start

From the repository root:

1. Start the local data services if you choose to use Docker Compose:
	```bash
	docker compose -f deploy/docker-compose.yml up -d
	docker compose -f deploy/docker-compose.yml ps
	```
	Wait for ClickHouse, Redis, and PostgreSQL to report healthy. Their named volumes preserve data when containers stop. The SQL initialization scripts run when their database volumes are first initialized.

2. Download Go dependencies and create the sample log files:
	```bash
	go mod tidy
	mkdir -p sample-logs
	touch sample-logs/app.log sample-logs/legacy.log
	```

3. Create the first administrator once. Enter the password at the prompt; do not commit it or place it in source files:
	```zsh
	read -r -s 'ADMIN_PASSWORD?Admin password: '
	printf '\n'
	go run ./cmd/api create-admin admin@example.test "$ADMIN_PASSWORD"
	unset ADMIN_PASSWORD
	```

4. In separate terminals, start the ingest API and Query API:
	```bash
	go run ./cmd/ingest
	```
	```bash
	export JWT_SECRET="$(openssl rand -hex 32)"
	go run ./cmd/api
	```
	The APIs listen on ports `8080` and `8081`. The Query API warns and uses a temporary random JWT secret when `JWT_SECRET` is unset; tokens then stop working when it restarts.

5. Start the agent in another terminal:
	```bash
	go run ./cmd/agent
	```
	It loads `configs/agent.yaml`. Update its API key and watched paths to match your services and log files.

6. Start the dashboard:
	```bash
	npm --prefix web install
	npm --prefix web run dev
	```
	Open [http://localhost:3000](http://localhost:3000). The Next.js rewrite proxies browser `/api/*` requests to `http://localhost:8081/api/*`.

To stop a Go service, press Ctrl+C in its terminal. If using Compose, `docker compose -f deploy/docker-compose.yml stop` stops containers without deleting their named volumes.

## Deploy to a VPS

The one-time installer supports fresh Debian and Ubuntu VPS hosts with `systemd`. Clone the repository and run the installer from its root:

```bash
git clone https://github.com/tountoye12/CentiLog.git
cd CentiLog
bash deploy/deploy.sh
```

The script installs Docker Engine and the Compose plugin if needed, creates a root `.env` with random PostgreSQL, Redis, ClickHouse, JWT, and demo-service credentials, builds and starts the services, then prompts for the initial admin email and password. An existing `.env` is retained and must contain all required variables. Store and back up `.env` securely; it is ignored by Git and set to mode `600`.

Open `http://<VPS-IP>:3000` after deployment and allow TCP port `3000` through the VPS firewall. The database ports bind to loopback only. Put an HTTPS reverse proxy in front of the dashboard before using it with real accounts or exposing credentials over the internet.

The installer does not create a Processor. The agent can send accepted logs to Redis, but they will not reach ClickHouse until `cmd/processor` is implemented and added to deployment. Alert processing and chaos testing are also not implemented yet.

## Tests and checks

Run Go tests from the repository root:

```bash
go test ./...
```

Run dashboard lint and production build:

```bash
npm --prefix web run lint
npm --prefix web run build
```

The agent has parser, configuration, tailing, buffering, and sender tests. The Query API has authentication, permission, and parameterized-query tests.

## Agent configuration

The example configuration is [`configs/agent.yaml`](configs/agent.yaml). It sets the ingest URL and development API key, host/environment defaults, disk-buffer directory and size, batch size, flush interval, minimum level, per-level sampling, and watched files. The sample watches:

- `./sample-logs/app.log` in auto-detect mode using local time for timestamps without an offset.
- `./sample-logs/legacy.log` as text using `Asia/Kolkata` for timestamps without an offset.

Supported formats are `auto`, `json`, `syslog`, and `text`. Time-zone data is embedded for portability. The agent reads complete new lines, persists per-file offsets, restarts from the beginning if a file shrinks, joins stack-trace continuation lines, filters and samples before redaction, and writes gzip batches to disk before advancing offsets.

## APIs

### Ingest API (`:8080`)

| Method and route | Purpose |
| --- | --- |
| `GET /healthz` | Health check. |
| `POST /v1/logs` | Accept a JSON array of logs, optionally gzip-compressed, with `X-Api-Key`. Returns accepted/rejected counts. |

Ingest derives the service and retention from the API key, normalizes and redacts each valid record again, and writes it to the Redis Stream. Bodies are limited to 10 MiB and batches to 10,000 logs. Queue length above one million entries produces HTTP 429 with `Retry-After`.

### Query API (`:8081`)

Protected routes require `Authorization: Bearer <JWT>`.

| Method and route | Access and purpose |
| --- | --- |
| `POST /api/login` | Public; returns a 12-hour JWT. |
| `GET /api/me` | Authenticated; current user and role. |
| `GET /api/services` | Authenticated; all services for admins, assigned services for viewers. |
| `POST /api/services` | Admin; creates a service and returns its generated API key once. |
| `PUT /api/services/{name}` | Admin; updates retention days. |
| `POST /api/users` | Admin; creates an admin or viewer and assigns viewer services. |
| `GET /api/logs` | Authenticated; filters by `from`/`to` (Unix milliseconds), `service`, `level`, `text`, `trace_id`, `before` (Unix milliseconds), and `limit` (maximum 1,000), newest first. |
| `GET /api/trace/{id}` | Authenticated; permitted-service logs for that trace from the last seven days, oldest first. |
| `GET /api/stats/volume?days=N` | Authenticated; daily log counts and uncompressed serialized bytes per permitted service. `days` is 1–365. |
| `GET /api/stats/patterns?hours=N` | Authenticated; top 50 message patterns from permitted services. Numbers become `N`; long hex IDs become `ID`. `hours` is 1–720. |

Service permissions are enforced server-side: viewer service names are loaded from PostgreSQL and passed as ClickHouse query parameters. Search values are also bound as ClickHouse HTTP query parameters; they are not interpolated into SQL.

### Dashboard routes

- `/login`: authenticate with an API account.
- `/`: log search.
- `/noise`: message patterns.
- `/cost`: per-service daily volume and raw bytes.
- `/settings`: admin-only service retention, service creation, and user permissions.

The browser stores the JWT in local storage. The API helper attaches it to requests, displays server error messages, and clears the token and redirects to `/login` after HTTP 401.

## Centilog Schema

Each normalized log row contains:

| Field | Description |
| --- | --- |
| `timestamp` | Event time in UTC, millisecond precision. |
| `service` | Service name. |
| `host` | Source host. |
| `env` | Deployment environment. |
| `level` | `trace`, `debug`, `info`, `warn`, `error`, or `fatal`. |
| `message` | Normalized and redacted message. |
| `trace_id` | Cross-service request identifier. |
| `attributes` | String-to-string map of additional fields. |
| `redacted` | Whether sensitive content was changed. |
| `ingested_at` | Ingest time in UTC, millisecond precision. |
| `retention_days` | Service retention period. |

ClickHouse stores `centilog.logs` with `MergeTree`, daily partitions, ordering by `(service, level, timestamp)`, low-cardinality repetitive text fields, trace/message skip indexes, and per-row retention TTL.

## Repository layout

```text
.
├── cmd/
│   ├── agent/       # Go collector, parsers, disk buffer, sender
│   ├── api/         # Go Query API
│   └── ingest/      # Go ingest API
├── configs/         # Agent YAML configuration
├── deploy/          # Compose and database initialization SQL
├── internal/
│   ├── config/      # Environment defaults and connection strings
│   ├── redact/      # Sensitive-data redaction
│   └── schema/      # Log schema, level normalization, timestamp handling
├── web/             # Next.js, TypeScript, Tailwind dashboard
├── SPEC.md          # Project source of truth
└── go.mod           # Single Go module: centilog
```

`cmd/processor` is a planned directory and is not present yet. Until it is implemented, Redis Stream entries are not consumed into ClickHouse. Alerts and chaos testing are also future build phases.

## Authors

- [Mamadou Diallo](https://github.com/tountoye12)

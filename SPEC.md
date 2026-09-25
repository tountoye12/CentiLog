# Centilog Project Specification

This document is the single source of truth for Centilog's goals, architecture, data schema, repository layout, and implementation phases. Implementation decisions and project documentation must remain consistent with this specification.

## Goal

Collect logs from many servers into one centralized platform without the usual problems of cost, inconsistent formats, unreliable timestamps, sensitive data exposure, outages, log loss, noise, retention, access control, and missing cross-service correlation.

## The 10 Problems and Solutions

1. **Volume and cost**
   - The agent drops low-level logs and samples noisy ones before sending them.
   - The dashboard shows costs per service.
   - Logs are stored in compressed form.

2. **Inconsistent formats**
   - The agent converts JSON, syslog, and plain-text log lines into the Centilog Schema.

3. **Timestamp and time-zone mismatch**
   - Time zones can be configured per file.
   - All stored timestamps use UTC.
   - Future timestamps are corrected and flagged in the log's `attributes`.
   - The platform warns when it detects clock drift.

4. **Sensitive data**
   - The agent redacts passwords, tokens, API keys, email addresses, and credit-card numbers before sending logs.
   - The ingest API applies the same redaction again as a second protection layer.
   - Credit-card numbers are redacted only when they pass the Luhn check.
   - Redacted logs have `redacted` set to `true`.

5. **Single point of failure**
   - A Redis Stream queue separates ingestion from storage processing.
   - The ingest API returns HTTP `429 Too Many Requests` when the queue is too full to accept more logs.

6. **Log loss in transit**
   - The agent writes batches to a disk buffer before sending them.
   - Failed sends are retried with exponential backoff.

7. **Noise**
   - The dashboard includes a page that groups similar messages into patterns and shows a count for each pattern.

8. **Retention**
   - Retention is configured in days per service.
   - The database automatically enforces each service's retention period.

9. **Access control**
   - Users log in with either the admin or viewer role.
   - Permissions can be set per service.
   - Every query applies the requesting user's service permissions.

10. **No correlation across services**
    - Logs include a `trace_id` field.
    - The dashboard has a trace view that shows one request's logs across services in time order.

## Architecture

```text
Go Agent
  -> Go Ingest API (:8080)
  -> Redis Stream queue
  -> Go Processor
  -> ClickHouse

Next.js Dashboard (:3000)
  -> Go Query API (:8081)
  -> ClickHouse (log queries)
  -> PostgreSQL (users, services, API keys, retention, alert rules)
```

- The agent, ingest API, processor, and query API are Go services in one Go module named `centilog`.
- The web dashboard uses Next.js, TypeScript, and Tailwind CSS.
- PostgreSQL, Redis, and ClickHouse run in Docker Compose.
- The ingest API is the log-write entry point. The query API serves dashboard reads and uses PostgreSQL for application metadata and access control.

## Centilog Schema

Each normalized log record uses these fields:

| Field | Meaning and requirements |
| --- | --- |
| `timestamp` | Event time in UTC with millisecond precision. |
| `service` | Name of the service that produced the log. |
| `host` | Name or identifier of the source host. |
| `env` | Deployment environment. |
| `level` | One of `trace`, `debug`, `info`, `warn`, `error`, or `fatal`. |
| `message` | Normalized log message. |
| `trace_id` | Trace identifier used to correlate logs across services. |
| `attributes` | String-to-string map for additional structured fields and processing flags. |
| `redacted` | Boolean indicating whether sensitive data was redacted. |
| `ingested_at` | Time the platform accepted the record, in UTC with millisecond precision. |
| `retention_days` | Retention period in days for the record's service. |

## Repository Layout

```text
centilog/
├── go.mod                 # Single Go module: centilog
├── cmd/
│   ├── ingest/            # Ingest API entry point
│   ├── processor/         # Queue consumer and storage entry point
│   ├── agent/             # Server-side log collection entry point
│   └── api/               # Query API entry point
├── internal/              # Shared, non-public Go packages
├── configs/               # Application configuration
├── deploy/                # Docker Compose and deployment files
├── scripts/               # Development and operational scripts
└── web/                   # Next.js, TypeScript, and Tailwind dashboard
```

## Build Phases

Implement the project in this order:

1. **Infrastructure**: establish the Go module, Docker Compose services, and basic project structure.
2. **Schema and redaction**: define the Centilog Schema and implement the shared redaction rules.
3. **Ingest**: accept, validate, redact, and enqueue log batches; return HTTP 429 when the queue is too full.
4. **Processor**: read queued batches and write normalized records to ClickHouse.
5. **Agent**: parse supported formats, apply configured filtering and sampling, buffer batches on disk, and retry failed sends.
6. **Query API**: provide authenticated, permission-checked reads and use PostgreSQL for application metadata.
7. **Dashboard**: add log exploration, per-service costs, message patterns, and trace views.
8. **Alerts**: implement alert rules stored in PostgreSQL and surface triggered alerts.
9. **Chaos testing**: test failure and recovery behavior, including queue saturation, unavailable storage, interrupted network sends, and recovery without silent log loss.

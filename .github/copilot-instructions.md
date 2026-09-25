# Centilog Project Instructions

## Source of Truth

- Always follow [`SPEC.md`](../../SPEC.md) for Centilog's goals, architecture, schema, folder layout, and build phases. If a request conflicts with it, explain the conflict and ask before changing the specification.
- The user is not a programmer. For every implementation task, give exact, numbered steps to run and verify the result, including copyable commands and what success looks like. Explain when a task is documentation-only or the application is not runnable yet; never invent commands that the project does not provide.

## Changes and Safety

- Keep changes small, focused, and runnable. Do not rewrite unrelated files or remove existing user changes.
- Do not add application code before the user asks for it.
- Never log secrets or include secret values in errors, examples, or test output. Use placeholders for credentials.

## Data Handling and Go

- Store and compare all timestamps in UTC; preserve millisecond precision required by the Centilog Schema.
- Run sensitive-data redaction in both the agent and the ingest API. Treat ingest-side redaction as mandatory defense in depth, even if the agent has already redacted a record.
- Prefer the Go standard library before adding dependencies; justify a dependency when one is needed.
- Go services must shut down gracefully and include focused tests for parsing and redaction behavior.

## Frontend

- Use Next.js App Router, TypeScript, and Tailwind CSS for the dashboard, consistent with `SPEC.md`.

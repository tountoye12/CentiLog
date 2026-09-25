---
name: centilog-development
description: 'Use when building, changing, testing, or debugging Centilog features, including Go services, log parsing, redaction, timestamps, APIs, storage, and the Next.js dashboard. Follow @SPEC.md and explain exact run and verification steps for a non-programmer.'
user-invocable: true
disable-model-invocation: false
---

# Centilog Development

Use this skill for Centilog implementation and maintenance tasks. The project's always-on instructions in `.github/copilot-instructions.md` also apply.

## Required Workflow

1. Read `@SPEC.md` before planning or changing application behavior. Treat it as the source of truth for goals, architecture, schema, folder layout, and build order. If a request conflicts with it, explain the conflict and ask before changing the specification.
2. Inspect the relevant code, tests, and working-tree changes. Preserve user edits and avoid unrelated files.
3. Make the smallest focused change that leaves the project runnable. Do not add application code unless the user requested implementation.
4. Add or update focused tests for changed behavior. Go parsing and redaction behavior must have tests.
5. Run the narrowest relevant tests or checks, then any broader required checks. Report what ran and whether it passed; never claim an unrun check passed.
6. In the final response, give exact numbered run and verify steps for a non-programmer. Include copyable commands and expected success output. If the project is not runnable yet or the change is documentation-only, say so and give the appropriate verification steps instead of inventing commands.

## Centilog Requirements

- Follow the architecture, build phases, and Centilog Schema in `@SPEC.md`.
- Store timestamps in UTC with the schema's millisecond precision. Parse configured per-file time zones as specified, and flag corrected future timestamps.
- Redact passwords, tokens, API keys, email addresses, and Luhn-valid credit-card numbers in both the agent and the ingest API. Ingest-side redaction is mandatory defense in depth.
- Never log secrets or expose them in errors, examples, fixtures, or test output. Use clearly fake placeholders in examples.
- Prefer the Go standard library before adding dependencies. Add a dependency only when it provides meaningful value and is justified.
- Go services must shut down gracefully. Test log parsing and redaction, including sensitive-data edge cases.
- Build the dashboard with Next.js App Router, TypeScript, and Tailwind CSS.
- Keep changes small, focused, and runnable. Do not rewrite unrelated files, change public interfaces unnecessarily, or remove user data.

# Centilog Dashboard

This package contains Centilog's Next.js App Router dashboard. The project-wide architecture, schema, setup, and API documentation is in the [root README](../README.md) and [`SPEC.md`](../SPEC.md).

From the repository root, start the development server with:

```bash
npm --prefix web run dev
```

Open `http://localhost:3000`. Browser requests under `/api/*` are rewritten to the Query API at `http://localhost:8081/api/*`.

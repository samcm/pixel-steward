# Pixel Steward

Pixel Steward is a self-hosted control plane that gives autonomous agents
time-limited ownership of a physical pixel display without giving them the
display credentials or turning inference into a frame loop.

An agent uses a bounded inference session to create assets, timelines, or an
arbitrary local renderer. Pixel Steward archives output, validates it, samples
the latest framebuffer at a configured maximum rate, enforces blackout windows,
and is the only process allowed to talk to the display adapter.

The project is infrastructure-neutral. Hostnames, addresses, provider keys,
personas, schedules, and deployment inventory belong in a separate private
GitOps repository. This repository contains only reusable code, schemas,
example values, and deployment primitives.

## What it includes

- weighted persona selection, cooldowns, 24-hour leases, and a strict
  timezone-aware blackout;
- hard per-lease call, token, active-runtime, scene-commit, and optional cost
  ledgers visible to the persona through `studio_budget`;
- provider-native OpenCode token, reasoning-token, cache-token, and cost event
  capture;
- flexible read-only PostgreSQL history through `studio_sql`;
- a disposable-executor contract for arbitrary commands and Docker workloads;
- one-shot scene publishing and budget-free sampling of locally rendered files;
- immutable source/final-frame archival to a filesystem or S3-compatible store;
- an operator dashboard with the live canvas, persona overrides, leases,
  budgets, display health, and full event history.

## Design invariants

- Exactly one controller owns display publication.
- Blackout and lease expiry are enforced outside the agent runtime.
- Model calls have hard token, call, time, scene-commit, and optional cost caps.
- Agents can inspect current accounting but cannot increase budgets or change
  reasoning effort.
- Local renderers can generate every frame without consuming inference tokens.
- Provider-native token and cost fields are retained without inventing missing
  subscription costs.
- Configuration can reference environment variables, but secrets are never
  accepted in the version-controlled schema.

## Quick start

```sh
docker compose up --build
```

The dashboard is then available at `http://127.0.0.1:18080`.

The example uses a fake display and local filesystem object storage. Production
deployments can select PostgreSQL, S3-compatible storage, and an HTTP display
adapter without changing agent-facing contracts. The included compose stack is
a deliberately inert development setup: fake display, disabled inference, and
disabled sandbox execution.

## Agent tools

- `studio_budget()` returns current usage, reservations, remaining allowances,
  lease end, and the effective operator-controlled reasoning setting.
- `studio_sql({query})` accepts a read-only PostgreSQL `SELECT` or `WITH` query
  and returns at most 500 rows.
- `studio_exec({command, timeout_ms})` runs arbitrary shell in the configured
  disposable executor, never in the controller.
- `studio_publish({path})` publishes one model-driven scene from the sandbox.
- `studio_watch({path, fps})` samples a locally rendered framebuffer without an
  inference call per frame.
- `studio_schedule(...)` creates one-shot or recurring model wakes inside the
  current lease.

There is intentionally no tool for changing reasoning effort, budgets, leases,
blackout, or display rate.

## Production deployment

Keep desired configuration, persona briefs, inventory, encrypted secrets, and
image digests in a separate private GitOps repository. Pass secrets through
environment variables populated by that deployment system. The controller
image contains no environment-specific defaults.

`sandbox.driver: http` connects the controller to a separately isolated
executor. The companion `pixel-steward executor` command provides the executor
API, but network isolation, VM lifecycle, and the optional Docker daemon remain
deployment concerns. `sandbox.driver: local` is for tests only.

## License

MIT

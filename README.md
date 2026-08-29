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
- inference routing selected independently from persona identity and character;
- hard per-lease call, token, active-runtime, scene-commit, and optional cost
  ledgers visible to the persona through `studio_budget`;
- pluggable Hermes and OpenCode runtimes with provider-native token,
  reasoning-token, cache-token, model-call, and cost capture;
- fresh Hermes sessions per wake with durable, isolated per-persona memory and
  exact persisted assistant/tool transcripts;
- flexible read-only PostgreSQL history through `studio_sql`;
- agent-authored 1-3 sentence journal entries, exposed as `history_journal`, so
  future agents can recover intent without decoding runtime telemetry;
- a disposable-executor contract for arbitrary commands and Docker workloads;
- one-shot scene publishing and budget-free sampling of locally rendered files;
- immutable source/final-frame archival to a filesystem or S3-compatible store;
- an operator interface whose primary surface is the agent's real transcript —
  verbatim model text and full tool calls with their exact commands, inputs,
  status and output — alongside the live canvas, truthful display state,
  persona deep dives with every controller prompt verbatim, leases, budgets,
  inference costs, the frame archive, and agent-authored journal history.

## Design invariants

- Exactly one controller owns display publication.
- Daylight arms the display without starting adapter fallback content; the
  panel turns on with the first steward frame, which is then held exclusively
  until another frame or blackout.
- Persona configuration never selects a provider, endpoint, model, or reasoning
  level; the controller binds its independently configured inference profile to
  each lease and records that binding in history.
- Blackout and lease expiry are enforced outside the agent runtime.
- A configured test window is an absolute RFC3339 deadline: it can temporarily
  override blackout, then fails closed without an operator cleanup action.
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

## Operator interface

The interface is a Preact + TypeScript application in `web/`, built by Vite into
content-hashed assets and embedded into the Go binary from `internal/api/dist`.
The operator interface is served by the single static binary, with no separate
asset server or frontend process.

```sh
make ui     # npm ci + vite build into internal/api/dist
make build  # frontend, then the Go binary
```

`internal/api/dist` is generated, not version controlled. A `go build` without
the asset step still produces a working controller; the root route then explains
that the frontend was not compiled in. Container images always build it first.

With `http.auth.mode: bearer` the interface asks for the operator token on the
first 401 and keeps it in this browser only, in session storage by default. The
token is attached to same-origin operator API calls as an `Authorization` header,
and mirrored into a `SameSite=Strict` cookie scoped to `/api/v1/objects` because
`<img>` cannot send a header. With `mode: disabled` no token is ever requested,
stored, or sent.

Frontend checks:

```sh
npm --prefix web run typecheck
npm --prefix web test
```

## Agent tools

- `studio_budget()` returns current usage, reservations, remaining allowances,
  lease end, and the effective operator-controlled reasoning setting.
- `studio_sql({query})` accepts a read-only PostgreSQL `SELECT` or `WITH` query
  and returns at most 500 rows.
- `studio_journal({entry})` writes the agent's concise account of the current
  wake to the shared, human-readable history.
- `studio_exec({command, timeout_ms})` runs arbitrary shell in the configured
  disposable executor, never in the controller.
- `studio_publish({path})` publishes one model-driven scene from the sandbox.
- `studio_watch({path, fps})` samples a locally rendered framebuffer without an
  inference call per frame.
- `studio_schedule(...)` creates one-shot or recurring model wakes inside the
  current lease.

There is intentionally no tool for changing reasoning effort, budgets, leases,
blackout, or display rate.

## Agent runtimes

`runtime.driver: hermes` starts a fresh Hermes one-shot for each wake and keeps
each persona's Hermes home and memory isolated under the configured workspace
root. The runtime enables only the lease-scoped `studio` MCP server. Native
Hermes terminal and web tools are not exposed, so arbitrary code and Docker
workloads still cross the configured disposable-executor boundary. Automatic
session-title inference is disabled; every auxiliary inference that does run is
included in the persisted usage report.

`runtime.driver: opencode` remains available for deployments that prefer the
OpenCode executor. Persona identity and model routing are independent of the
selected runtime in both cases.

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

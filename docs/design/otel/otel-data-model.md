# OpenTelemetry Data Model

Last reviewed: 2026-08-11

Freshness source: `internal/telemetry/`, `internal/storage/dolt/store.go`,
`internal/compact/haiku.go`, `cmd/bd/find_duplicates.go`, `internal/hooks/`,
and `internal/tracker/engine.go`.

This reference documents the OpenTelemetry spans and metrics emitted by the
current `bd` process. Telemetry is opt-in and becomes active when
`BD_OTEL_METRICS_URL` is set or `BD_OTEL_STDOUT=true`.

## Export and Resource Model

`telemetry.Init` creates a resource with:

- `service.name=bd`
- `service.version=<bd version>`
- standard host attributes from `resource.WithHost()`
- standard process attributes from `resource.WithProcess()`

Metrics are exported to the OTLP HTTP endpoint in `BD_OTEL_METRICS_URL` and/or
the stdout exporter. Spans are exported only by the stdout exporter when
`BD_OTEL_STDOUT=true`; there is no remote trace exporter in the current default
stack. `BD_OTEL_LOGS_URL` is reserved for future log export.

When telemetry is disabled, no-op providers are installed and the storage
wrapper returns the original store.

## Naming Conventions

- Spans use dotted operation names such as `bd.command.create`,
  `storage.CreateIssue`, `dolt.query`, and `tracker.sync`.
- Metrics use the `bd.` prefix.
- Storage method names retain Go casing in the span name and in
  `db.operation`.
- Attribute names use their subsystem prefix: `bd.*`, `db.*`, `dolt.*`,
  `hook.*`, or `sync.*`.

Not every operation emits both a span and a dedicated metric. The sections
below distinguish the two signal types.

## CLI Command Spans

The normal root command path starts one span named `bd.command.<name>` before
database access, so downstream SQL, storage, tracker, and AI spans inherit it
through the command context. The internal metrics-flusher subcommand exits the
pre-run path before this span is started.

| Attribute | Meaning |
|---|---|
| `bd.command` | Cobra command name |
| `bd.version` | CLI version |
| `bd.args` | Command-line arguments joined with spaces |

These spans do not claim host, OS, or working-directory command attributes;
host and process identity belongs to the resource.

## Storage Wrapper

When telemetry is enabled, `telemetry.WrapStorage` instruments the operational
methods of the `storage.Storage` interface. A method `CreateIssue` produces a
client span named `storage.CreateIssue` with `db.operation=CreateIssue`; the
same pattern applies to CRUD, dependency, label, query, statistics,
configuration, transaction, iterator, count, merge-slot, and slot methods.
Lifecycle `Close` delegates directly to the wrapped store without a span.

Method-specific attributes include, when applicable:

- `bd.issue.id`, `bd.issue.type`, `bd.issue.count`
- `bd.actor`, `bd.update.count`
- `bd.query`, `bd.result.count`, `bd.result.external_blocked_count`
- `bd.dep.from`, `bd.dep.to`, `bd.dep.type`
- `bd.label`, `bd.config.key`, `bd.local_metadata.key`
- `bd.since`, `bd.max_depth`, `db.commit_msg`, `slot.key`

Errors are recorded on the span and set its status to error.

### Storage metrics

| Metric | Type | Unit | Meaning |
|---|---|---|---|
| `bd.storage.operations` | Counter | operations | Storage calls; includes `db.operation` plus method attributes |
| `bd.storage.operation.duration` | Histogram | ms | Call duration; currently records method-specific attributes only |
| `bd.storage.errors` | Counter | errors | Failed calls; currently records method-specific attributes only |
| `bd.issue.count` | Gauge | issues | Status snapshot recorded by `GetStatistics` |

The duration and error instruments do not currently add `db.operation`, so do
not build dashboards that assume that dimension exists on those series.

## Dolt SQL Spans

The Dolt store emits client spans for its SQL helpers:

| Span | `db.operation` | Additional attributes |
|---|---|---|
| `dolt.exec` | `exec` | truncated `db.statement` |
| `dolt.query` | `query` | truncated `db.statement` |
| `dolt.query_row` | `query_row` | truncated `db.statement` |

All three also receive the cached Dolt attributes:

- `db.system=dolt`
- `db.readonly=<store mode>`
- `db.server_mode=true` (the current store implementation is server-backed)

SQL statements are truncated to 300 characters before being attached.

## Dolt Version-Control Spans

| Span | Attributes |
|---|---|
| `dolt.commit` | common Dolt attributes |
| `dolt.push` | `dolt.remote`, `dolt.branch` |
| `dolt.force_push` | `dolt.remote`, `dolt.branch` |
| `dolt.pull` | `dolt.remote`, `dolt.branch` |
| `dolt.branch` | `dolt.branch` |
| `dolt.checkout` | `dolt.branch` |
| `dolt.merge` | `dolt.merge_branch`; `dolt.conflicts` when conflicts exist |

The commit message is not attached as a span attribute. Each operation records
an error and error status when its returned error is non-nil.

## Dolt Runtime Metrics

| Metric | Type | Unit | Meaning |
|---|---|---|---|
| `bd.db.retry_count` | Counter | retries | SQL retries for transient server errors |
| `bd.db.lock_wait_ms` | Histogram | ms | Database-lock wait time |
| `bd.db.circuit_trips` | Counter | trips | Circuit breaker transitions to open |
| `bd.db.circuit_rejected` | Counter | requests | Requests rejected while the circuit is open |
| `bd.db.serialization_errors` | Counter | errors | MySQL 1213/1205 failures observed before retry |
| `bd.write_retries_total` | Counter | retries | Write transaction retries; `type` is `serialization` or `connection` |
| `bd.db.conn_acquire_ms` | Histogram | ms | Time to acquire a pooled transaction connection |
| `bd.db.pool_wait_count` | Counter | waits | Connection acquisitions that waited for the pool |
| `bd.db.pool_wait_ms` | Histogram | ms | Time spent waiting because the pool was exhausted |
| `bd.db.pool_open` | Observable gauge | connections | Open pooled connections |
| `bd.db.pool_in_use` | Observable gauge | connections | Connections currently in use |
| `bd.db.pool_idle` | Observable gauge | connections | Idle pooled connections |
| `bd.db.pool_max_open` | Observable gauge | connections | Configured maximum open connections |

## Tracker Sync Spans

The shared tracker engine emits:

- `tracker.sync` with `sync.tracker`, effective `sync.pull`, effective
  `sync.push`, and `sync.dry_run`; completion attributes summarize pulled,
  pushed, conflict, create, update, skip, and error counts.
- `tracker.detect_conflicts` with `sync.tracker` and the resulting
  `sync.conflicts` count.
- `tracker.pull` with `sync.tracker`, `sync.dry_run`, and create/update/skip
  result counts.
- `tracker.push` with `sync.tracker`, `sync.dry_run`, and
  create/update/skip/error result counts.

## Hook Spans and Events

Hook execution creates a root span named `hook.exec`, because hooks are
fire-and-forget and do not inherit the originating command context.

| Attribute | Meaning |
|---|---|
| `hook.event` | `create`, `update`, or `close` |
| `hook.path` | Executed hook path |
| `bd.issue_id` | Related issue ID |

Captured stdout and stderr are added as `hook.stdout` and `hook.stderr` span
events with truncated `output` and `bytes` attributes. Start failures,
non-zero exits, and timeouts are recorded as span errors.

## AI Spans and Metrics

Both compaction and AI duplicate detection use the span name
`anthropic.messages.new`.

| Operation | Span attributes |
|---|---|
| Compaction | `bd.ai.model`, `bd.ai.operation=compact`, input/output tokens, attempts |
| Duplicate detection | `bd.ai.model`, `bd.ai.operation=find_duplicates`, batch size, input/output tokens, duration in ms |

The compaction client resolves credentials in this order:
`ANTHROPIC_API_KEY`, `ai.api_key`, then an explicitly supplied key. It permits
three retries after the initial request (four attempts total) with exponential
backoff. Duplicate detection falls back to mechanical scores when the AI call
fails.

Compaction records these metrics with `bd.ai.model`:

| Metric | Type | Unit |
|---|---|---|
| `bd.ai.input_tokens` | Counter | tokens |
| `bd.ai.output_tokens` | Counter | tokens |
| `bd.ai.request.duration` | Histogram | ms |

AI duplicate detection currently records token counts and duration only as
span attributes, not through those metric instruments.

## Source Review Checklist

When changing telemetry behavior, review the canonical source closest to the
signal:

- providers and storage wrapper: `internal/telemetry/`
- Dolt SQL, version-control spans, and pool/retry metrics:
  `internal/storage/dolt/store.go`
- tracker synchronization: `internal/tracker/engine.go`
- hook execution and output events: `internal/hooks/`
- compaction AI telemetry: `internal/compact/haiku.go`
- duplicate-detection AI telemetry: `cmd/bd/find_duplicates.go`

This document was re-reviewed against those sources on 2026-08-11.

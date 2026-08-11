# Error Handling Guidelines

Last reviewed: 2026-08-11

Freshness source: `cmd/bd/*.go`, `cmd/bd/errors.go`, and `cmd/bd/main.go`.

This document describes the current CLI error boundary and the conventions for
new command code. The central rule is simple: normal Cobra command paths return
errors; the process boundary owns the final exit.

## Normal Command Errors

Commands implemented with `RunE` should return an error to their caller. The
helpers in `cmd/bd/errors.go` format a user-facing message and return an
`*exitError` with exit code 1:

- `HandleError(format, args...)` writes plain text to stderr.
- `HandleErrorWithHint(message, hint)` writes an error and actionable hint.
- `HandleErrorRespectJSON(format, args...)` emits JSON to stdout when `--json`
  is active; otherwise it behaves like `HandleError`.
- `HandleErrorWithHintRespectJSON(message, hint)` applies the same stdout JSON
  contract while retaining the hint.
- `SilentExit()` returns exit code 1 without printing another message. Use it
  only after the command has already rendered the complete failure result.

Example:

```go
RunE: func(cmd *cobra.Command, args []string) error {
    if len(args) == 0 {
        return HandleErrorWithHintRespectJSON(
            "issue ID is required",
            "run 'bd list' to find an issue ID",
        )
    }

    issue, err := store.GetIssue(cmd.Context(), args[0])
    if err != nil {
        return HandleErrorRespectJSON("loading issue %s: %v", args[0], err)
    }
    // ...
    return nil
}
```

Returning the error preserves Cobra control flow and lets the root execution
path in `cmd/bd/main.go` select the process exit code after deferred command
cleanup and telemetry have run.

## JSON Stream Selection

Choose the helper according to the command's established output contract:

| Helper family | Plain-text destination | `--json` destination |
|---|---|---|
| `HandleError*` | stderr | stderr |
| `HandleError*RespectJSON` | stderr | stdout |
| `FatalError*` | stderr | stderr |
| `FatalError*RespectJSON` | stderr | stdout |

JSON errors contain `schema_version` and an `error` field, plus `hint` when the
hint helper is used. Envelope mode places that payload under `data`; see
[JSON_SCHEMA.md](JSON_SCHEMA.md).

Do not switch an existing command between stdout and stderr casually. Scripts
may depend on its established stream contract.

## Warnings and Best-Effort Work

Use `WarnError` or an explicit stderr warning only when the primary operation
can still be considered successful. Typical examples are optional cleanup,
advisory metadata, or a background convenience step whose failure is already
reported.

```go
if err := refreshOptionalCache(); err != nil {
    WarnError("refreshing cache: %v", err)
}
```

A warning is not appropriate when the requested state change failed, a
transaction did not commit, output is incomplete, or continuing could hide
data loss. Return an error in those cases.

## Cleanup Errors

Deferred cleanup often cannot change the command result. Ignoring an error is
acceptable only when all of these are true:

1. The cleanup is best-effort and does not affect correctness or durability.
2. The primary operation has already established its result.
3. There is no useful recovery action for the caller.

Make the intent visible at the call site:

```go
defer func() {
    _ = rows.Close() // best-effort cleanup; query result is already decided
}()
```

Never discard transaction commit/rollback failures, database writes, export
serialization failures, or errors that determine whether the requested result
is complete.

## Exceptional Immediate-Exit Paths

The `FatalError*` helpers call `os.Exit(1)`. They are retained only for
proxied-server handlers that run outside the normal `RunE` error-return path.
That mode is currently not enterable because `bd init --proxied-server` is
rejected as not implemented. New and converted `RunE` code must use the
returning helpers instead.

Immediate exit bypasses deferred per-command cleanup and telemetry. When the
proxied-server path becomes active, convert its fatal helpers to returned
errors before relying on it in production.

`CheckReadonly(operation)` is another deliberate process boundary. When
read-only mode blocks a mutation, it prints the violation, flushes queued
metrics, and exits. Call it before performing any write. It is not a general
validation helper.

## Decision Guide

| Situation | Required handling |
|---|---|
| Invalid input or missing required state in `RunE` | Return `HandleError*` |
| Existing JSON command whose errors belong on stdout | Return a `RespectJSON` helper |
| Failure after the command already printed a complete diagnostic/result | Return `SilentExit()` |
| Requested database or filesystem mutation failed | Return an error; do not warn and continue |
| Optional advisory/cleanup operation failed | Warn or explicitly ignore with a reason |
| Mutation attempted in read-only mode | Call `CheckReadonly` before the write |
| Proxied-server-only handler outside `RunE` | Existing `FatalError*` path only; do not expand it |

## Review Checklist

- Does normal command code return instead of calling `os.Exit`?
- Does the helper preserve the command's JSON stdout/stderr contract?
- Can every warning truly leave the requested operation successful?
- Are transaction, persistence, and serialization errors propagated?
- Is every ignored error limited to non-critical cleanup with clear intent?
- Does a read-only guard run before the first mutation?
- Do tests assert the exit code and the correct output stream where relevant?

## Testing

Prefer command-level tests that invoke the Cobra path and capture stdout,
stderr, and the returned exit code. For JSON commands, test both legacy and
envelope payloads where the shared JSON helper is used. Subprocess tests are
appropriate only for the intentional `os.Exit` boundaries such as
`CheckReadonly`.

Relevant regression coverage includes `cmd/bd/errors_test.go`,
`cmd/bd/main_errors_test.go`, `cmd/bd/readonly_test.go`, and the JSON contract
tests under `cmd/bd/protocol/`.

## References

- `cmd/bd/errors.go` — formatting helpers and exit boundaries
- `cmd/bd/main.go` — root execution, exit-code handling, and final cleanup
- `cmd/bd/init.go` — proxied-server rejection that keeps fatal paths latent
- `cmd/bd/*_test.go` — command and output-stream regression tests

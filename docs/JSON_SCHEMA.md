# JSON Output Schema Contract

Last reviewed: 2026-08-10

Freshness source: `cmd/bd/output.go`, `cmd/bd/errors.go`, and
`cmd/bd/protocol/json_contract_test.go`.

All `bd` commands that support `--json` output can wrap their response in a
uniform envelope by setting `BD_JSON_ENVELOPE=1`. This will become the default
format in v2.0.

## Migration Guide

### Opt in to the envelope format

```bash
export BD_JSON_ENVELOPE=1
```

### Envelope format (BD_JSON_ENVELOPE=1, default in v2.0)

Every `--json` command wraps output as:

```json
{"schema_version": 2, "data": <legacy-payload>}
```

The legacy payload is untouched inside `.data`. The command-specific payload
rules below therefore apply to the legacy top level and to envelope mode's
`.data` in exactly the same way.

### Updating consumers

```bash
# Legacy mode:
bd list --json | jq '.[0].id'
bd show beads-abc --json | jq '.title'

# Envelope mode:
bd list --json | jq '.data[0].id'
bd show beads-abc --json | jq '.data.title'

# Version check:
bd show beads-abc --json | jq '.schema_version'
```

### Timeline

- **Current release**: Legacy format is default. Set `BD_JSON_ENVELOPE=1` to opt in.
  A deprecation notice is printed to stderr when `--json` is used without the env var.
- **v2.0**: Envelope becomes the default. `BD_JSON_ENVELOPE=0` is available as
  a temporary escape hatch for one release cycle.

## Schema Version

Current version: **2**

The `schema_version` field is an integer that increments when:

- Fields are added, renamed, or removed
- Output structure changes, including object/array shape
- Field types change

Additive optional fields do not bump the version. Schema version 2 records the
arity-based object/array contract introduced in `1.2.0-fork.1`.

## Output Formats

### Envelope mode (BD_JSON_ENVELOPE=1)

Object and array payloads use the same outer envelope:

```json
{
  "schema_version": 2,
  "data": {
    "id": "beads-abc",
    "title": "Example issue",
    "status": "open"
  }
}
```

```json
{
  "schema_version": 2,
  "data": [
    {"id": "beads-abc", "title": "First"},
    {"id": "beads-def", "title": "Second"}
  ]
}
```

### Legacy mode (default, until v2.0)

Successful `show`, `update`, `close`, and `reopen` output is determined only by
the number of issue IDs requested, never by result count, storage state, or the
embedded/proxied route:

| Request form | JSON payload |
|---|---|
| Exactly one explicit ID | Bare object |
| Two or more explicit IDs | Array, even if only one operation succeeds |
| No-ID `update` or `close` using last-touched | Bare object |
| `show --current` or `show --as-of` | Bare object |
| Query commands such as `list`, `ready`, `children`, `show --children`, and `dep list` | Always an array |

`create` continues to return one bare object. `show --thread` and `show --refs`
return their existing command-specific structures and are outside the issue
record arity contract.

Bare objects include `schema_version` alongside their data:

```json
{
  "schema_version": 2,
  "id": "beads-abc",
  "title": "Example issue",
  "status": "open",
  "priority": 1,
  "issue_type": "task",
  "created_at": "2026-04-20T12:00:00Z"
}
```

Raw arrays do not receive a top-level schema field:

```json
[
  {"id": "beads-abc", "title": "First"},
  {"id": "beads-def", "title": "Second"}
]
```

### Close auxiliary-result envelope

When `close` uses `--suggest-next`, `--continue`, or `--claim-next`, it always
returns one keyed payload. Requested result keys remain present even when no
result exists; `closed` and `unblocked` are arrays, while `continue` and
`claimed` are an object or `null`. Multiple flags combine in the same payload.

```json
{
  "closed": [{"id": "beads-abc", "status": "closed"}],
  "unblocked": [],
  "claimed": null
}
```

### Error output (stderr)

Errors with `--json` active emit JSON to stderr:

```json
{
  "schema_version": 2,
  "error": "issue not found: beads-xyz",
  "code": "not_found"
}
```

## Field Contracts by Command

### `bd list --json`

Required fields per item:

- `id` (string): Issue ID, for example `beads-abc`
- `title` (string): Issue title
- `status` (string): `open`, `in_progress`, `closed`, or `deferred`
- `priority` (number): 0-4
- `issue_type` (string): `bug`, `feature`, `task`, `epic`, or `chore`
- `created_at` (string): RFC3339 timestamp

Optional fields include `description`, `owner`, `updated_at`, `closed_at`,
`labels`, `dependencies`, count fields, and `parent`.

### `bd ready --json`

Uses the `bd list --json` item schema and returns only unblocked issues.

### `bd blocked --json`

Returns standard issue records plus `blocked_by_count` and `blocked_by`.

### `bd show --json`

One requested issue returns an object; multiple requested issues return an
array. Full records include description, acceptance criteria, dependencies,
and comments as loaded by the corresponding include flags.

`--fields=id,status,metadata` projects only the named `IssueDetails` JSON
fields and preserves the requested key order. Unknown fields are errors. A
valid field that was not loaded is still present with its zero or `null` value.

### `bd dep list --json`

The container is always an array. The default `--format=issues` returns issue
records for either direction and any request arity. Use `--format=edges` for
explicit dependency records with exactly `issue_id`, `depends_on_id`, and
`type`.

### `bd update` metadata flags

`--set-metadata key=value` always stores `value` as a JSON string, including
values such as `true`, `null`, `0123`, or a large integer. Use the repeatable
`--set-metadata-json key=<raw JSON>` flag when a JSON number, boolean, null,
array, or object is intentional. Supplying the same key through both flags is
an error.

### `bd edit`

Interactive `bd edit` requires both stdin and stdout to be terminals. In
headless workflows, use `bd update <id> --body-file <path>` instead.

### `bd import --json`

Returns one summary object with `source`, `created`, `skipped`,
`dedup_skipped`, `memories`, `ids`, and `dry_run`.

### `bd export --json`

Outputs JSONL, one self-contained issue or memory record per line, rather than
one array or envelope. Each line includes `schema_version`.

## Consumer Guidelines

1. Check `schema_version` on object output. Warn on a newer version, then
   attempt parsing so additive changes remain usable.
2. Select object versus array for issue mutation commands from request arity,
   not from result count. Query commands always return arrays.
3. Ignore unknown fields.
4. Use `--json`, not `--format json`. The `dep list --format` flag selects its
   JSON record type; it does not enable JSON output.

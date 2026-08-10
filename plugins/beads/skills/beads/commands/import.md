---
description: Import and upsert issues from JSONL
argument-hint: [file|-]
---

`bd import [file|-]` imports newline-delimited issue JSON. Existing IDs are
updated when the incoming `updated_at` is newer; use `--allow-stale` only for an
intentional older-snapshot restore.

`spec_id` is a top-level issue field. Create or update it with `--spec-id`; do
not store it under metadata. `bd show`, `bd list`, `bd export`, and `bd import`
preserve the native field across JSON round-trips.

```bash
bd import issues.jsonl
bd export -o issues.jsonl
bd update bd-123 --spec-id docs/specs/feature.md --json
```

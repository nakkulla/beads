---
id: show
title: bd show
slug: /cli-reference/show
sidebar_position: 40
---

<!-- AUTO-GENERATED: do not edit manually -->
Generated from `bd help --doc show`

## bd show

Show issue details.

With --json, one requested issue returns an object and multiple requested
issues return an array. --current returns one object; --as-of follows the same
one-ID object/multi-ID array rule. --children is a query and always returns an
array. Use --fields to project IssueDetails JSON fields in the requested order.

```
bd show [id...] [--id=<id>...] [--current] [flags]
```

**Aliases:** view

**Flags:**

```
      --as-of string         Show issue as it existed at a specific commit hash or branch (requires Dolt)
      --children             Show only the children of this issue
      --current              Show the currently active issue (in-progress, hooked, or last touched)
      --fields string        Select JSON fields in requested order (comma-separated)
      --id stringArray       Issue ID (use for IDs that look like flags, e.g., --id=gt--xyz)
      --include-comments     Stream full comment bodies in JSON output (--json only; may be slow on issues with many comments)
      --include-dependents   Stream full dependent issues in JSON output (--json only; may be slow on hub beads)
      --links                Include current branch, PR URL, and worktree links (with --as-of, links are current)
      --local-time           Show timestamps in local time instead of UTC
      --long                 Show all available fields (extended metadata, agent identity, gate fields, etc.)
      --refs                 Show issues that reference this issue (reverse lookup)
      --short                Show compact one-line output per issue
      --thread               Show full conversation thread (for messages)
  -w, --watch                Watch for changes and auto-refresh display
```

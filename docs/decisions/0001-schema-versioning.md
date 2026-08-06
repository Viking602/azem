# ADR 0001: SQLite Schema Versioning

Status: Accepted
Date: 2026-08-06

## Context

Azem stores sessions, governed execution, recovery state, and desktop project
ownership in one local SQLite database. Schema 20 also stores semantic context
snapshots, append-only semantic events, and context manifests. Runtime migrations and SQLC require
separate schema representations. Older binaries cannot safely interpret state
written by newer schemas.

## Decision

- Append migrations and keep `schemaVersion == len(migrations)`.
- Keep `migrations.go` and `dbgen/schema.sql` synchronized and regenerate SQLC
  output when affected.
- Back up an existing older database before applying an upgrade.
- Reject a database whose `user_version` is newer than the binary supports.
- Test previous-version upgrade, retained data, current-version reopen, and
  future-version rejection.
- Never implement rollback by lowering `user_version`, deleting tables, or
  rebuilding the user's database.
- A migration may invalidate explicitly replaceable derived state, such as
  pre-release Provider `ModelHistory`, only when authoritative transcript,
  Todo, tool, Artifact, Memory, and recovery records remain intact and the
  invalidation is covered by an upgrade test.

## Consequences

Upgrades are forward-only. Restoring a pre-upgrade backup requires a binary
that supports the backup schema. Every persistence feature carries migration,
compile-time schema, documentation, and regression-test work.

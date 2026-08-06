# ADR 0001: SQLite Schema Versioning

Status: Accepted
Date: 2026-08-06

## Context

Azem stores sessions, governed execution, recovery state, and desktop project
ownership in one local SQLite database. Runtime migrations and SQLC require
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

## Consequences

Upgrades are forward-only. Restoring a pre-upgrade backup requires a binary
that supports the backup schema. Every persistence feature carries migration,
compile-time schema, documentation, and regression-test work.

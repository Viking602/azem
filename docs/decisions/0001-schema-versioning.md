# ADR 0001: SQLite Schema Versioning

Status: Accepted
Date: 2026-08-06

## Context

Azem stores sessions, governed execution, recovery state, and desktop project
ownership in one local SQLite database. Schema 20 stores retained semantic
context catalogs and manifests. Schema 21 keeps catalog/control-plane rows in
SQLite and stores large opaque payloads as content-addressed files. Schema 27
uses the same BlobStore boundary for Venat v0.16 execution
continuations/results and immutable application bindings. Schema 28 adds
reversible project-catalog visibility without deleting session ownership.
Runtime migrations
and SQLC require separate schema representations. Older binaries cannot safely
interpret state written by newer schemas.

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
- A breaking execution-runtime upgrade must introduce a versioned binding and
  migration. Preserve terminal legacy history. Mark non-terminal legacy rows
  without enough identity for exact replay `reconcile_required`; never
  manufacture a new checkpoint or lower the schema to run old code.

## Consequences

Upgrades are forward-only. Restoring a pre-upgrade backup requires a binary
that supports the backup schema. Every persistence feature carries migration,
compile-time schema, documentation, and regression-test work.

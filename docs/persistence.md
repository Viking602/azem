# Persistence and Recovery

Last verified: 2026-08-06

Azem stores configuration and durable runtime state locally. SQLite is the
authoritative store for sessions, projections, governed agent execution, usage,
memory, desktop projects, semantic context, and recovery metadata. Current schema version: **20**.

## Paths and permissions

`internal/config/paths.go` resolves these locations:

| Data | Path rule |
|---|---|
| Configuration | `$XDG_CONFIG_HOME/azem/config.yaml`, or `~/.config/azem/config.yaml` |
| SQLite database | Next to configuration as `azem.db` |
| Attachments and other data | `$XDG_DATA_HOME/azem`, or the platform data directory |
| Runtime state and logs | `$XDG_STATE_HOME/azem`, or the platform user-cache directory |

Azem creates its directories with mode `0700`, and protects the database and
upgrade backup with mode `0600`. SQLite and file credential stores rely on
filesystem permissions; use the system keyring when stronger credential
protection is required.

## Schema definitions

The schema intentionally has two synchronized representations:

- `internal/store/sqlite/migrations.go` is the runtime upgrade history.
- `internal/store/sqlite/dbgen/schema.sql` is SQLC's compile-time schema.

`schemaVersion` must equal `len(migrations)`. A schema change is incomplete
until both files, generated SQLC code when affected, and migration tests agree.
Never rewrite an existing migration after release; append the next migration.

Generate SQLC code with:

```bash
make sqlc
```

Review generated changes before committing. Generation does not replace the
upgrade and reopen tests.

## Open and upgrade sequence

`internal/store/sqlite/provider.go` performs the following sequence:

1. Create the database directory and open SQLite.
2. Enable foreign keys and a five-second busy timeout.
3. Acquire the platform-specific database upgrade lock.
4. Enable WAL mode and read `PRAGMA user_version`.
5. Reject a version greater than the compiled `schemaVersion` without writing.
6. Before upgrading an existing older database, create `azem.db.bak` with
   SQLite `VACUUM INTO` and replace the previous backup atomically.
7. Apply every missing migration transactionally and update `user_version`.
8. Reopen normal operation only after migration succeeds.

An existing current-version database is not backed up on every launch. The
automatic `.bak` file is an upgrade checkpoint, not a continuous backup plan.

## Compatibility policy

- Older schemas upgrade forward automatically after a consistent backup.
- The current schema can be reopened repeatedly without mutation.
- Future schemas are rejected because older code cannot safely interpret them.
- Downgrading the application does not downgrade the database.
- Never lower `PRAGMA user_version`, remove tables, or delete a user database to
  make an old binary start.

Schema 18 adds the durable control-plane stores and their indexes:

- `agent_definition_snapshots`
- `admission_reservations`
- `resource_claims`

These tables satisfy current Venat definition, admission, and resource-claim
contracts and must remain aligned with the declared `go.mod` version.

Schema 19 adds desktop project ownership:

- `desktop_projects` is the durable, ordered catalog of opened project roots.
- `session_workspaces` assigns each session to exactly one project.
- `workspace_session_state` remains the last-session pointer for a workspace;
  it is not the project catalog and does not replace per-session ownership.

Desktop project roots are application state and are never written into the
single `workspace.root` configuration field. On launch, the GUI restores the
most recently opened valid project. A session cannot be silently moved to a
different project. Pre-schema-19 sessions are adopted automatically only when
there is exactly one valid project; otherwise they remain unassigned until
opened from the correct workspace.

Schema 20 replaces the pre-release compaction checkpoint format:

- `session_semantic_state` stores the current host-validated semantic snapshot and writer cursor.
- `session_semantic_state_events` stores append-only patches with revision CAS and idempotency keys.
- `context_manifests` records the exact ordered context segments, exclusions, policy version, and active manifest hash.

The migration deliberately invalidates replaceable `model_history` and prompt-cache identity so no legacy summary can enter the new kernel. It preserves canonical blocks, Todo, tool records, artifacts, Memory, Recap, project ownership, and every other authoritative store.

## Stored data groups

| Group | Examples | Owner |
|---|---|---|
| Conversation | sessions, canonical blocks, projections, provider requests, attachments | `internal/session` |
| Tool timeline | tool records, file evidence, action attempts, tool-call charges | `internal/session`, `internal/app`, Venat adapters |
| Agent execution | runs, tasks, leases, approvals, resume tokens, team state | Venat through `internal/store/sqlite` |
| Control plane | definition snapshots, admission reservations, resource claims | Venat through `stores_control_plane.go` |
| Context | semantic state/events, manifests, memories, recaps, history FTS, context artifacts | `internal/app`, `internal/memory`, `internal/recap`, `internal/session` |
| Product state | authentication metadata, model catalog cache, usage | corresponding internal services |
| Desktop navigation | project catalog, session-to-project ownership, last workspace session | `internal/session`, `internal/app`, `internal/desktop` |

Generated `dbgen` code is an implementation detail of the SQLite adapters;
application and UI packages should depend on focused services instead of raw
queries.

## Crash recovery

At exclusive startup, `PrepareRecovery` treats active leases as belonging to
the prior process and expires them immediately. It quarantines incomplete
action attempts and provider requests so the runtime does not replay unknown
side effects blindly.

Recovery then:

1. Interrupts incomplete subagent projections.
2. Loads non-terminal runs and pending approval tokens.
3. Restores durable projections.
4. Resumes Team runs and eligible Single-Agent runs.
5. Leaves `reconcile_required` runs and unknown action attempts paused for an
   explicit reconciliation decision.
6. Uses succeeded action attempts as an anti-replay ledger when a resumed model
   emits a fresh call ID for an already completed non-idempotent input.

The UI must display recovered and reconciliation-required state; it must not
hide it by converting the run to success or starting a replacement session.

## Operator recovery procedure

If startup reports a schema, lock, or corruption error:

1. Stop every Azem process using the database.
2. Preserve `azem.db`, any `-wal`/`-shm` companions, and `azem.db.bak` before
   attempting recovery.
3. Record the application version and reported schema version.
4. For a future-schema error, install the matching or newer Azem binary; do not
   edit the schema version.
5. For a failed upgrade, retain the failed database as evidence and restore the
   pre-upgrade `.bak` only with a binary that supports that backup's schema.
6. For suspected corruption, work on a copy and use SQLite integrity/recovery
   tooling; never experiment on the only user database.

The project does not currently provide a one-command rollback or corruption
repair workflow. `docs/release.md` and `docs/troubleshooting.md` remain required
before declaring those operations fully supported.

## Verification

Run the persistence suite:

```bash
go test ./internal/store/sqlite
```

Required coverage includes previous-schema upgrade, schema 18 control-plane
tables and indexes, schema 19 project ownership backfill, schema 20 compaction-state
invalidation with canonical data retention, current-version reopen, automatic backup, and rejection of a future schema. Run
`GOWORK=off go test ./...` before release.

# Persistence and Recovery

Last verified: 2026-08-17

Azem stores configuration and durable runtime state locally. SQLite is the
authoritative catalog, search index, and Venat control plane. Large opaque
payloads live in a content-addressed file store so one `azem.db` does not grow
without bound. Current schema version: **21**.

## Paths and permissions

`internal/config/paths.go` resolves these locations:

| Data | Path rule |
|---|---|
| Home | `~/.azem`, or `$AZEM_HOME` |
| Configuration | `$AZEM_HOME/config.yaml` |
| SQLite database | `$AZEM_HOME/azem.db` |
| Content-addressed blobs | `$AZEM_HOME/blobs/<aa>/<sha256>` |
| Attachments, plugins, worktrees | `$AZEM_HOME` |
| Runtime state and logs | `$AZEM_HOME` |

The first launch that uses the default `~/.azem` home moves a previous
`~/.config/azem` tree, the platform data directory (`~/Library/Application Support/azem` on macOS, `%AppData%\azem` on Windows, `~/.local/share/azem` on Linux), and the platform cache directory into that home. It refuses to start if the old database is still locked. `AZEM_HOME` disables that migration. Project-local `{workspace}/.azem` is unchanged.

Azem creates its directories with mode `0700`, and protects the database,
upgrade backup, and blob files with mode `0600` where the platform supports
POSIX permission bits. SQLite and file credential stores rely on filesystem
permissions; use Keychain, Windows Credential Manager, or the platform keyring
when stronger credential protection is required.

## What stays in SQLite versus files

This split follows the same boundary as oh-my-pi: the database keeps identity,
indexes, and transactional control state; bytes that only need to be fetched
by hash live on disk. Azem does not store sessions as JSONL because compaction,
FTS, project ownership, and Venat leases must commit together.

| Stays in SQLite | Lives in `~/.azem/blobs` |
|---|---|
| Session catalog, UI flags, project ownership | Context artifact payloads (always) |
| Event/record catalog rows (`run_id`, sequence, SHA-256) | Venat `events.data` larger than 4 KiB |
| Leases, reservations, claims metadata | Venat `records.data` larger than 4 KiB |
| `history_fts` / `memories_fts` | Tool content and structured results larger than 4 KiB |
| Todos, recaps, usage, provider requests, auth, model catalog | Subagent output and transcript larger than 4 KiB |
| Context manifests and retained schema-20 semantic catalog rows | Provider `model_history` larger than 4 KiB |
| User and completed assistant block JSON (also indexed by FTS) | Thinking, agent, and other non-FTS transcript blocks larger than 4 KiB |

Catalog rows keep the SHA-256 digest. Identical bytes collapse to one file
under `blobs/<first two hex chars>/<64-hex digest>`. Reads verify the bytes
against that digest; context artifacts also enforce a 64 MiB read limit before
allocation. Forked sessions reuse blob files for ordinary copied artifacts but
receive new session-scoped catalog IDs. Derived `ModelHistory`, context
manifests, provider cache, context archives, and retained legacy semantic rows
are reset; the target session rebuilds from its copied canonical transcript.
After schema 21 extracts legacy inline payloads it `VACUUM`s the database so
the old pages are not left behind.

Blob installation uses an atomic no-replace operation to identify the one
writer that created a digest path. Runtime catalog writers hold SQLite's
writer lock before installing bytes. After a failed, conflicted, or ignored
write, a `BEGIN IMMEDIATE` reference check deletes only creator-owned files
that no catalog row adopted. The writer lock prevents another process from
committing a reference between the check and deletion.

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

Schema 20 introduced semantic-state tables and `context_manifests`. The
deterministic archive kernel now uses `context_manifests`; the
`session_semantic_state` and `session_semantic_state_events` tables remain so
existing databases can reopen without a destructive migration. Production
runtime code does not read or write those legacy semantic rows; the offline
trajectory exporter retains them to keep historical database exports lossless.

The schema-20 migration deliberately invalidates replaceable `model_history`
and prompt-cache identity so no legacy summary can enter the current kernel.
It preserves canonical blocks, Todo, tool records, artifacts, Memory, Recap,
project ownership, and every other authoritative store.

Schema 21 moves large payloads out of SQLite:

- `context_artifacts.payload` is extracted into the blob store and the column is dropped.
- The leftover `session_projections.blocks` JSON column is dropped. The transcript is `session_blocks` only.
- Tool content, tool structured payloads, subagent output/transcript, large thinking/agent blocks, large `model_history`, and large Venat `events`/`records` become SHA-256 references. A real 2.5 GiB developer database was 2.27 GiB Venat `events` plus ~170 MiB of artifacts, subagent transcripts, and tool rows.
- User and completed assistant blocks stay inline so `history_fts` triggers keep indexing the same JSON.

Deterministic compaction reuses these schemas. The exact omitted history is a
`context_archive` context artifact: its catalog row and SHA-256 remain in
SQLite while the canonical JSON payload lives in the schema-21 blob store.
`ArchiveContextManifestV1` policy version 3 records that artifact ID/digest plus
carrier/frame statistics, and `ModelHistory` wire version 3 points at the active
manifest. Bitmap frames are generated session attachments, not the source of
truth. Recovery verifies the source digest before re-rendering a missing frame.
No new SQLite migration is required; the existing checkpoint transaction
commits archive manifest and replaceable model history together with
`semantic_revision=0`.

### Revision, evidence, and learning records

The adaptive coding pipeline does not add schema 22. Session-scoped control
records reuse `context_artifacts`, whose payloads already live in the schema-21
blob store. Artifact kinds are versioned and purpose-specific:

- `work_revision_v1:*`, `action_intent_v1:*`,
  `observation_envelope_v1:*`, `work_disposition_v1:*`, and
  `guidance_decision_v1:*` bind asynchronous work and its disposition to one
  canonical user/Todo/semantic snapshot.
- `verification_plan_v1:*` and `verification_result_v1:*` retain
  criterion-level verification evidence at that same revision.
- `evidence_ledger_v1:*` records ranked candidates, selected sources, and the
  downstream consumer outcome.
- `coding_memory_catalog_v1` and `coding_memory_policy_v1:*` retain typed
  memories, tombstones, provenance, and the explicit per-user opt-in policy.

These records are strict JSON inside the existing session/run ownership and
SHA-256 boundaries. Large payload behavior, deletion, fork behavior, and blob
verification therefore remain schema-21 behavior; runtime migration files and
`dbgen/schema.sql` are unchanged. The trajectory exporter opens the database
read-only at the service boundary and writes a detached JSON export. Replay,
noise, routing, training, tool-lab, and adapter artifacts are offline files or
in-process control-plane inputs; they do not create hidden SQLite tables.


`history_fts` is also the durable conversation-content index used by desktop
global search. Insert, update, delete, and session-cascade triggers keep
canonical user blocks and completed assistant blocks synchronized. Global
search never indexes mutable agent/process output or cancelled partial answers,
and it returns FTS snippets rather than loading complete block payloads into
React. Session-title matching scans only the short local title column; content
matching and ranking remain on FTS5.

## Stored data groups

| Group | Examples | Owner |
|---|---|---|
| Conversation | sessions, canonical blocks, projections, provider requests, attachments | `internal/session` |
| Tool timeline | tool records, file evidence, action attempts, tool-call charges | `internal/session`, `internal/app`, Venat adapters |
| Agent execution | runs, tasks, leases, approvals, resume tokens, team state | Venat through `internal/store/sqlite` |
| Control plane | definition snapshots, admission reservations, resource claims | Venat through `stores_control_plane.go` |
| Context | archive manifests, memories, recaps, history FTS, context artifacts, work revisions, verification records, evidence ledgers, coding-memory catalogs; retained legacy semantic tables | `internal/app`, `internal/contextarchive`, `internal/memory`, `internal/recap`, `internal/session`, `internal/workrevision`, `internal/evidence`, `internal/codingmemory` |
| Product state | authentication metadata, model catalog cache, usage | corresponding internal services |
| Desktop navigation | project catalog, session-to-project ownership, last workspace session | `internal/session`, `internal/app`, `internal/desktop` |

Generated `dbgen` code is an implementation detail of the SQLite adapters;
application and UI packages should depend on focused services instead of raw
queries.

## Crash recovery

Before `PrepareRecovery`, each process acquires `azem.db.runtime.lock`. The
first live process holds it exclusively while recovering, then downgrades it to
a shared lock for the rest of its lifetime. Additional desktop windows wait
for that boundary and hold only a shared lock, so they cannot expire leases,
interrupt Subagents, or quarantine provider requests owned by another live
process. If the exclusive owner exits before publishing a completed recovery,
one waiter takes over recovery instead of accepting a partial boundary.

Only an exclusive owner treats active leases as belonging to a prior process
and expires them immediately. It also expires leftover active resource claims
so a dead workspace lock cannot queue later sessions. It quarantines
incomplete action attempts and provider requests so the runtime does not
replay unknown side effects blindly.

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

Required coverage includes runtime-fence ownership and failed-owner takeover,
previous-schema upgrade, schema 18 control-plane
tables and indexes, schema 19 project ownership backfill, schema 20 compaction-state
invalidation with canonical data retention, schema 21 blob extraction with
payload-column removal, current-version reopen, automatic backup, and rejection of a future schema. Run
`GOWORK=off go test ./...` before release.

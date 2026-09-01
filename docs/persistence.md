# Persistence and Recovery

Last verified: 2026-08-30

Azem stores configuration and durable runtime state locally. SQLite is the
authoritative catalog, search index, application control plane, and Venat
v0.16.1 durable backend. Large opaque payloads live in a content-addressed
file store so one `azem.db` does not grow without bound. Current schema
version: **27**.

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
| Event/record catalog rows (`run_id`, sequence, SHA-256) | Legacy event/record bodies larger than 4 KiB |
| App runs/tasks, approvals, claims, Team/Subagent state | Venat v0.16 execution, attempt, receipt, and binding payloads above the inline limit |
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
- Schema 27 execution state has no v0.15 representation. After the first v1
  execution spec, checkpoint, attempt, or result is written, the only supported
  rollback to a pre-v0.16 binary is to stop every Azem process and restore the
  complete pre-upgrade database backup with a binary that supports that
  backup's schema. This discards work created after the backup. Never merge v1
  rows into the older database or lower `user_version`.


Schema 18 introduced durable application control-plane tables and indexes:

- `agent_definition_snapshots` is retained for forward-compatible historical
  data; the v0.16 direct-engine runtime does not deploy or write definitions.
- `admission_reservations` and `resource_claims` remain Azem-owned application
  scheduling facts. They are separate from Venat v0.16 execution leases and
  durable effect attempts.

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

Schema 22 stores llmux provider model catalogs in SQLite:

- `llmux_provider_models` holds each provider's discovered or imported model
  rows (`id`, enabled flag, display metadata, modalities) as JSON payloads.
- `config.yaml` keeps `providers.llmux.<id>.enabled` and `base_url` only.
  A launch that still has YAML `models` copies them into SQLite once and
  rewrites that provider without the catalog.
- Discover, enable/disable, and later list/load paths use the SQLite catalog.
  Subscription ChatGPT/Grok/Cursor rows stay in `model_catalog`.
Schema 23 adds the native security-scan catalog:

- `security_scans`, `security_scan_progress`, and `security_scan_workers`
  retain target binding, model route, phase, cost, and durable Standard/Deep
  coordination.
- `security_scan_artifacts` stores scan-relative paths, media types, sizes, and
  SHA-256 seals; private canonical bytes live below the Azem data directory.
- `security_findings`, occurrences, locations, triage, matches, remediation
  attempts, and publications provide stable cross-scan indexing. Publication
  claims are exclusive; indeterminate or failed external effects are retained
  for reconciliation and never replayed automatically.
- Security model work uses Azem-owned scan/worker artifacts and direct v1
  execution bindings; it does not create hidden conversation sessions.

Schema 24 adds the durable conversation graph:

- `session_graphs` stores root/parent session identity, source provenance,
  active branch, and active leaf.
- `session_graph_entries` links every canonical block to one parent entry.
- `session_branches` and `session_labels` keep named heads and user labels.
  Navigation changes the active projection without deleting sibling history.

Schema 25 adds auth-broker control state:

- `auth_broker_tokens` stores only hashed bearer-token identities and
  revocation metadata.
- Disabled credentials, temporary account blocks, and usage observations are
  durable and account-scoped. Client snapshot caches remain encrypted files,
  not plaintext SQLite payloads.

Schema 26 adds `github_webhook_deliveries`. The signed delivery ID and payload
digest are inserted before a repair trigger, making webhook redelivery
idempotent across restart.
Schema 27 is the clean Venat v0.16 execution boundary:

- `agent_executions` stores one immutable execution spec plus its current
  status, fenced lease, continuation/result payload, version, and digest.
- `agent_effect_attempts` records versioned model/tool attempts as
  `running`, `succeeded`, `failed`, `unknown`, or `abandoned`. A claim after
  response loss moves the uncertain attempt to `unknown`; it is never replayed
  without an explicit reconciliation receipt.
- `agent_execution_receipts` makes start/resume/release/settlement commands
  idempotent by request hash, command key, and lease token.
- `agent_execution_bindings` maps an Azem session/run/agent/kind/segment to
  the immutable executable manifest and profile hash. This is the only bridge
  between application orchestration and Venat durable execution.

All four payload families use the schema-21 BlobStore threshold and verify
SHA-256 on hydration. Schema 27 does not delete the v0.15 application tables:
terminal history remains readable, while non-terminal legacy rows without a
v1 binding are marked `reconcile_required` instead of being replayed.

Schema 28 adds app-only project catalog visibility:

- `desktop_projects.visible=0` hides a project from navigation without deleting
  its workspace, sessions, or immutable `session_workspaces` ownership.
- Startup access updates recency without changing visibility. Explicitly opening
  the project sets visibility back to 1.
- If the active project is removed, the single GPUI window switches to another
  visible project. The only active project cannot be removed.
- The migration marks non-terminal `subagent_runs` stranded by older shutdown
  races as `interrupted`; current runtime recovery may requeue only a valid
  durable child owned by a recovered parent.


### Revision, evidence, and learning records

The adaptive coding records continue to reuse `context_artifacts`; native
security scanning is the schema-23 domain above, session graphs are schema 24,
auth-broker control state is schema 25, webhook receipts are schema 26,
Venat v0.16 execution durability is schema 27, and project-catalog visibility
is schema 28.

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

Adaptive records remain strict JSON inside the existing session/run ownership
and SHA-256 boundaries. Large payload behavior, deletion, fork behavior, and
blob verification remain schema-21 behavior. Schemas 22–27 add provider-model
catalogs, security scans, session graphs, auth-broker state, webhook delivery
receipts, and v1 execution durability. The trajectory
exporter opens the database read-only at the service boundary and writes a
detached JSON export. Replay, noise, routing, training, tool-lab, and adapter
artifacts remain offline files or in-process control-plane inputs.
Schema-21 JSON offload leaves `{}` in the inline column; text/byte payloads
leave an empty sentinel. Trajectory export recognizes only those sentinels,
loads the referenced blob, and verifies its SHA-256. A non-sentinel inline
value whose bytes disagree with the stored digest remains corruption and makes
the export fail closed.


`history_fts` is also the durable conversation-content index used by desktop
global search. Insert, update, delete, and session-cascade triggers keep
canonical user blocks and completed assistant blocks synchronized. Global
search never indexes mutable agent/process output or cancelled partial answers,
and it returns FTS snippets rather than loading complete block payloads into
the renderer. Session-title matching scans only the short local title column; content
matching and ranking remain on FTS5.

## Stored data groups

| Group | Examples | Owner |
|---|---|---|
| Conversation | sessions, canonical blocks, projections, provider requests, attachments | `internal/session` |
| Tool timeline | tool records, file evidence, legacy action attempts, tool-call charges | `internal/session`, `internal/app` |
| Application orchestration | runs, tasks, approvals, resume tokens, resource claims, Team/Subagent lifecycle | Azem through `internal/agentruntime` and focused services |
| Venat execution | execution spec/status/lease/checkpoint/result, model/tool attempts, receipts | Venat `durable.Runtime` through `internal/store/sqlite.DurableBackend` |
| Execution binding | session/run/agent/kind/segment, immutable manifest, profile hash | `internal/agent` |
| Context | archive manifests, memories, recaps, history FTS, context artifacts, work revisions, verification records, evidence ledgers, coding-memory catalogs; retained legacy semantic tables | `internal/app`, `internal/contextarchive`, `internal/memory`, `internal/recap`, `internal/session`, `internal/workrevision`, `internal/evidence`, `internal/codingmemory` |
| Product state | authentication metadata, model catalog cache, usage | corresponding internal services |
| Desktop navigation | project catalog, session-to-project ownership, last workspace session | `internal/session`, `internal/app`, `internal/desktop` |
| Session graph | graph metadata, entries, branches, labels, import provenance | `internal/session`, `internal/sessionimport` |
| Auth broker | hashed access tokens, disabled credentials, temporary blocks, usage observations | `internal/authbroker` |
| GitHub delivery | signed webhook delivery ID and digest receipts | `internal/githubwebhook` |

Generated `dbgen` code is an implementation detail of the SQLite adapters;
application and UI packages should depend on focused services instead of raw
queries.

## Crash recovery

Bootstrap resolves the destination database path without migration or open,
then acquires `azem.db.runtime.lock`. The first live process holds it
exclusively across legacy-home relocation, SQLite open/backup/schema upgrade,
legacy crash preparation, durable runtime construction, and replay. It
downgrades to a shared lifetime lock only after the recovery projection is
published. Additional windows wait for that boundary and receive
`shouldRecover == false`; they never expire work owned by the live process.
If the exclusive owner exits early, one waiter takes over.

The exclusive sequence is:

1. Open SQLite, take the upgrade lock, create the backup when required, and
   migrate through schema 28.
2. Before constructing `durable.Runtime`, expire dead legacy leases/resource
   claims and quarantine incomplete legacy action/provider-request records.
3. Construct the single service-lifetime durable runtime, enumerate
   non-terminal runs, and validate every v1 execution binding and manifest.
4. Leave a suspended approval waiting; start a pending binding; resume a
   persisted continuation with exact target facts; replay a terminal result;
   or surface an unknown attempt for explicit reconciliation.
5. Mark every non-terminal v0.15 row without a binding
   `reconcile_required`. Preserve its evidence; never synthesize a
   continuation or execute its pending tool.
6. Interrupt and requeue app-owned Subagent lifecycle rows, resume
   application-owned Team orchestration, repair tool projections, publish
   recovery state, then call `FinishRecovery`.

Succeeded attempts remain anti-replay evidence when a recovered model emits a
fresh call ID for a completed non-idempotent input. Unknown attempts retain
execution ID, operation ID, attempt number/version, checkpoint sequence, and
continuation phase until an explicit resolution.

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
GOWORK=off go test ./internal/store/sqlite
```

Required coverage includes runtime-fence ownership and failed-owner takeover,
previous-schema upgrade, schema 18 legacy control-plane retention, schema 19
project ownership, schema 20 context invalidation with canonical retention,
schema 21 blob extraction, schema 22 model catalogs, schema 23 security scans,
schema 24 graphs, schema 25 auth-broker state, schema 26 webhook deduplication,
schema 27 durable backend contract/binding/blob/response-loss behavior, and
schema 28 project visibility with retained session ownership.
Also verify current-version reopen, automatic backup, retained legacy
run/tool/approval rows, and future-schema rejection. Run
`GOWORK=off go test ./...` before release.

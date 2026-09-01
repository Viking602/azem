package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Viking602/azem/internal/blobstore"
)

const schemaVersion = 28

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS records (
		kind TEXT NOT NULL,
		key1 TEXT NOT NULL,
		key2 TEXT NOT NULL DEFAULT '',
		run_id TEXT NOT NULL DEFAULT '',
		task_id TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL DEFAULT 0,
		tool_name TEXT NOT NULL DEFAULT '',
		idempotency_key TEXT NOT NULL DEFAULT '',
		data BLOB NOT NULL,
		PRIMARY KEY (kind, key1, key2)
	);
	CREATE INDEX IF NOT EXISTS records_kind_run ON records(kind, run_id, created_at, key1);
	CREATE INDEX IF NOT EXISTS records_kind_task ON records(kind, task_id, created_at, key1);
	CREATE UNIQUE INDEX IF NOT EXISTS action_attempt_idempotency
		ON records(kind, run_id, task_id, tool_name, idempotency_key)
		WHERE kind = 'action_attempt' AND idempotency_key <> '';
	CREATE TABLE IF NOT EXISTS events (
		run_id TEXT NOT NULL,
		sequence INTEGER NOT NULL,
		recorded_at INTEGER NOT NULL,
		data BLOB NOT NULL,
		PRIMARY KEY (run_id, sequence)
	);
	CREATE TABLE IF NOT EXISTS leases (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		task_id TEXT NOT NULL,
		holder_id TEXT NOT NULL,
		status TEXT NOT NULL,
		expires_at INTEGER NOT NULL,
		version INTEGER NOT NULL,
		data BLOB NOT NULL
	);
	CREATE INDEX IF NOT EXISTS leases_task_version ON leases(run_id, task_id, version DESC);
	CREATE UNIQUE INDEX IF NOT EXISTS leases_active_slot ON leases(run_id, task_id)
		WHERE status = 'active';
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL DEFAULT '',
		provider_id TEXT NOT NULL DEFAULT '',
		model_id TEXT NOT NULL DEFAULT '',
		reasoning TEXT NOT NULL DEFAULT '',
		agent_mode TEXT NOT NULL DEFAULT 'single',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS session_projections (
		session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
		last_run_id TEXT NOT NULL DEFAULT '',
		blocks BLOB NOT NULL DEFAULT '[]',
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS accounts (
		id TEXT NOT NULL,
		provider_id TEXT NOT NULL,
		email TEXT NOT NULL DEFAULT '',
		display_name TEXT NOT NULL DEFAULT '',
		plan TEXT NOT NULL DEFAULT '',
		credential_ref TEXT NOT NULL,
		status TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY(provider_id, id)
	);
	CREATE TABLE IF NOT EXISTS model_catalog (
		provider_id TEXT NOT NULL,
		account_id TEXT NOT NULL,
		model_id TEXT NOT NULL,
		etag TEXT NOT NULL DEFAULT '',
		fetched_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		data BLOB NOT NULL,
		PRIMARY KEY(provider_id, account_id, model_id)
	);
	CREATE TABLE IF NOT EXISTS subagent_runs (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		parent_run_id TEXT NOT NULL,
		parent_agent_id TEXT NOT NULL DEFAULT '',
		tool_call_id TEXT NOT NULL DEFAULT '',
		role TEXT NOT NULL,
		state TEXT NOT NULL,
		summary TEXT NOT NULL DEFAULT '',
		started_at INTEGER NOT NULL,
		finished_at INTEGER NOT NULL DEFAULT 0
	);`,
	`CREATE TABLE IF NOT EXISTS auth_credentials (
		provider_id TEXT NOT NULL,
		account_id TEXT NOT NULL,
		data TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY(provider_id, account_id)
	);`,
	`CREATE TABLE IF NOT EXISTS subagent_runs (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		parent_run_id TEXT NOT NULL,
		parent_agent_id TEXT NOT NULL DEFAULT '',
		tool_call_id TEXT NOT NULL DEFAULT '',
		role TEXT NOT NULL,
		state TEXT NOT NULL,
		summary TEXT NOT NULL DEFAULT '',
		started_at INTEGER NOT NULL,
		finished_at INTEGER NOT NULL DEFAULT 0
	);
	ALTER TABLE subagent_runs RENAME COLUMN role TO subagent_type;
	ALTER TABLE subagent_runs ADD COLUMN child_run_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN description TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN model TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN reasoning TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN capability_mode TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN requested_isolation TEXT NOT NULL DEFAULT 'none';
	ALTER TABLE subagent_runs ADD COLUMN isolation TEXT NOT NULL DEFAULT 'none';
	ALTER TABLE subagent_runs ADD COLUMN cwd TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN background INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE subagent_runs ADD COLUMN output TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN error TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN warning TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN transcript BLOB NOT NULL DEFAULT X'';
	ALTER TABLE subagent_runs ADD COLUMN tool_calls INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE subagent_runs ADD COLUMN turns INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE subagent_runs ADD COLUMN tokens_used INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE subagent_runs ADD COLUMN tools_used BLOB NOT NULL DEFAULT '[]';
	ALTER TABLE subagent_runs ADD COLUMN worktree_path TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN completion_delivered INTEGER NOT NULL DEFAULT 0;
	UPDATE subagent_runs SET state='completed' WHERE state='succeeded';
	CREATE INDEX IF NOT EXISTS subagent_runs_session_started ON subagent_runs(session_id, started_at);
	CREATE INDEX IF NOT EXISTS subagent_runs_parent_state ON subagent_runs(parent_run_id, state);`,
	`CREATE TABLE session_todos (
		session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
		goal TEXT NOT NULL DEFAULT '',
		revision INTEGER NOT NULL DEFAULT 0,
		phases BLOB NOT NULL DEFAULT '[]',
		updated_at INTEGER NOT NULL
	);`,
	`CREATE TABLE memories (
		memory_rowid INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, content TEXT NOT NULL, anchor TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '',
		provenance TEXT NOT NULL CHECK(provenance IN ('manual','runtime')),
		status TEXT NOT NULL CHECK(status IN ('active','forgotten')), importance INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
	);
	CREATE INDEX memories_scope_recent ON memories(anchor,status,updated_at DESC);
	CREATE VIRTUAL TABLE memories_fts USING fts5(content, content='memories', content_rowid='memory_rowid');
	CREATE TRIGGER memories_ai AFTER INSERT ON memories BEGIN INSERT INTO memories_fts(rowid,content) VALUES(new.memory_rowid,new.content); END;
	CREATE TRIGGER memories_ad AFTER DELETE ON memories BEGIN INSERT INTO memories_fts(memories_fts,rowid,content) VALUES('delete',old.memory_rowid,old.content); END;
	CREATE TRIGGER memories_au AFTER UPDATE OF content ON memories BEGIN INSERT INTO memories_fts(memories_fts,rowid,content) VALUES('delete',old.memory_rowid,old.content); INSERT INTO memories_fts(rowid,content) VALUES(new.memory_rowid,new.content); END;
	CREATE TABLE recaps (
		session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE, anchor TEXT NOT NULL,
		covered_boundary TEXT NOT NULL DEFAULT '', revision INTEGER NOT NULL DEFAULT 1,
		goal TEXT NOT NULL DEFAULT '', summary TEXT NOT NULL DEFAULT '', open_items TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL
	);
	CREATE INDEX recaps_anchor_updated ON recaps(anchor,updated_at DESC);`,
	`CREATE TABLE IF NOT EXISTS session_projections (
		session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
		last_run_id TEXT NOT NULL DEFAULT '',
		blocks BLOB NOT NULL DEFAULT '[]',
		updated_at INTEGER NOT NULL
	);
	ALTER TABLE session_projections ADD COLUMN model_history BLOB NOT NULL DEFAULT '{}';`,
	`CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL DEFAULT '',
		provider_id TEXT NOT NULL DEFAULT '',
		model_id TEXT NOT NULL DEFAULT '',
		reasoning TEXT NOT NULL DEFAULT '',
		agent_mode TEXT NOT NULL DEFAULT 'single',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE session_blocks (
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		sequence INTEGER NOT NULL,
		kind TEXT NOT NULL,
		run_id TEXT NOT NULL DEFAULT '',
		agent_id TEXT NOT NULL DEFAULT '',
		data BLOB NOT NULL,
		PRIMARY KEY(session_id, sequence)
	);
	CREATE INDEX session_blocks_run ON session_blocks(session_id, run_id, sequence);
	CREATE UNIQUE INDEX session_blocks_agent ON session_blocks(session_id, agent_id)
		WHERE kind='agent' AND agent_id<>'';
	INSERT INTO session_blocks(session_id,sequence,kind,run_id,agent_id,data)
		SELECT p.session_id, CAST(j.key AS INTEGER),
			COALESCE(json_extract(j.value,'$.kind'),''),
			COALESCE(json_extract(j.value,'$.runId'),''),
			COALESCE(json_extract(j.value,'$.agentId'),''),
			CAST(j.value AS BLOB)
		FROM session_projections p, json_each(p.blocks) j;
	UPDATE session_projections SET blocks='[]';`,
	`ALTER TABLE subagent_runs ADD COLUMN provider TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE session_projections ADD COLUMN usage BLOB NOT NULL DEFAULT '{}';`,
	`ALTER TABLE session_projections ADD COLUMN checkpoint_generation INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE session_projections ADD COLUMN cache_epoch INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE session_projections ADD COLUMN cache_identity_hash TEXT NOT NULL DEFAULT '';`,
	`CREATE TABLE context_artifacts (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		run_id TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		payload BLOB NOT NULL,
		preview TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		UNIQUE(session_id,kind,sha256)
	);
	CREATE INDEX context_artifacts_session_created ON context_artifacts(session_id,created_at);`,
	`CREATE TABLE provider_requests (
		request_id TEXT PRIMARY KEY,
		provider_request_id TEXT NOT NULL DEFAULT '',
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		run_id TEXT NOT NULL DEFAULT '',
		request_kind TEXT NOT NULL,
		provider TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		transport TEXT NOT NULL DEFAULT '',
		cache_epoch INTEGER NOT NULL DEFAULT 0,
		checkpoint_generation INTEGER NOT NULL DEFAULT 0,
		input_tokens INTEGER NOT NULL DEFAULT 0,
		cached_tokens INTEGER NOT NULL DEFAULT 0,
		cache_write_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		reasoning_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		cache_reported INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL,
		started_at INTEGER NOT NULL,
		completed_at INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX provider_requests_session_kind_epoch_completed
		ON provider_requests(session_id,request_kind,cache_epoch,completed_at);`,
	`CREATE VIRTUAL TABLE history_fts USING fts5(
		content,
		session_id UNINDEXED,
		source_type UNINDEXED,
		source_id UNINDEXED
	);
	INSERT INTO history_fts(content,session_id,source_type,source_id)
		SELECT COALESCE(json_extract(data,'$.content'),''),session_id,'sequence','sequence:'||sequence
		FROM session_blocks WHERE (kind='user' OR (kind='assistant' AND COALESCE(json_extract(data,'$.state'),'') IN ('','completed')))
			AND TRIM(COALESCE(json_extract(data,'$.content'),''))<>'';
	INSERT INTO history_fts(content,session_id,source_type,source_id)
		SELECT preview,session_id,'artifact','artifact:'||id FROM context_artifacts WHERE TRIM(preview)<>'';
	CREATE TRIGGER history_blocks_ai AFTER INSERT ON session_blocks
		WHEN (new.kind='user' OR (new.kind='assistant' AND COALESCE(json_extract(new.data,'$.state'),'') IN ('','completed')))
			AND TRIM(COALESCE(json_extract(new.data,'$.content'),''))<>''
		BEGIN INSERT INTO history_fts(content,session_id,source_type,source_id) VALUES(COALESCE(json_extract(new.data,'$.content'),''),new.session_id,'sequence','sequence:'||new.sequence); END;
	CREATE TRIGGER history_blocks_ad AFTER DELETE ON session_blocks WHEN old.kind IN ('user','assistant')
		BEGIN DELETE FROM history_fts WHERE session_id=old.session_id AND source_type='sequence' AND source_id='sequence:'||old.sequence; END;
	CREATE TRIGGER history_blocks_au AFTER UPDATE ON session_blocks
		BEGIN
			DELETE FROM history_fts WHERE session_id=old.session_id AND source_type='sequence' AND source_id='sequence:'||old.sequence;
			INSERT INTO history_fts(content,session_id,source_type,source_id)
				SELECT COALESCE(json_extract(new.data,'$.content'),''),new.session_id,'sequence','sequence:'||new.sequence
				WHERE (new.kind='user' OR (new.kind='assistant' AND COALESCE(json_extract(new.data,'$.state'),'') IN ('','completed')))
					AND TRIM(COALESCE(json_extract(new.data,'$.content'),''))<>'';
		END;
	CREATE TRIGGER history_artifacts_ai AFTER INSERT ON context_artifacts WHEN TRIM(new.preview)<>''
		BEGIN INSERT INTO history_fts(content,session_id,source_type,source_id) VALUES(new.preview,new.session_id,'artifact','artifact:'||new.id); END;
	CREATE TRIGGER history_artifacts_ad AFTER DELETE ON context_artifacts
		BEGIN DELETE FROM history_fts WHERE session_id=old.session_id AND source_type='artifact' AND source_id='artifact:'||old.id; END;
	CREATE TRIGGER history_artifacts_au AFTER UPDATE ON context_artifacts
		BEGIN
			DELETE FROM history_fts WHERE session_id=old.session_id AND source_type='artifact' AND source_id='artifact:'||old.id;
			INSERT INTO history_fts(content,session_id,source_type,source_id)
				SELECT new.preview,new.session_id,'artifact','artifact:'||new.id WHERE TRIM(new.preview)<>'';
		END;`,
	`CREATE TABLE tool_call_charges (
		run_id TEXT NOT NULL,
		task_id TEXT NOT NULL,
		call_id TEXT NOT NULL,
		tool_name TEXT NOT NULL,
		input_hash TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY(run_id,task_id,call_id)
	);
	CREATE INDEX tool_call_charges_run_task ON tool_call_charges(run_id,task_id);`,
	`CREATE TABLE session_tool_records (
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		run_id TEXT NOT NULL,
		tool_call_id TEXT NOT NULL,
		anchor_sequence INTEGER NOT NULL DEFAULT -1,
		name TEXT NOT NULL,
		arguments BLOB NOT NULL DEFAULT '{}',
		state TEXT NOT NULL,
		content TEXT NOT NULL DEFAULT '',
		structured BLOB NOT NULL DEFAULT 'null',
		artifact_id TEXT NOT NULL DEFAULT '',
		observations BLOB NOT NULL DEFAULT '[]',
		started_at INTEGER NOT NULL,
		completed_at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(session_id,run_id,tool_call_id)
	);
	CREATE INDEX session_tool_records_session_started
		ON session_tool_records(session_id,started_at,run_id,tool_call_id);
	CREATE TABLE workspace_session_state (
		anchor TEXT PRIMARY KEY,
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		updated_at INTEGER NOT NULL
	);`,
	`ALTER TABLE provider_requests ADD COLUMN cache_write_reported INTEGER NOT NULL DEFAULT 0;
	UPDATE provider_requests SET cache_write_reported=1 WHERE cache_write_tokens<>0;`,
	`CREATE TABLE session_ui_state (
		session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
		pinned INTEGER NOT NULL DEFAULT 0,
		archived INTEGER NOT NULL DEFAULT 0,
		unread INTEGER NOT NULL DEFAULT 0
	);`,
	`CREATE TABLE agent_definition_snapshots (
		definition_id TEXT NOT NULL,
		version TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		data BLOB NOT NULL,
		PRIMARY KEY(definition_id,version)
	);
	CREATE INDEX agent_definition_snapshots_created
		ON agent_definition_snapshots(created_at,definition_id,version);
	CREATE TABLE admission_reservations (
		id TEXT PRIMARY KEY,
		agent_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		state TEXT NOT NULL,
		version INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		data BLOB NOT NULL,
		UNIQUE(agent_id,run_id)
	);
	CREATE INDEX admission_reservations_agent_state
		ON admission_reservations(agent_id,state,created_at,id);
	CREATE INDEX admission_reservations_expiry
		ON admission_reservations(state,expires_at,id);
	CREATE TABLE resource_claims (
		id TEXT PRIMARY KEY,
		resource_key TEXT NOT NULL,
		run_id TEXT NOT NULL,
		task_id TEXT NOT NULL,
		lease_id TEXT NOT NULL,
		holder_id TEXT NOT NULL,
		mode TEXT NOT NULL,
		state TEXT NOT NULL,
		version INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		data BLOB NOT NULL
	);
	CREATE INDEX resource_claims_owner
		ON resource_claims(run_id,task_id,holder_id,created_at,id);
	CREATE INDEX resource_claims_lease_state
		ON resource_claims(lease_id,state,id);
	CREATE INDEX resource_claims_key_state_expiry
		ON resource_claims(resource_key,state,expires_at,id);`,
	`CREATE TABLE desktop_projects (
		workspace TEXT PRIMARY KEY,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE session_workspaces (
		session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
		workspace TEXT NOT NULL REFERENCES desktop_projects(workspace) ON DELETE RESTRICT,
		assigned_at INTEGER NOT NULL
	);
	CREATE INDEX session_workspaces_workspace ON session_workspaces(workspace,assigned_at DESC,session_id);
	INSERT INTO desktop_projects(workspace,updated_at)
		SELECT anchor,MAX(updated_at) FROM workspace_session_state WHERE TRIM(anchor)<>'' GROUP BY anchor;
	INSERT INTO session_workspaces(session_id,workspace,assigned_at)
		SELECT session_id,anchor,updated_at FROM workspace_session_state WHERE TRIM(anchor)<>''
		ON CONFLICT(session_id) DO NOTHING;`,
	`CREATE TABLE session_semantic_state (
		session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
		revision INTEGER NOT NULL DEFAULT 0,
		checkpoint_id TEXT NOT NULL DEFAULT '',
		cursor BLOB NOT NULL DEFAULT '{}',
		state BLOB NOT NULL DEFAULT '{}',
		source_digest TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE session_semantic_state_events (
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		revision INTEGER NOT NULL,
		checkpoint_id TEXT NOT NULL,
		base_revision INTEGER NOT NULL,
		cursor BLOB NOT NULL,
		patch BLOB NOT NULL,
		source_digest TEXT NOT NULL,
		writer_run_id TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		PRIMARY KEY(session_id,revision),
		UNIQUE(session_id,base_revision,source_digest)
	);
	CREATE TABLE context_manifests (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		run_id TEXT NOT NULL DEFAULT '',
		canonical_high_water INTEGER NOT NULL DEFAULT -1,
		semantic_revision INTEGER NOT NULL DEFAULT 0,
		policy_version INTEGER NOT NULL,
		manifest_hash TEXT NOT NULL,
		activated INTEGER NOT NULL DEFAULT 0,
		data BLOB NOT NULL,
		created_at INTEGER NOT NULL,
		UNIQUE(session_id,manifest_hash)
	);
	CREATE INDEX context_manifests_session_created
		ON context_manifests(session_id,created_at DESC,id);
	CREATE UNIQUE INDEX context_manifests_one_active
		ON context_manifests(session_id) WHERE activated=1;
	UPDATE session_projections
		SET model_history='{}', checkpoint_generation=checkpoint_generation+1,
			cache_epoch=cache_epoch+1, cache_identity_hash='';`,
	`ALTER TABLE session_tool_records ADD COLUMN content_sha256 TEXT NOT NULL DEFAULT '';
	ALTER TABLE session_tool_records ADD COLUMN structured_sha256 TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN transcript_sha256 TEXT NOT NULL DEFAULT '';
	ALTER TABLE subagent_runs ADD COLUMN output_sha256 TEXT NOT NULL DEFAULT '';
	ALTER TABLE session_projections ADD COLUMN model_history_sha256 TEXT NOT NULL DEFAULT '';
	ALTER TABLE session_blocks ADD COLUMN data_sha256 TEXT NOT NULL DEFAULT '';`,
	`CREATE TABLE llmux_provider_models (
		provider_id TEXT NOT NULL,
		model_id TEXT NOT NULL,
		payload BLOB NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY(provider_id, model_id)
	);
	CREATE INDEX llmux_provider_models_provider ON llmux_provider_models(provider_id, model_id);`,
	`CREATE TABLE security_scans (
		id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL DEFAULT '',
		requested_session_id TEXT NOT NULL DEFAULT '',
		root_run_id TEXT NOT NULL DEFAULT '',
		parent_scan_id TEXT NOT NULL DEFAULT '',
		target_id TEXT NOT NULL,
		target_kind TEXT NOT NULL,
		target_path TEXT NOT NULL,
		target_snapshot_digest TEXT NOT NULL,
		mode TEXT NOT NULL CHECK(mode IN ('standard','deep')),
		status TEXT NOT NULL CHECK(status IN ('queued','running','blocked','complete','failed','canceled')),
		phase TEXT NOT NULL,
		completeness TEXT NOT NULL DEFAULT '' CHECK(completeness IN ('','complete','partial','unknown')),
		route_json BLOB NOT NULL,
		budget_json BLOB NOT NULL,
		deep_json BLOB NOT NULL,
		target_json BLOB NOT NULL,
		knowledge_json BLOB NOT NULL DEFAULT '[]',
		user_context TEXT NOT NULL DEFAULT '',
		workflow_version TEXT NOT NULL,
		contract_version TEXT NOT NULL,
		output_dir TEXT NOT NULL,
		failure_message TEXT NOT NULL DEFAULT '',
		blocking_reason TEXT NOT NULL DEFAULT '',
		warning TEXT NOT NULL DEFAULT '',
		input_tokens INTEGER NOT NULL DEFAULT 0,
		cached_input_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		estimated_cost_usd REAL NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		started_at INTEGER NOT NULL DEFAULT 0,
		completed_at INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX security_scans_project_created ON security_scans(project_id,created_at DESC,id);
	CREATE INDEX security_scans_target_status ON security_scans(target_id,status,created_at DESC,id);
	CREATE INDEX security_scans_parent ON security_scans(parent_scan_id,created_at,id);
	CREATE UNIQUE INDEX security_scans_one_active_deep_target
		ON security_scans(project_id,target_id,target_snapshot_digest)
		WHERE mode='deep' AND status IN ('queued','running','blocked');

	CREATE TABLE security_scan_workers (
		id TEXT PRIMARY KEY,
		scan_id TEXT NOT NULL REFERENCES security_scans(id) ON DELETE CASCADE,
		run_id TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL CHECK(kind IN ('audit','reducer','matcher','fixer','verifier')),
		status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed','canceled')),
		sequence INTEGER NOT NULL,
		attempt INTEGER NOT NULL,
		completion_sequence INTEGER NOT NULL DEFAULT 0,
		route_json BLOB NOT NULL,
		result_path TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		started_at INTEGER NOT NULL DEFAULT 0,
		completed_at INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL,
		UNIQUE(scan_id,kind,sequence,attempt)
	);
	CREATE INDEX security_scan_workers_scan_status ON security_scan_workers(scan_id,status,kind,sequence);
	CREATE UNIQUE INDEX security_scan_workers_completion
		ON security_scan_workers(scan_id,completion_sequence)
		WHERE completion_sequence > 0;

	CREATE TABLE security_scan_progress (
		scan_id TEXT PRIMARY KEY REFERENCES security_scans(id) ON DELETE CASCADE,
		phase TEXT NOT NULL,
		files_completed INTEGER NOT NULL DEFAULT 0,
		files_total INTEGER NOT NULL DEFAULT 0,
		reviewed_paths_json BLOB NOT NULL DEFAULT '[]',
		workers_planned INTEGER NOT NULL DEFAULT 0,
		workers_running INTEGER NOT NULL DEFAULT 0,
		workers_done INTEGER NOT NULL DEFAULT 0,
		message TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL
	);

	CREATE TABLE security_scan_artifacts (
		scan_id TEXT NOT NULL REFERENCES security_scans(id) ON DELETE CASCADE,
		kind TEXT NOT NULL,
		path TEXT NOT NULL,
		media_type TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		byte_size INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY(scan_id,kind,path)
	);

	CREATE TABLE security_findings (
		id TEXT PRIMARY KEY,
		target_id TEXT NOT NULL,
		fingerprint TEXT NOT NULL UNIQUE,
		rule_id TEXT NOT NULL,
		identity_anchor TEXT NOT NULL,
		identity_instance TEXT NOT NULL DEFAULT '',
		first_seen_at INTEGER NOT NULL,
		last_seen_at INTEGER NOT NULL
	);
	CREATE INDEX security_findings_target_last_seen ON security_findings(target_id,last_seen_at DESC,id);

	CREATE TABLE security_finding_occurrences (
		id TEXT PRIMARY KEY,
		finding_id TEXT NOT NULL REFERENCES security_findings(id),
		scan_id TEXT NOT NULL REFERENCES security_scans(id) ON DELETE CASCADE,
		severity TEXT NOT NULL,
		confidence TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'open' CHECK(status IN ('open','closed')),
		title TEXT NOT NULL,
		summary TEXT NOT NULL,
		details_json BLOB NOT NULL,
		created_at INTEGER NOT NULL,
		UNIQUE(scan_id,finding_id)
	);
	CREATE INDEX security_occurrences_scan_severity ON security_finding_occurrences(scan_id,severity,id);
	CREATE INDEX security_occurrences_finding_scan ON security_finding_occurrences(finding_id,scan_id);

	CREATE TABLE security_finding_locations (
		occurrence_id TEXT NOT NULL REFERENCES security_finding_occurrences(id) ON DELETE CASCADE,
		sort_order INTEGER NOT NULL,
		path TEXT NOT NULL,
		start_line INTEGER NOT NULL,
		end_line INTEGER NOT NULL,
		role TEXT NOT NULL,
		PRIMARY KEY(occurrence_id,sort_order)
	);

	CREATE TABLE security_finding_triage (
		occurrence_id TEXT PRIMARY KEY REFERENCES security_finding_occurrences(id) ON DELETE CASCADE,
		status TEXT NOT NULL CHECK(status IN ('open','closed')),
		close_reason TEXT NOT NULL DEFAULT '' CHECK(close_reason IN ('','false_positive','already_fixed','wont_fix')),
		note TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL
	);

	CREATE TABLE security_remediation_attempts (
		id TEXT PRIMARY KEY,
		occurrence_id TEXT NOT NULL REFERENCES security_finding_occurrences(id) ON DELETE CASCADE,
		state TEXT NOT NULL CHECK(state IN ('requested','generated','applied','verifying','verified','no_change','blocked','failed','superseded')),
		version INTEGER NOT NULL,
		base_revision TEXT NOT NULL,
		base_snapshot_digest TEXT NOT NULL,
		applied_snapshot_digest TEXT NOT NULL DEFAULT '',
		files_json BLOB NOT NULL DEFAULT '[]',
		verification TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		branch TEXT NOT NULL DEFAULT '',
		commit_sha TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX security_remediation_occurrence_created
		ON security_remediation_attempts(occurrence_id,created_at DESC,id);

	CREATE TABLE security_scan_matches (
		before_occurrence_id TEXT NOT NULL,
		after_occurrence_id TEXT NOT NULL,
		match_kind TEXT NOT NULL CHECK(match_kind IN ('exact','semantic')),
		confidence REAL NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		PRIMARY KEY(before_occurrence_id,after_occurrence_id)
	);

	CREATE TABLE security_publications (
		scan_id TEXT NOT NULL REFERENCES security_scans(id) ON DELETE CASCADE,
		occurrence_id TEXT NOT NULL REFERENCES security_finding_occurrences(id) ON DELETE CASCADE,
		destination TEXT NOT NULL,
		status TEXT NOT NULL CHECK(status IN ('pending','publishing','published','failed')),
		external_id TEXT NOT NULL DEFAULT '',
		external_url TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY(scan_id,occurrence_id,destination)
	);`,
	`CREATE TABLE session_graphs (
		session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
		root_session_id TEXT NOT NULL,
		parent_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
		forked_from_entry_id TEXT NOT NULL DEFAULT '',
		prompt_cache_key TEXT NOT NULL DEFAULT '',
		active_branch TEXT NOT NULL DEFAULT 'main',
		active_leaf_entry_id TEXT NOT NULL DEFAULT '',
		source_kind TEXT NOT NULL DEFAULT 'native',
		source_ref TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		CHECK(parent_session_id IS NULL OR parent_session_id <> session_id)
	);
	CREATE INDEX session_graphs_root_created ON session_graphs(root_session_id,created_at,session_id);
	CREATE INDEX session_graphs_parent_created ON session_graphs(parent_session_id,created_at,session_id);

	CREATE TABLE session_graph_entries (
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		entry_id TEXT NOT NULL,
		source_entry_id TEXT NOT NULL DEFAULT '',
		parent_entry_id TEXT,
		block_sequence INTEGER NOT NULL,
		kind TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY(session_id,entry_id),
		UNIQUE(session_id,block_sequence),
		FOREIGN KEY(session_id,parent_entry_id) REFERENCES session_graph_entries(session_id,entry_id),
		FOREIGN KEY(session_id,block_sequence) REFERENCES session_blocks(session_id,sequence) ON DELETE CASCADE
	);
	CREATE INDEX session_graph_entries_parent ON session_graph_entries(session_id,parent_entry_id,block_sequence);
	CREATE UNIQUE INDEX session_graph_entries_source ON session_graph_entries(session_id,source_entry_id) WHERE source_entry_id<>'';

	CREATE TABLE session_branches (
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		name TEXT NOT NULL COLLATE NOCASE,
		head_entry_id TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY(session_id,name)
	);
	CREATE INDEX session_branches_head ON session_branches(session_id,head_entry_id);
	CREATE TABLE session_labels (
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		entry_id TEXT NOT NULL,
		label TEXT NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY(session_id,entry_id),
		FOREIGN KEY(session_id,entry_id) REFERENCES session_graph_entries(session_id,entry_id) ON DELETE CASCADE
	);
	CREATE INDEX session_labels_lookup ON session_labels(session_id,label,entry_id);

	INSERT INTO session_graphs(
		session_id,root_session_id,parent_session_id,forked_from_entry_id,prompt_cache_key,
		active_branch,active_leaf_entry_id,source_kind,source_ref,created_at,updated_at
	)
	SELECT id,id,NULL,'','', 'main','', 'native','',created_at,updated_at FROM sessions;

	INSERT INTO session_graph_entries(session_id,entry_id,parent_entry_id,block_sequence,kind,created_at)
	SELECT session_id,
		session_id || ':' || printf('%020d',sequence),
		LAG(session_id || ':' || printf('%020d',sequence)) OVER(PARTITION BY session_id ORDER BY sequence),
		sequence,kind,COALESCE((SELECT updated_at FROM sessions WHERE id=session_blocks.session_id),0)
	FROM session_blocks;

	UPDATE session_graphs
	SET active_leaf_entry_id=COALESCE((
		SELECT entry_id FROM session_graph_entries
		WHERE session_graph_entries.session_id=session_graphs.session_id
		ORDER BY block_sequence DESC LIMIT 1
	),'');

	INSERT INTO session_branches(session_id,name,head_entry_id,created_at,updated_at)
	SELECT session_id,'main',active_leaf_entry_id,created_at,updated_at FROM session_graphs;

	CREATE TRIGGER session_graph_after_session_insert
	AFTER INSERT ON sessions
	BEGIN
		INSERT INTO session_graphs(
			session_id,root_session_id,parent_session_id,forked_from_entry_id,prompt_cache_key,
			active_branch,active_leaf_entry_id,source_kind,source_ref,created_at,updated_at
		) VALUES(NEW.id,NEW.id,NULL,'','', 'main','', 'native','',NEW.created_at,NEW.updated_at);
		INSERT INTO session_branches(session_id,name,head_entry_id,created_at,updated_at)
		VALUES(NEW.id,'main','',NEW.created_at,NEW.updated_at);
	END;

	CREATE TRIGGER session_graph_after_block_insert
	AFTER INSERT ON session_blocks
	BEGIN
		INSERT INTO session_graph_entries(session_id,entry_id,parent_entry_id,block_sequence,kind,created_at)
		VALUES(
			NEW.session_id,
			NEW.session_id || ':' || printf('%020d',NEW.sequence),
			NULLIF((SELECT active_leaf_entry_id FROM session_graphs WHERE session_id=NEW.session_id),''),
			NEW.sequence,NEW.kind,COALESCE((SELECT updated_at FROM sessions WHERE id=NEW.session_id),NEW.sequence)
		);
		UPDATE session_graphs
		SET active_leaf_entry_id=NEW.session_id || ':' || printf('%020d',NEW.sequence),
			updated_at=COALESCE((SELECT updated_at FROM sessions WHERE id=NEW.session_id),updated_at)
		WHERE session_id=NEW.session_id;
		INSERT INTO session_branches(session_id,name,head_entry_id,created_at,updated_at)
		VALUES(
			NEW.session_id,
			COALESCE((SELECT active_branch FROM session_graphs WHERE session_id=NEW.session_id),'main'),
			NEW.session_id || ':' || printf('%020d',NEW.sequence),
			COALESCE((SELECT updated_at FROM sessions WHERE id=NEW.session_id),NEW.sequence),
			COALESCE((SELECT updated_at FROM sessions WHERE id=NEW.session_id),NEW.sequence)
		)
		ON CONFLICT(session_id,name) DO UPDATE SET
			head_entry_id=excluded.head_entry_id,
			updated_at=excluded.updated_at;
	END;`,
	`CREATE TABLE auth_broker_state (
		id INTEGER PRIMARY KEY CHECK(id=1),
		generation INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL
	);
	INSERT INTO auth_broker_state(id,generation,updated_at) VALUES(1,0,0);

	CREATE TABLE auth_broker_disabled (
		credential_id TEXT PRIMARY KEY,
		provider_id TEXT NOT NULL,
		account_id TEXT NOT NULL,
		cause TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX auth_broker_disabled_provider ON auth_broker_disabled(provider_id,updated_at,credential_id);

	CREATE TABLE auth_broker_blocks (
		credential_id TEXT NOT NULL,
		provider_id TEXT NOT NULL,
		scope TEXT NOT NULL,
		blocked_until INTEGER NOT NULL,
		reason TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL,
		PRIMARY KEY(credential_id,scope)
	);
	CREATE INDEX auth_broker_blocks_provider_expiry ON auth_broker_blocks(provider_id,blocked_until,credential_id);

	CREATE TABLE auth_broker_usage_observations (
		id TEXT PRIMARY KEY,
		client_id TEXT NOT NULL DEFAULT '',
		credential_id TEXT NOT NULL DEFAULT '',
		provider_id TEXT NOT NULL,
		account_id TEXT NOT NULL DEFAULT '',
		payload BLOB NOT NULL,
		observed_at INTEGER NOT NULL
	);
	CREATE INDEX auth_broker_usage_provider_time ON auth_broker_usage_observations(provider_id,observed_at,id);
	CREATE INDEX auth_broker_usage_client_time ON auth_broker_usage_observations(client_id,observed_at,id);

	CREATE TRIGGER auth_broker_credentials_ai AFTER INSERT ON auth_credentials BEGIN
		UPDATE auth_broker_state SET generation=generation+1,updated_at=NEW.updated_at WHERE id=1;
	END;
	CREATE TRIGGER auth_broker_credentials_au AFTER UPDATE ON auth_credentials BEGIN
		UPDATE auth_broker_state SET generation=generation+1,updated_at=NEW.updated_at WHERE id=1;
	END;
	CREATE TRIGGER auth_broker_credentials_ad AFTER DELETE ON auth_credentials BEGIN
		UPDATE auth_broker_state SET generation=generation+1,updated_at=CAST(strftime('%s','now') AS INTEGER)*1000000000 WHERE id=1;
	END;`,
	`CREATE TABLE github_webhook_deliveries (
		delivery_id TEXT PRIMARY KEY,
		event_name TEXT NOT NULL,
		repository TEXT NOT NULL,
		pull_request_number INTEGER NOT NULL DEFAULT 0,
		action TEXT NOT NULL DEFAULT '',
		payload_sha256 TEXT NOT NULL,
		status TEXT NOT NULL,
		received_at INTEGER NOT NULL
	);
	CREATE INDEX github_webhook_deliveries_received ON github_webhook_deliveries(received_at,delivery_id);
	CREATE INDEX github_webhook_deliveries_pr ON github_webhook_deliveries(repository,pull_request_number,received_at);`,
	`CREATE TABLE agent_executions (
		execution_id TEXT PRIMARY KEY,
		spec_hash BLOB NOT NULL CHECK(length(spec_hash)=32),
		status TEXT NOT NULL CHECK(status IN ('running','suspended','completed','failed')),
		version INTEGER NOT NULL CHECK(version>0),
		lease_owner TEXT NOT NULL DEFAULT '',
		lease_claim BLOB NOT NULL DEFAULT X'',
		lease_token INTEGER NOT NULL DEFAULT 0,
		lease_expires_at INTEGER NOT NULL DEFAULT 0,
		next_lease_token INTEGER NOT NULL DEFAULT 0,
		execution_inline BLOB NOT NULL,
		execution_digest TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX agent_executions_status ON agent_executions(status,updated_at,execution_id);
	CREATE INDEX agent_executions_lease_expiry ON agent_executions(lease_expires_at,execution_id);

	CREATE TABLE agent_effect_attempts (
		execution_id TEXT NOT NULL REFERENCES agent_executions(execution_id) ON DELETE CASCADE,
		operation_id TEXT NOT NULL,
		attempt_number INTEGER NOT NULL CHECK(attempt_number>0),
		kind TEXT NOT NULL CHECK(kind IN ('model','tool')),
		input_hash BLOB NOT NULL CHECK(length(input_hash)=32),
		status TEXT NOT NULL CHECK(status IN ('running','succeeded','failed','unknown','abandoned')),
		lease_owner TEXT NOT NULL DEFAULT '',
		lease_token INTEGER NOT NULL DEFAULT 0,
		version INTEGER NOT NULL CHECK(version>0),
		attempt_inline BLOB NOT NULL,
		attempt_digest TEXT NOT NULL DEFAULT '',
		PRIMARY KEY(execution_id,operation_id,attempt_number)
	);
	CREATE INDEX agent_effect_attempts_status ON agent_effect_attempts(execution_id,status,operation_id,attempt_number);

	CREATE TABLE agent_execution_receipts (
		execution_id TEXT NOT NULL REFERENCES agent_executions(execution_id) ON DELETE CASCADE,
		command_kind TEXT NOT NULL,
		command_key TEXT NOT NULL,
		request_hash BLOB NOT NULL CHECK(length(request_hash)=32),
		lease_token INTEGER NOT NULL DEFAULT 0,
		receipt_inline BLOB NOT NULL,
		receipt_digest TEXT NOT NULL DEFAULT '',
		PRIMARY KEY(execution_id,command_kind,command_key)
	);

	CREATE TABLE agent_execution_bindings (
		execution_id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL DEFAULT '',
		run_id TEXT NOT NULL,
		stable_id TEXT NOT NULL,
		agent_id TEXT NOT NULL,
		kind TEXT NOT NULL,
		segment INTEGER NOT NULL CHECK(segment>=0),
		manifest_inline BLOB NOT NULL,
		manifest_digest TEXT NOT NULL DEFAULT '',
		profile_hash TEXT NOT NULL,
		state TEXT NOT NULL,
		version INTEGER NOT NULL CHECK(version>0),
		updated_at INTEGER NOT NULL
	);
	CREATE UNIQUE INDEX agent_execution_bindings_segment ON agent_execution_bindings(kind,stable_id,segment);
	CREATE INDEX agent_execution_bindings_session ON agent_execution_bindings(session_id,updated_at,execution_id);`,
	`ALTER TABLE desktop_projects ADD COLUMN visible INTEGER NOT NULL DEFAULT 1;
	UPDATE subagent_runs
		SET state='interrupted',
			summary='interrupted by process restart',
			error='interrupted by process restart',
			finished_at=CASE
				WHEN finished_at=0 THEN CAST((julianday('now') - 2440587.5) * 86400000000000 AS INTEGER)
				ELSE finished_at
			END
		WHERE state IN ('initializing','queued','running','cancelling');`,
}

func migrate(ctx context.Context, db *sql.DB, blobs blobstore.Store) error {
	reclaimPages := false
	for {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin schema migration: %w", err)
		}
		var version int
		if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("read schema version: %w", err)
		}
		if version > schemaVersion {
			_ = tx.Rollback()
			return fmt.Errorf("database schema %d is newer than supported schema %d", version, schemaVersion)
		}
		if version == schemaVersion {
			if err := tx.Rollback(); err != nil {
				return err
			}
			if reclaimPages {
				if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
					return fmt.Errorf("reclaim extracted payload pages: %w", err)
				}
			}
			return nil
		}
		next := version + 1
		if _, err := tx.ExecContext(ctx, migrations[next-1]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", next, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)`, next); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", next, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, next)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("set schema version %d: %w", next, err)
		}
		if next == 21 {
			for _, statement := range []string{
				`ALTER TABLE events ADD COLUMN data_sha256 TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE records ADD COLUMN data_sha256 TEXT NOT NULL DEFAULT ''`,
			} {
				if _, err := tx.ExecContext(ctx, statement); err != nil && !isMissingRelation(err) {
					_ = tx.Rollback()
					return fmt.Errorf("apply migration %d: %w", next, err)
				}
			}
			if err := extractLargePayloads(ctx, tx, blobs); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("extract large payloads: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `ALTER TABLE context_artifacts DROP COLUMN payload`); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("drop artifact payload column: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `ALTER TABLE session_projections DROP COLUMN blocks`); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("drop leftover projection blocks column: %w", err)
			}
			reclaimPages = true
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", next, err)
		}
	}
}

func currentSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

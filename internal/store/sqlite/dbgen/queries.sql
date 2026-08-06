-- name: UpsertCredential :exec
INSERT INTO auth_credentials(provider_id,account_id,data,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(provider_id,account_id) DO UPDATE SET data=excluded.data,updated_at=excluded.updated_at;
-- name: GetCredential :one
SELECT data FROM auth_credentials WHERE provider_id=? AND account_id=?;
-- name: DeleteCredential :exec
DELETE FROM auth_credentials WHERE provider_id=? AND account_id=?;
-- name: GetCredentialRef :one
SELECT credential_ref FROM accounts WHERE provider_id=? AND id=?;
-- name: InsertMemory :exec
INSERT INTO memories(id,content,anchor,session_id,provenance,status,importance,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?);
-- name: ForgetMemory :execresult
UPDATE memories SET status='forgotten',updated_at=? WHERE id=? AND anchor=? AND status='active';
-- name: ListRecentMemories :many
SELECT id,content,anchor,session_id,provenance,status,importance,created_at,updated_at FROM memories WHERE anchor=? AND status='active' ORDER BY importance DESC,updated_at DESC LIMIT ?;
-- name: ListMemoriesByContent :many
SELECT id,content,anchor,session_id,provenance,status,importance,created_at,updated_at FROM memories WHERE anchor=sqlc.arg(anchor) AND status='active' AND instr(lower(content),lower(sqlc.arg(content)))>0 ORDER BY importance DESC,updated_at DESC LIMIT sqlc.arg(limit);
-- name: UpsertRecap :one
INSERT INTO recaps(session_id,anchor,covered_boundary,revision,goal,summary,open_items,updated_at) VALUES(?,?,?,1,?,?,?,?) ON CONFLICT(session_id) DO UPDATE SET covered_boundary=excluded.covered_boundary,revision=recaps.revision+1,goal=excluded.goal,summary=excluded.summary,open_items=excluded.open_items,updated_at=excluded.updated_at WHERE recaps.anchor=excluded.anchor RETURNING revision;
-- name: GetRecap :one
SELECT session_id,anchor,covered_boundary,revision,goal,summary,open_items,updated_at FROM recaps WHERE session_id=? AND anchor=?;
-- name: ListCatalog :many
SELECT fetched_at,expires_at,data FROM model_catalog WHERE provider_id=? AND account_id=? ORDER BY model_id;
-- name: DeleteCatalog :exec
DELETE FROM model_catalog WHERE provider_id=? AND account_id=?;
-- name: InsertCatalogModel :exec
INSERT INTO model_catalog(provider_id,account_id,model_id,etag,fetched_at,expires_at,data) VALUES(?,?,?,?,?,?,?);
-- name: ExtendCatalog :exec
UPDATE model_catalog SET expires_at=? WHERE provider_id=? AND account_id=?;
-- name: GetCatalogETag :one
SELECT etag FROM model_catalog WHERE provider_id=? AND account_id=? LIMIT 1;
-- name: GetTodo :one
SELECT goal,revision,phases,updated_at FROM session_todos WHERE session_id=?;
-- name: InsertTodoIfAbsent :execresult
INSERT INTO session_todos(session_id,goal,revision,phases,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(session_id) DO NOTHING;
-- name: UpdateTodoCAS :execresult
UPDATE session_todos SET goal=?,revision=?,phases=?,updated_at=? WHERE session_id=? AND revision=?;
-- name: UpdateUsage :execresult
UPDATE session_projections SET usage=? WHERE session_id=?;

-- name: InsertRecord :exec
INSERT INTO records(kind,key1,key2,run_id,task_id,status,created_at,tool_name,idempotency_key,data) VALUES(?,?,?,?,?,?,?,?,?,?);
-- name: UpsertRecord :exec
INSERT INTO records(kind,key1,key2,run_id,task_id,status,created_at,tool_name,idempotency_key,data) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(kind,key1,key2) DO UPDATE SET run_id=excluded.run_id,task_id=excluded.task_id,status=excluded.status,created_at=excluded.created_at,tool_name=excluded.tool_name,idempotency_key=excluded.idempotency_key,data=excluded.data;
-- name: GetRecordData :one
SELECT data FROM records WHERE kind=? AND key1=? AND key2=?;
-- name: ListRecordData :many
SELECT data FROM records WHERE kind=? ORDER BY created_at,key1,key2;
-- name: ListRecordDataByRun :many
SELECT data FROM records WHERE kind=? AND run_id=? ORDER BY created_at,key1,key2;
-- name: GetActionAttemptByIdempotency :one
SELECT data FROM records WHERE kind=? AND run_id=? AND task_id=? AND tool_name=? AND idempotency_key=?;

-- name: LatestEventSequence :one
SELECT sequence FROM events WHERE run_id=? ORDER BY sequence DESC LIMIT 1;
-- name: InsertEvent :exec
INSERT INTO events(run_id,sequence,recorded_at,data) VALUES(?,?,?,?);
-- name: ListEventData :many
SELECT data FROM events WHERE run_id=? ORDER BY sequence;
-- name: ListEventDataAfter :many
SELECT data FROM events WHERE run_id=? AND sequence>? ORDER BY sequence;

-- name: MaxLeaseVersion :one
SELECT CAST(COALESCE(MAX(version),0) AS INTEGER) FROM leases WHERE run_id=? AND task_id=?;
-- name: UpsertLease :exec
INSERT INTO leases(id,run_id,task_id,holder_id,status,expires_at,version,data) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET run_id=excluded.run_id,task_id=excluded.task_id,holder_id=excluded.holder_id,status=excluded.status,expires_at=excluded.expires_at,version=excluded.version,data=excluded.data;
-- name: InsertLease :exec
INSERT INTO leases(id,run_id,task_id,holder_id,status,expires_at,version,data) VALUES(?,?,?,?,?,?,?,?);
-- name: GetLeaseData :one
SELECT data FROM leases WHERE id=?;
-- name: GetLatestLeaseData :one
SELECT data FROM leases WHERE run_id=? AND task_id=? ORDER BY version DESC LIMIT 1;
-- name: GetLatestLease :one
SELECT version,data FROM leases WHERE run_id=? AND task_id=? ORDER BY version DESC LIMIT 1;
-- name: ExpireLeaseCAS :execresult
UPDATE leases SET status=?,data=? WHERE id=? AND version=?;
-- name: ExtendLeaseCAS :execresult
UPDATE leases SET expires_at=?,version=?,data=? WHERE id=? AND holder_id=? AND status=? AND version=?;
-- name: ReleaseExpiredLeaseCAS :execresult
UPDATE leases
SET status=?,version=?,data=?
WHERE leases.id=?
  AND leases.status=?
  AND leases.version=?
  AND leases.expires_at<=?
  AND leases.version=(
    SELECT MAX(latest.version)
    FROM leases AS latest
    WHERE latest.run_id=leases.run_id AND latest.task_id=leases.task_id
  );

-- name: ListActiveLeases :many
SELECT id,version,data FROM leases WHERE status=?;
-- name: ExpireActiveLeaseCAS :execresult
UPDATE leases SET status=?,data=? WHERE id=? AND version=? AND status=?;
-- name: ListIncompleteActionAttempts :many
SELECT key1,data FROM records WHERE kind=? AND status IN (?,?);
-- name: QuarantineActionAttemptCAS :execresult
UPDATE records SET status=?,data=? WHERE kind=? AND key1=? AND status IN (?,?);
-- name: QuarantineStartedProviderRequests :exec
UPDATE provider_requests SET status='unknown' WHERE status='started';
-- name: ListReconcileAttemptData :many
SELECT data FROM records WHERE kind=? AND status=? ORDER BY key1;
-- name: ListSucceededActionAttemptData :many
SELECT data FROM records WHERE kind=? AND run_id=? AND task_id=? AND status=? ORDER BY key1;
-- name: InsertToolCallCharge :execresult
INSERT OR IGNORE INTO tool_call_charges(run_id,task_id,call_id,tool_name,input_hash,created_at) VALUES(?,?,?,?,?,?);
-- name: GetToolCallCharge :one
SELECT tool_name,input_hash FROM tool_call_charges WHERE run_id=? AND task_id=? AND call_id=?;
-- name: CountToolCallCharges :one
SELECT COUNT(*) FROM tool_call_charges WHERE run_id=? AND task_id=?;
-- name: GetReconcileAttemptData :one
SELECT data FROM records WHERE kind=? AND key1=? AND status=?;
-- name: ResolveReconcileAttemptCAS :execresult
UPDATE records SET status=?,data=? WHERE kind=? AND key1=? AND status=?;

-- name: InsertAgentDefinitionSnapshot :execresult
INSERT OR IGNORE INTO agent_definition_snapshots(definition_id,version,created_at,data) VALUES(?,?,?,?);
-- name: GetAgentDefinitionSnapshotData :one
SELECT data FROM agent_definition_snapshots WHERE definition_id=? AND version=?;
-- name: ListAgentDefinitionSnapshotData :many
SELECT data FROM agent_definition_snapshots ORDER BY created_at,definition_id,version;

-- name: InsertAdmissionReservation :execresult
INSERT OR IGNORE INTO admission_reservations(id,agent_id,run_id,state,version,created_at,updated_at,expires_at,data) VALUES(?,?,?,?,?,?,?,?,?);
-- name: GetAdmissionReservationData :one
SELECT data FROM admission_reservations WHERE id=?;
-- name: GetAdmissionReservationDataForRun :one
SELECT data FROM admission_reservations WHERE agent_id=? AND run_id=?;
-- name: ListAdmissionReservationData :many
SELECT data FROM admission_reservations ORDER BY created_at,id;
-- name: ListAdmissionReservationDataByAgent :many
SELECT data FROM admission_reservations WHERE agent_id=? ORDER BY created_at,id;
-- name: UpdateAdmissionReservationCAS :execresult
UPDATE admission_reservations
SET state=sqlc.arg(next_state),version=sqlc.arg(next_version),updated_at=sqlc.arg(updated_at),expires_at=sqlc.arg(expires_at),data=sqlc.arg(data)
WHERE id=sqlc.arg(id) AND version=sqlc.arg(expected_version);

-- name: InsertResourceClaim :execresult
INSERT OR IGNORE INTO resource_claims(id,resource_key,run_id,task_id,lease_id,holder_id,mode,state,version,created_at,updated_at,expires_at,data) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?);
-- name: GetResourceClaimData :one
SELECT data FROM resource_claims WHERE id=?;
-- name: ListResourceClaimData :many
SELECT data FROM resource_claims ORDER BY created_at,id;
-- name: ListActiveResourceClaimDataByKey :many
SELECT data FROM resource_claims WHERE resource_key=? AND state='active' AND expires_at>? ORDER BY created_at,id;
-- name: UpdateResourceClaimCAS :execresult
UPDATE resource_claims
SET state=sqlc.arg(next_state),version=sqlc.arg(next_version),updated_at=sqlc.arg(updated_at),expires_at=sqlc.arg(expires_at),data=sqlc.arg(data)
WHERE id=sqlc.arg(id) AND version=sqlc.arg(expected_version);

-- name: InsertContextArtifact :exec
INSERT INTO context_artifacts(id,session_id,run_id,kind,sha256,payload,preview,created_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(session_id,kind,sha256) DO NOTHING;
-- name: GetContextArtifact :one
SELECT id,session_id,run_id,kind,sha256,payload,preview,created_at FROM context_artifacts WHERE id=? AND session_id=?;
-- name: EnsureSession :exec
INSERT INTO sessions(id,title,provider_id,model_id,reasoning,agent_mode,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING;
-- name: EnsureSessionProjection :exec
INSERT INTO session_projections(session_id,updated_at) VALUES(?,?) ON CONFLICT(session_id) DO NOTHING;
-- name: GetSession :one
SELECT id,title,provider_id,model_id,reasoning,agent_mode,created_at,updated_at FROM sessions WHERE id=?;
-- name: UpdateSessionPreferences :execresult
UPDATE sessions SET provider_id=?,model_id=?,reasoning=?,agent_mode=?,updated_at=? WHERE id=?;
-- name: GetSessionProjection :one
SELECT last_run_id,blocks,model_history,usage,updated_at,checkpoint_generation,cache_epoch,cache_identity_hash FROM session_projections WHERE session_id=?;
-- name: UpdateProjectionRun :exec
UPDATE session_projections SET last_run_id=?,updated_at=? WHERE session_id=?;
-- name: UpdateProjectionRunAfterAssistantMutation :exec
UPDATE session_projections SET last_run_id=sqlc.arg(last_run_id), model_history=CASE WHEN CAST(json_extract(model_history,'$.coveredThroughSequence') AS INTEGER)>=CAST(sqlc.arg(history_sequence) AS INTEGER) THEN '{}' ELSE model_history END, checkpoint_generation=checkpoint_generation+CASE WHEN CAST(json_extract(model_history,'$.coveredThroughSequence') AS INTEGER)>=CAST(sqlc.arg(generation_sequence) AS INTEGER) THEN 1 ELSE 0 END, updated_at=sqlc.arg(updated_at) WHERE session_id=sqlc.arg(session_id);
-- name: UpdateSessionTimestamp :exec
UPDATE sessions SET updated_at=? WHERE id=?;
-- name: GetProjectionCheckpoint :one
SELECT last_run_id,checkpoint_generation FROM session_projections WHERE session_id=?;
-- name: CompleteProjectionCAS :execresult
UPDATE session_projections SET last_run_id=?,model_history=?,checkpoint_generation=?,updated_at=? WHERE session_id=? AND last_run_id=? AND checkpoint_generation=?;
-- name: GetRunCheckpointState :one
SELECT last_run_id,checkpoint_generation,cache_epoch,cache_identity_hash FROM session_projections WHERE session_id=?;
-- name: GetProjectionHistory :one
SELECT model_history FROM session_projections WHERE session_id=?;
-- name: SaveRunCheckpointCAS :execresult
UPDATE session_projections SET model_history=?,checkpoint_generation=?,cache_epoch=?,cache_identity_hash=?,updated_at=? WHERE session_id=? AND last_run_id=? AND checkpoint_generation=?;
-- name: UpdateAgentBlock :execresult
UPDATE session_blocks SET run_id=?,data=? WHERE session_id=? AND kind='agent' AND agent_id=?;
-- name: TouchProjection :exec
UPDATE session_projections SET updated_at=? WHERE session_id=?;
-- name: GetCompactionState :one
SELECT model_history,updated_at,checkpoint_generation,cache_epoch FROM session_projections WHERE session_id=?;
-- name: SaveCompaction :exec
UPDATE session_projections SET model_history=?,checkpoint_generation=?,cache_epoch=?,cache_identity_hash='',updated_at=? WHERE session_id=?;
-- name: ListSessionBlocks :many
SELECT sequence,data FROM session_blocks WHERE session_id=? ORDER BY sequence;
-- name: GetLatestSessionBlock :one
SELECT sequence,data FROM session_blocks WHERE session_id=? ORDER BY sequence DESC LIMIT 1;
-- name: UpdateSessionBlockData :exec
UPDATE session_blocks SET data=? WHERE session_id=? AND sequence=?;
-- name: CanonicalHighWater :one
SELECT sequence FROM session_blocks WHERE session_id=? AND kind IN ('user','assistant') ORDER BY sequence DESC LIMIT 1;
-- name: InsertSessionBlock :exec
INSERT INTO session_blocks(session_id,sequence,kind,run_id,agent_id,data) SELECT ?,COALESCE(MAX(b.sequence)+1,0),?,?,?,? FROM session_blocks b WHERE b.session_id=?;
-- name: ListSessions :many
SELECT s.id,s.title,s.provider_id,s.model_id,s.reasoning,s.agent_mode,s.created_at,s.updated_at FROM sessions s JOIN session_projections p ON p.session_id=s.id WHERE p.last_run_id<>'' OR EXISTS(SELECT 1 FROM session_blocks b WHERE b.session_id=s.id) OR CAST(p.blocks AS TEXT)<>'[]' ORDER BY s.updated_at DESC;
-- name: ListSessionsLimited :many
SELECT s.id,s.title,s.provider_id,s.model_id,s.reasoning,s.agent_mode,s.created_at,s.updated_at FROM sessions s JOIN session_projections p ON p.session_id=s.id WHERE p.last_run_id<>'' OR EXISTS(SELECT 1 FROM session_blocks b WHERE b.session_id=s.id) OR CAST(p.blocks AS TEXT)<>'[]' ORDER BY s.updated_at DESC LIMIT ?;
-- name: InsertSessionToolRecord :execresult
INSERT INTO session_tool_records(session_id,run_id,tool_call_id,anchor_sequence,name,arguments,state,content,structured,artifact_id,observations,started_at,completed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(session_id,run_id,tool_call_id) DO NOTHING;
-- name: GetSessionToolRecord :one
SELECT anchor_sequence,name,arguments,state,content,structured,artifact_id,observations,started_at,completed_at FROM session_tool_records WHERE session_id=? AND run_id=? AND tool_call_id=?;
-- name: CompleteSessionToolRecordCAS :execresult
UPDATE session_tool_records SET name=?,state=?,content=?,structured=?,artifact_id=?,observations=?,completed_at=? WHERE session_id=? AND run_id=? AND tool_call_id=? AND state='running';
-- name: RestartInterruptedSessionToolRecordCAS :execresult
UPDATE session_tool_records SET name=?,arguments=?,state='running',content='',structured='null',artifact_id='',observations='[]',started_at=?,completed_at=0 WHERE session_id=? AND run_id=? AND tool_call_id=? AND state='interrupted';
-- name: ListSessionToolRecords :many
SELECT run_id,tool_call_id,anchor_sequence,name,arguments,state,content,structured,artifact_id,observations,started_at,completed_at FROM session_tool_records WHERE session_id=? ORDER BY started_at,run_id,tool_call_id;
-- name: InterruptRunningSessionToolRecordsByRun :exec
UPDATE session_tool_records SET state='interrupted',completed_at=? WHERE run_id=? AND state='running';
-- name: UpsertWorkspaceSession :exec
INSERT INTO workspace_session_state(anchor,session_id,updated_at) VALUES(?,?,?) ON CONFLICT(anchor) DO UPDATE SET session_id=excluded.session_id,updated_at=excluded.updated_at;
-- name: GetWorkspaceSession :one
SELECT session_id FROM workspace_session_state WHERE anchor=?;
-- name: ListAccounts :many
SELECT id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at FROM accounts ORDER BY updated_at DESC;
-- name: ListAccountsByProvider :many
SELECT id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at FROM accounts WHERE provider_id=? ORDER BY updated_at DESC;
-- name: HasAnyAccount :one
SELECT EXISTS(SELECT 1 FROM accounts WHERE provider_id=? LIMIT 1);
-- name: HasActiveChatGPTAccount :one
SELECT EXISTS(SELECT 1 FROM accounts WHERE provider_id='chatgpt' AND status='active');
-- name: GetAccount :one
SELECT id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at FROM accounts WHERE provider_id=? AND id=?;
-- name: UpsertAccount :exec
INSERT INTO accounts(id,provider_id,email,display_name,plan,credential_ref,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(provider_id,id) DO UPDATE SET email=excluded.email,display_name=excluded.display_name,plan=excluded.plan,credential_ref=excluded.credential_ref,status=excluded.status,updated_at=excluded.updated_at;
-- name: UpdateAccountStatus :execresult
UPDATE accounts SET status=?,updated_at=? WHERE provider_id=? AND id=?;

-- name: UpsertProviderRequest :exec
INSERT INTO provider_requests(request_id,provider_request_id,session_id,run_id,request_kind,provider,model,transport,cache_epoch,checkpoint_generation,input_tokens,cached_tokens,cache_write_tokens,cache_write_reported,output_tokens,reasoning_tokens,total_tokens,cache_reported,status,started_at,completed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(request_id) DO UPDATE SET provider_request_id=excluded.provider_request_id,session_id=excluded.session_id,run_id=excluded.run_id,request_kind=excluded.request_kind,provider=excluded.provider,model=excluded.model,transport=excluded.transport,cache_epoch=excluded.cache_epoch,checkpoint_generation=excluded.checkpoint_generation,input_tokens=excluded.input_tokens,cached_tokens=excluded.cached_tokens,cache_write_tokens=excluded.cache_write_tokens,cache_write_reported=excluded.cache_write_reported,output_tokens=excluded.output_tokens,reasoning_tokens=excluded.reasoning_tokens,total_tokens=excluded.total_tokens,cache_reported=excluded.cache_reported,status=excluded.status,started_at=MIN(provider_requests.started_at,excluded.started_at),completed_at=excluded.completed_at;
-- name: AggregateAllUsage :one
SELECT CAST(COALESCE(SUM(input_tokens),0) AS INTEGER) raw_input,CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN input_tokens ELSE 0 END),0) AS INTEGER) reported_input,CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN cached_tokens ELSE 0 END),0) AS INTEGER) cached,CAST(COALESCE(SUM(cache_write_tokens),0) AS INTEGER) cache_write,CAST(COALESCE(SUM(output_tokens),0) AS INTEGER) output,CAST(COALESCE(SUM(reasoning_tokens),0) AS INTEGER) reasoning,CAST(COALESCE(SUM(total_tokens),0) AS INTEGER) total,COUNT(*) requests,CAST(COALESCE(SUM(cache_reported),0) AS INTEGER) reported_requests,CAST(COALESCE(MAX(cache_reported),0) AS INTEGER) reported,CAST(COALESCE(MAX(cache_write_reported),0) AS INTEGER) write_reported FROM provider_requests WHERE session_id=? AND run_id=? AND status='completed';
-- name: AggregateMainUsageByEpoch :one
SELECT CAST(COALESCE(SUM(input_tokens),0) AS INTEGER) raw_input,CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN input_tokens ELSE 0 END),0) AS INTEGER) reported_input,CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN cached_tokens ELSE 0 END),0) AS INTEGER) cached,CAST(COALESCE(SUM(cache_write_tokens),0) AS INTEGER) cache_write,CAST(COALESCE(SUM(output_tokens),0) AS INTEGER) output,CAST(COALESCE(SUM(reasoning_tokens),0) AS INTEGER) reasoning,CAST(COALESCE(SUM(total_tokens),0) AS INTEGER) total,COUNT(*) requests,CAST(COALESCE(SUM(cache_reported),0) AS INTEGER) reported_requests,CAST(COALESCE(MAX(cache_reported),0) AS INTEGER) reported FROM provider_requests WHERE session_id=? AND status='completed' AND request_kind='main' AND cache_epoch=?;
-- name: AggregateMainUsageByRun :one
SELECT CAST(COALESCE(SUM(input_tokens),0) AS INTEGER) raw_input,CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN input_tokens ELSE 0 END),0) AS INTEGER) reported_input,CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN cached_tokens ELSE 0 END),0) AS INTEGER) cached,CAST(COALESCE(SUM(cache_write_tokens),0) AS INTEGER) cache_write,CAST(COALESCE(SUM(output_tokens),0) AS INTEGER) output,CAST(COALESCE(SUM(reasoning_tokens),0) AS INTEGER) reasoning,CAST(COALESCE(SUM(total_tokens),0) AS INTEGER) total,COUNT(*) requests,CAST(COALESCE(SUM(cache_reported),0) AS INTEGER) reported_requests,CAST(COALESCE(MAX(cache_reported),0) AS INTEGER) reported FROM provider_requests WHERE session_id=? AND status='completed' AND request_kind='main' AND run_id=?;
-- name: AggregateMainUsage :one
SELECT CAST(COALESCE(SUM(input_tokens),0) AS INTEGER) raw_input,CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN input_tokens ELSE 0 END),0) AS INTEGER) reported_input,CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN cached_tokens ELSE 0 END),0) AS INTEGER) cached,CAST(COALESCE(SUM(cache_write_tokens),0) AS INTEGER) cache_write,CAST(COALESCE(SUM(output_tokens),0) AS INTEGER) output,CAST(COALESCE(SUM(reasoning_tokens),0) AS INTEGER) reasoning,CAST(COALESCE(SUM(total_tokens),0) AS INTEGER) total,COUNT(*) requests,CAST(COALESCE(SUM(cache_reported),0) AS INTEGER) reported_requests,CAST(COALESCE(MAX(cache_reported),0) AS INTEGER) reported FROM provider_requests WHERE session_id=? AND status='completed' AND request_kind='main';
-- name: AggregateCompactionUsage :one
SELECT CAST(COALESCE(SUM(input_tokens),0) AS INTEGER) raw_input,CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN input_tokens ELSE 0 END),0) AS INTEGER) reported_input,CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN cached_tokens ELSE 0 END),0) AS INTEGER) cached,CAST(COALESCE(SUM(cache_write_tokens),0) AS INTEGER) cache_write,CAST(COALESCE(SUM(output_tokens),0) AS INTEGER) output,CAST(COALESCE(SUM(reasoning_tokens),0) AS INTEGER) reasoning,CAST(COALESCE(SUM(total_tokens),0) AS INTEGER) total,COUNT(*) requests,CAST(COALESCE(SUM(cache_reported),0) AS INTEGER) reported_requests,CAST(COALESCE(MAX(cache_reported),0) AS INTEGER) reported FROM provider_requests WHERE session_id=? AND run_id=? AND status='completed' AND request_kind='compaction';
-- name: AggregateTeamUsage :one
SELECT CAST(COALESCE(SUM(input_tokens),0) AS INTEGER) raw_input,CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN input_tokens ELSE 0 END),0) AS INTEGER) reported_input,CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN cached_tokens ELSE 0 END),0) AS INTEGER) cached,CAST(COALESCE(SUM(cache_write_tokens),0) AS INTEGER) cache_write,CAST(COALESCE(SUM(output_tokens),0) AS INTEGER) output,CAST(COALESCE(SUM(reasoning_tokens),0) AS INTEGER) reasoning,CAST(COALESCE(SUM(total_tokens),0) AS INTEGER) total,COUNT(*) requests,CAST(COALESCE(SUM(cache_reported),0) AS INTEGER) reported_requests,CAST(COALESCE(MAX(cache_reported),0) AS INTEGER) reported FROM provider_requests WHERE session_id=? AND run_id=? AND status='completed' AND request_kind='team';
-- name: AggregateSubagentUsage :one
SELECT CAST(COALESCE(SUM(input_tokens),0) AS INTEGER) raw_input,CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN input_tokens ELSE 0 END),0) AS INTEGER) reported_input,CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN cached_tokens ELSE 0 END),0) AS INTEGER) cached,CAST(COALESCE(SUM(cache_write_tokens),0) AS INTEGER) cache_write,CAST(COALESCE(SUM(output_tokens),0) AS INTEGER) output,CAST(COALESCE(SUM(reasoning_tokens),0) AS INTEGER) reasoning,CAST(COALESCE(SUM(total_tokens),0) AS INTEGER) total,COUNT(*) requests,CAST(COALESCE(SUM(cache_reported),0) AS INTEGER) reported_requests,CAST(COALESCE(MAX(cache_reported),0) AS INTEGER) reported FROM provider_requests WHERE session_id=? AND run_id=? AND status='completed' AND request_kind='subagent';
-- name: LatestMainUsage :one
SELECT input_tokens,cached_tokens,output_tokens,reasoning_tokens,cache_reported FROM provider_requests WHERE session_id=? AND run_id=? AND request_kind='main' AND status='completed' ORDER BY completed_at DESC,started_at DESC,request_id DESC LIMIT 1;
-- name: ProviderRunTotalTokens :one
SELECT CAST(COALESCE(SUM(total_tokens),0) AS INTEGER) FROM provider_requests WHERE session_id=? AND run_id=? AND status='completed';
-- name: CountUnknownProviderRequests :one
SELECT COUNT(*) FROM provider_requests WHERE session_id=? AND run_id=? AND status='unknown';
-- name: CountUncheckpointedCompletions :one
SELECT COUNT(*) FROM provider_requests WHERE session_id=? AND run_id=? AND status='completed' AND checkpoint_generation>=?;
-- name: GetCacheProjection :one
SELECT cache_epoch,checkpoint_generation,cache_identity_hash FROM session_projections WHERE session_id=?;
-- name: InitializeCacheIdentity :exec
UPDATE session_projections SET cache_identity_hash=? WHERE session_id=?;
-- name: ChangeCacheIdentity :exec
UPDATE session_projections SET cache_epoch=?,cache_identity_hash=? WHERE session_id=?;
-- name: AdvanceCacheEpochCAS :execresult
UPDATE session_projections SET cache_epoch=cache_epoch+1,cache_identity_hash=? WHERE session_id=? AND cache_epoch=?;
-- name: GetCacheEpoch :one
SELECT cache_epoch FROM session_projections WHERE session_id=?;

-- name: CreateSubagentRun :exec
INSERT INTO subagent_runs(id,session_id,parent_run_id,parent_agent_id,tool_call_id,child_run_id,description,subagent_type,state,summary,provider,model,reasoning,capability_mode,requested_isolation,isolation,cwd,background,output,error,warning,transcript,tool_calls,turns,tokens_used,tools_used,worktree_path,completion_delivered,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?);
-- name: SaveSubagentRun :execresult
UPDATE subagent_runs SET session_id=?,parent_run_id=?,parent_agent_id=?,tool_call_id=?,child_run_id=?,description=?,subagent_type=?,state=?,summary=?,provider=?,model=?,reasoning=?,capability_mode=?,requested_isolation=?,isolation=?,cwd=?,background=?,output=?,error=?,warning=?,transcript=?,tool_calls=?,turns=?,tokens_used=?,tools_used=?,worktree_path=?,completion_delivered=?,started_at=?,finished_at=? WHERE id=?;
-- name: GetSubagentRun :one
SELECT * FROM subagent_runs WHERE id=?;
-- name: ListSubagentRuns :many
SELECT * FROM subagent_runs ORDER BY started_at,id;
-- name: ListSubagentRunsBySession :many
SELECT * FROM subagent_runs WHERE session_id=? ORDER BY started_at,id;
-- name: SetSubagentCompletionDelivered :execresult
UPDATE subagent_runs SET completion_delivered=? WHERE id=?;
-- name: InterruptIncompleteSubagents :execresult
UPDATE subagent_runs SET state='interrupted',summary='interrupted by process restart',error='interrupted by process restart',finished_at=? WHERE state IN ('initializing','queued','running','cancelling');

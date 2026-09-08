use super::*;
use serde_json::json;

impl AppState {
    pub(super) fn apply_projection_snapshot(&mut self, snapshot: &Value) {
        self.sequence = self
            .sequence
            .max(snapshot["wireSequence"].as_u64().unwrap_or(0));
        self.navigation.current_session_id = value_str(snapshot, "selectedSessionId");
        self.runtime.running = false;
        self.runtime.guidance_open = false;
        self.runtime.run_id = "".into();
        self.runtime.active_session_id = "".into();
        self.runtime.activity = "".into();
        for session in &mut self.navigation.sessions {
            session.running = false;
        }
        if let Some(projection) = snapshot.get("session").filter(|v| v.is_object()) {
            self.apply_session_projection(projection);
        }
        if let Some(runs) = snapshot["runs"].as_array() {
            for run in runs {
                self.apply_run_projection(run);
            }
        }
        if let Some(live) = snapshot["liveBlocks"].as_array() {
            let mut blocks = self.transcript.blocks.borrow_mut();
            for value in live {
                if value_str(value, "sessionId") != self.navigation.current_session_id
                    || !value_str(value, "agentId").is_empty()
                {
                    continue;
                }
                let Ok(mut block) = serde_json::from_value::<Block>(value.clone()) else {
                    continue;
                };
                if block.kind.as_ref() == "text" {
                    block.kind = "assistant".into();
                }
                if let Some(existing) = blocks.iter_mut().find(|item| {
                    item.id == block.id
                        || (!block.tool_call_id.is_empty()
                            && item.tool_call_id == block.tool_call_id)
                }) {
                    existing.state = block.state;
                    if !block.content.is_empty() {
                        existing.content = block.content;
                    }
                } else {
                    blocks.push(block);
                }
            }
        }
        self.transcript.rebuild_index();
        self.runtime.approvals.clear();
        self.runtime.questions.clear();
        self.runtime.plans.clear();
        if let Some(controls) = snapshot["controls"].as_array() {
            for control in controls {
                let (kind, key) = match control["kind"].as_str().unwrap_or_default() {
                    "approval" => ("approval_requested", "approvalId"),
                    "input" => ("user_input_requested", "userInputId"),
                    "plan" => ("plan_proposed", "planId"),
                    _ => continue,
                };
                let mut event = control.clone();
                event["kind"] = json!(kind);
                event[key] = control["id"].clone();
                self.apply_direct_event(event);
            }
        }
        self.runtime.prompt_queues.clear();
        if let Some(queues) = snapshot["promptQueues"].as_array() {
            for queue in queues {
                self.apply_prompt_queue(queue);
            }
        }
        self.runtime.context_profile = snapshot["contextProfile"].clone();
    }

    pub(super) fn apply_session_projection(&mut self, projection: &Value) {
        let session = &projection["session"];
        if value_str(session, "id").is_empty() {
            return;
        }
        let blocks = projection["blocks"].as_array().cloned().unwrap_or_default();
        // Reuse the existing durable tool ordering and control restoration.
        self.load_session(DesktopEvent {
            session_id: value_str(session, "id").to_string(),
            state: "refreshed".into(),
            data: HashMap::from([
                ("title".into(), value_str(session, "title").to_string()),
                (
                    "provider".into(),
                    value_str(session, "providerId").to_string(),
                ),
                ("model".into(), value_str(session, "modelId").to_string()),
                (
                    "reasoning".into(),
                    value_str(session, "reasoning").to_string(),
                ),
                (
                    "agentMode".into(),
                    value_str(session, "agentMode").to_string(),
                ),
                ("blocks".into(), json!(blocks).to_string()),
                (
                    "blockSequences".into(),
                    json!(
                        blocks
                            .iter()
                            .map(|b| b["sequence"].as_i64().unwrap_or(0))
                            .collect::<Vec<_>>()
                    )
                    .to_string(),
                ),
                ("toolRecords".into(), projection["toolRecords"].to_string()),
                ("usage".into(), projection["usage"].to_string()),
                ("active".into(), self.runtime.running.to_string()),
                ("activeRunID".into(), self.runtime.run_id.to_string()),
            ]),
            todo: projection["todo"].clone(),
            recap: projection["recap"].clone(),
            agent_snapshots: projection["agentSnapshots"]
                .as_array()
                .cloned()
                .unwrap_or_default(),
            ..Default::default()
        });
    }

    pub(super) fn apply_run_projection(&mut self, run: &Value) {
        let session = value_str(run, "sessionId");
        if session.is_empty() {
            return;
        }
        let terminal = [
            "completed",
            "failed",
            "cancelled",
            "reconcile_required",
            "blocked",
        ];
        let running = !terminal.contains(&run["state"].as_str().unwrap_or_default())
            && !terminal.contains(&run["bindingState"].as_str().unwrap_or_default())
            && run["activity"] != "idle";
        set_session_running(&mut self.navigation.sessions, &session, running);
        if running {
            self.runtime.active_session_id = session.clone();
        } else if self.runtime.active_session_id == session {
            self.runtime.active_session_id = "".into();
        }
        if session == self.navigation.current_session_id {
            self.runtime.running = running;
            self.runtime.guidance_open = running
                && run["allowedActions"]
                    .as_array()
                    .is_some_and(|actions| actions.iter().any(|action| action == "guide"));
            self.runtime.run_id = value_str(run, "runId");
            self.runtime.activity = if running {
                value_str(run, "activity")
            } else {
                "".into()
            };
        }
    }

    pub(super) fn apply_prompt_queue(&mut self, queue: &Value) {
        let session = value_str(queue, "sessionId").to_string();
        if session.is_empty() {
            return;
        }
        if self.runtime.prompt_queues.get(&session).is_some_and(|old| {
            old["revision"].as_i64().unwrap_or(0) > queue["revision"].as_i64().unwrap_or(0)
        }) {
            return;
        }
        self.runtime.prompt_queues.insert(session, queue.clone());
    }
}

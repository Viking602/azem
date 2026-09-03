use super::*;
pub(in crate::surfaces) fn tool_group_entry(
    index: usize,
    blocks: &[Block],
    style: (ThemePalette, Locale, bool, f32, i64),
    agents: &[serde_json::Value],
    expansion: Rc<RefCell<ProcessExpansion>>,
    owner: Entity<AzemWindow>,
) -> gpui::AnyElement {
    let (palette, locale, reduced_motion, horizontal_gutter, live_elapsed_ms) = style;
    let turn = turn_process_range(blocks, index).expect("tool belongs to a turn");
    let range = tool_group_range(blocks, &turn, index);
    let step_indexes = process_step_indexes(blocks, range.clone());
    if step_indexes
        .iter()
        .copied()
        .find(|candidate| is_process_tool_block(&blocks[*candidate]))
        != Some(index)
    {
        return div().h(px(0.)).into_any_element();
    }
    let key = tool_group_key(blocks, &range, index);
    let terminal = blocks
        .get(turn.end.saturating_sub(1))
        .filter(|block| is_turn_final_output(block));
    let group = &blocks[range.clone()];
    let running = terminal.is_none()
        && range.end == turn.process_end
        && turn.end == blocks.len()
        && (live_elapsed_ms > 0 || group.iter().any(is_active_process_block));
    let process_run_id = group
        .iter()
        .find_map(|block| (!block.run_id.is_empty()).then_some(block.run_id.as_ref()))
        .unwrap_or(key.as_str());
    if group.iter().any(|block| {
        is_process_tool_block(block) && tool_activity_kind(block) == ToolActivityKind::Subagent
    }) {
        let subagents = subagent_run_card(
            index,
            group,
            process_run_id,
            agents,
            (palette, locale),
            expansion,
            owner,
        );
        return div()
            .id(("timeline-block", index))
            .role(Role::Article)
            .aria_label(locale.text("ui.subagents"))
            .w_full()
            .min_w_0()
            .px(px(horizontal_gutter))
            .py(px(4.))
            .flex()
            .justify_center()
            .child(
                div()
                    .w_full()
                    .max_w(px(CHAT_COLUMN_MAX_WIDTH))
                    .child(subagents),
            )
            .into_any_element();
    }
    let summary = run_process_summary(group, terminal, running, live_elapsed_ms, locale);
    let transition = if running {
        expansion.borrow_mut().activity_transition(&key, &summary)
    } else {
        (None, 0)
    };
    let expanded = expansion.borrow().is_expanded(&key);
    let step_count = step_indexes.len();
    let body = div()
        .relative()
        .w_full()
        .max_w(px(CHAT_COLUMN_MAX_WIDTH))
        .pl(px(18.))
        .when(step_count > 1, |body| {
            body.child(
                div()
                    .absolute()
                    .left(px(8.))
                    .top(px(16.))
                    .bottom(px(16.))
                    .w(px(1.))
                    .bg(palette.border),
            )
        })
        .children(
            step_indexes
                .iter()
                .enumerate()
                .map(|(row_index, step_index)| {
                    process_step_row(
                        index,
                        row_index,
                        &blocks[*step_index],
                        group,
                        (palette, locale, reduced_motion),
                        expansion.clone(),
                        owner.clone(),
                    )
                }),
        );
    div()
        .id(("timeline-block", index))
        .role(Role::Article)
        .aria_label("tool")
        .w_full()
        .min_w_0()
        .px(px(horizontal_gutter))
        .py(px(4.))
        .flex()
        .flex_col()
        .items_center()
        .child(turn_status_header(
            index,
            summary,
            running,
            (palette, reduced_motion, transition),
            Some((key, expanded)),
            expansion.clone(),
            owner.clone(),
        ))
        .when(expanded, |entry| entry.child(body))
        .into_any_element()
}

pub(crate) fn needs_pending_process(blocks: &[Block], running: bool) -> bool {
    running
        && blocks
            .last()
            .is_some_and(|block| block.kind.as_ref() == "user")
}

pub(in crate::surfaces) fn pending_process_entry(
    index: usize,
    palette: ThemePalette,
    locale: Locale,
    reduced_motion: bool,
    horizontal_gutter: f32,
    live_elapsed_ms: i64,
) -> gpui::AnyElement {
    let summary = processing_status(live_elapsed_ms, locale);
    div()
        .id(("timeline-block", index))
        .role(Role::Article)
        .aria_label(summary.clone())
        .w_full()
        .px(px(horizontal_gutter))
        .py(px(8.))
        .flex()
        .justify_center()
        .child(
            div()
                .id(("process-group", index))
                .role(Role::Status)
                .aria_label(summary.clone())
                .w_full()
                .max_w(px(CHAT_COLUMN_MAX_WIDTH))
                .h(px(36.))
                .flex()
                .items_center()
                .child(animated_activity_label(
                    index,
                    summary,
                    palette,
                    reduced_motion,
                )),
        )
        .into_any_element()
}

pub(in crate::surfaces) fn thinking_process_entry(
    index: usize,
    block: &Block,
    palette: ThemePalette,
    locale: Locale,
    reduced_motion: bool,
    horizontal_gutter: f32,
    live_elapsed_ms: i64,
) -> gpui::AnyElement {
    let active = is_active_process_block(block);
    let elapsed_ms = process_number(block, "elapsedMs").unwrap_or(live_elapsed_ms);
    let label = if active {
        processing_status(elapsed_ms, locale)
    } else {
        locale.text("ui.thought").to_string()
    };
    let activity = if active {
        animated_activity_label(index, label.clone(), palette, reduced_motion)
    } else {
        div()
            .min_w_0()
            .truncate()
            .text_size(px(12.))
            .font_weight(gpui::FontWeight::MEDIUM)
            .text_color(palette.faint)
            .child(label.clone())
            .into_any_element()
    };
    div()
        .id(("timeline-block", index))
        .role(Role::Article)
        .aria_label(label)
        .w_full()
        .px(px(horizontal_gutter))
        .py(px(4.))
        .flex()
        .justify_center()
        .child(
            div()
                .w_full()
                .max_w(px(CHAT_COLUMN_MAX_WIDTH))
                .flex()
                .flex_col()
                .child(div().h(px(36.)).flex().items_center().child(activity)),
        )
        .into_any_element()
}

pub(in crate::surfaces) struct TurnProcessRange {
    start: usize,
    end: usize,
    process_end: usize,
}

pub(in crate::surfaces) fn turn_process_range(
    blocks: &[Block],
    index: usize,
) -> Option<TurnProcessRange> {
    blocks
        .get(index)
        .filter(|block| block.kind.as_ref() != "user")?;
    let start = blocks[..index]
        .iter()
        .rposition(|block| block.kind.as_ref() == "user")
        .map_or(0, |user| user + 1);
    let end = blocks[index..]
        .iter()
        .position(|block| block.kind.as_ref() == "user")
        .map_or(blocks.len(), |offset| index + offset);
    let terminal = blocks
        .get(end.saturating_sub(1))
        .filter(|block| is_turn_final_output(block));
    let process_end = end - usize::from(terminal.is_some());
    Some(TurnProcessRange {
        start,
        end,
        process_end,
    })
}

pub(in crate::surfaces) fn tool_group_range(
    blocks: &[Block],
    turn: &TurnProcessRange,
    index: usize,
) -> std::ops::Range<usize> {
    let start = (turn.start..index)
        .rev()
        .find(|candidate| is_tool_group_boundary(&blocks[*candidate]))
        .map_or(turn.start, |boundary| boundary + 1);
    let end = (index + 1..turn.process_end)
        .find(|candidate| is_tool_group_boundary(&blocks[*candidate]))
        .unwrap_or(turn.process_end);
    start..end
}

pub(in crate::surfaces) fn thinking_belongs_to_tool_group(blocks: &[Block], index: usize) -> bool {
    let Some(turn) = turn_process_range(blocks, index) else {
        return false;
    };
    let start = (turn.start..index)
        .rev()
        .find(|candidate| is_tool_group_boundary(&blocks[*candidate]))
        .map_or(turn.start, |boundary| boundary + 1);
    let end = (index + 1..turn.process_end)
        .find(|candidate| is_tool_group_boundary(&blocks[*candidate]))
        .unwrap_or(turn.process_end);
    blocks[start..end].iter().any(is_process_tool_block)
}

fn is_tool_group_boundary(block: &Block) -> bool {
    !is_process_tool_block(block) && !is_hidden_process_block(block)
}

pub(in crate::surfaces) fn tool_group_key(
    blocks: &[Block],
    range: &std::ops::Range<usize>,
    index: usize,
) -> String {
    blocks[range.clone()]
        .iter()
        .find_map(|block| (!block.run_id.is_empty()).then_some(block.run_id.as_ref()))
        .map_or_else(
            || format!("tool-group:{index}"),
            |run_id| format!("tool-group:{run_id}:{index}"),
        )
}

pub(in crate::surfaces) fn process_step_indexes(
    blocks: &[Block],
    range: std::ops::Range<usize>,
) -> Vec<usize> {
    let mut indexes = Vec::with_capacity(range.len());
    indexes.extend(
        range
            .clone()
            .filter(|index| is_thinking_text(&blocks[*index])),
    );

    let mut seen_tool_calls = HashSet::new();
    indexes.extend(range.filter(|index| {
        let block = &blocks[*index];
        if !is_process_tool_block(block) {
            return false;
        }
        let tool_call_id = block.tool_call_id.as_ref();
        tool_call_id.is_empty() || seen_tool_calls.insert(tool_call_id)
    }));
    indexes
}

fn turn_status_header(
    index: usize,
    summary: String,
    running: bool,
    style: (ThemePalette, bool, (Option<String>, usize)),
    toggle: Option<(String, bool)>,
    expansion: Rc<RefCell<ProcessExpansion>>,
    owner: Entity<AzemWindow>,
) -> gpui::AnyElement {
    let header = div()
        .id(("turn-status", index))
        .aria_label(summary.clone())
        .w_full()
        .max_w(px(CHAT_COLUMN_MAX_WIDTH))
        .child(turn_status_row(
            index,
            summary,
            running,
            toggle.as_ref().map(|(_, expanded)| *expanded),
            style,
        ));
    let Some((toggle_key, expanded)) = toggle else {
        return header.role(Role::Status).into_any_element();
    };
    header
        .role(Role::Button)
        .aria_expanded(expanded)
        .tab_stop(true)
        .cursor_pointer()
        .on_click(move |_, _, cx| {
            expansion.borrow_mut().toggle(&toggle_key);
            owner.update(cx, |this, cx| {
                this.refresh_transcript_layout(index);
                cx.notify();
            });
        })
        .into_any_element()
}

fn turn_status_row(
    index: usize,
    summary: String,
    running: bool,
    expanded: Option<bool>,
    style: (ThemePalette, bool, (Option<String>, usize)),
) -> gpui::AnyElement {
    let (palette, reduced_motion, transition) = style;
    let label = if running {
        rolling_activity_label(index, summary, transition, palette, reduced_motion)
    } else {
        div()
            .min_w_0()
            .truncate()
            .text_size(px(12.))
            .font_weight(gpui::FontWeight::MEDIUM)
            .text_color(palette.faint)
            .child(summary)
            .into_any_element()
    };
    div()
        .min_w_0()
        .h(px(36.))
        .flex()
        .items_center()
        .gap_1()
        .child(div().min_w_0().flex().items_center().child(label))
        .children(expanded.map(|expanded| {
            div().flex_shrink_0().child(icon(
                if expanded {
                    "chevron-down"
                } else {
                    "chevron-right"
                },
                13.,
                palette.faint,
            ))
        }))
        .into_any_element()
}

pub(in crate::surfaces) fn is_hidden_process_block(block: &Block) -> bool {
    is_thinking_text(block)
        || is_host_tool_announcement(block)
        || (block.kind.as_ref() == "status" && block.title.as_ref() == "run_cancelled")
}

fn is_turn_final_output(block: &Block) -> bool {
    block.kind.as_ref() == "error"
        || (block.kind.as_ref() == "assistant" && block.text_phase.as_ref() != "commentary")
}

pub(in crate::surfaces) fn is_thinking_text(block: &Block) -> bool {
    block.kind.as_ref() == "thinking"
}

fn is_active_process_block(block: &Block) -> bool {
    is_active_process_state(block.state.as_ref())
}

fn is_active_process_state(state: &str) -> bool {
    matches!(
        state,
        "running"
            | "queued"
            | "pending"
            | "streaming"
            | "started"
            | "arguments"
            | "progress"
            | "awaiting_approval"
            | "reviewing_approval"
    )
}

pub(in crate::surfaces) fn running_tool_summary(block: &Block, locale: Locale) -> String {
    let (action, _) = tool_action(tool_name(block), locale);
    let preview = truncate_label(&tool_preview(block), 42);
    if preview.is_empty() {
        action.to_string()
    } else {
        format!("{action} {preview}")
    }
}

fn animated_activity_label(
    animation_id: usize,
    label: String,
    palette: ThemePalette,
    reduced_motion: bool,
) -> gpui::AnyElement {
    let row = div()
        .min_w_0()
        .h(px(17.))
        .overflow_hidden()
        .flex()
        .items_center()
        .text_size(px(12.))
        .font_weight(gpui::FontWeight::MEDIUM)
        .text_color(palette.faint)
        .child(label);
    if reduced_motion {
        row.into_any_element()
    } else {
        row.with_animation(
            ("activity-pulse", animation_id),
            Animation::new(Duration::from_millis(1_500)).repeat(),
            |row, delta| {
                let peak = 1. - (2. * delta.clamp(0., 1.) - 1.).abs();
                row.opacity(0.72 + 0.28 * peak)
            },
        )
        .into_any_element()
    }
}

fn rolling_activity_label(
    animation_id: usize,
    label: String,
    transition: (Option<String>, usize),
    palette: ThemePalette,
    reduced_motion: bool,
) -> gpui::AnyElement {
    let (previous, revision) = transition;
    let Some(previous) = previous.filter(|_| !reduced_motion) else {
        return animated_activity_label(animation_id, label, palette, reduced_motion);
    };
    let transition_id = animation_id.wrapping_mul(1_000_003).wrapping_add(revision);
    let previous = div()
        .absolute()
        .left_0()
        .top_0()
        .h(px(17.))
        .flex()
        .items_center()
        .child(previous)
        .with_animation(
            ("activity-roll-out", transition_id),
            Animation::new(crate::PROCESS_ACTIVITY_ROLL_DURATION)
                .with_easing(gpui::ease_out_quint()),
            |row, progress| row.top(px(-17. * progress)).opacity(1. - progress),
        );
    let current = div()
        .absolute()
        .left_0()
        .top_0()
        .child(animated_activity_label(
            transition_id,
            label.clone(),
            palette,
            false,
        ))
        .with_animation(
            ("activity-roll-in", transition_id),
            Animation::new(crate::PROCESS_ACTIVITY_ROLL_DURATION)
                .with_easing(gpui::ease_out_quint()),
            |row, progress| row.top(px(17. * (1. - progress))).opacity(progress),
        );
    div()
        .relative()
        .min_w_0()
        .h(px(17.))
        .overflow_hidden()
        .text_size(px(12.))
        .font_weight(gpui::FontWeight::MEDIUM)
        .text_color(palette.faint)
        .child(div().opacity(0.).child(label))
        .child(previous)
        .child(current)
        .into_any_element()
}

pub(in crate::surfaces) fn is_host_tool_announcement(block: &Block) -> bool {
    block_data_value(block, "synthetic").and_then(serde_json::Value::as_str)
        == Some("tool_announcement")
        || block.content.trim() == "正在调用所需工具，并根据实际结果继续。"
}

pub(in crate::surfaces) fn run_process_summary(
    group: &[Block],
    terminal: Option<&Block>,
    running: bool,
    live_elapsed_ms: i64,
    locale: Locale,
) -> String {
    if running {
        return latest_process_activity(group, locale)
            .unwrap_or_else(|| processing_status(live_elapsed_ms, locale));
    }
    let cancelled = group.iter().chain(terminal).any(|block| {
        block.kind.as_ref() == "status"
            && block.title.as_ref() == "run_cancelled"
            && block.state.as_ref() == "cancelled"
    });
    let run_elapsed_ms = group
        .iter()
        .chain(terminal)
        .find_map(|block| process_number(block, "runElapsedMs"));
    let started_at = group
        .iter()
        .chain(terminal)
        .filter_map(|block| process_number(block, "startedAt"))
        .min();
    let completed_at = group
        .iter()
        .chain(terminal)
        .filter_map(|block| process_number(block, "completedAt"))
        .max();
    let elapsed_ms = run_elapsed_ms.unwrap_or_else(|| {
        terminal
            .and_then(|block| process_number(block, "elapsedMs"))
            .or_else(|| {
                started_at
                    .zip(completed_at)
                    .and_then(|(started, completed)| {
                        (completed >= started).then_some(completed - started)
                    })
            })
            .filter(|elapsed| *elapsed > 0)
            .unwrap_or_else(|| {
                group
                    .iter()
                    .filter_map(|block| process_number(block, "elapsedMs"))
                    .sum()
            })
    });
    let seconds = (elapsed_ms / 1000).max(0);
    let minutes = seconds / 60;
    let remainder = seconds % 60;
    if cancelled {
        return locale.format(
            if minutes > 0 {
                "duration.stoppedMinutes"
            } else {
                "duration.stoppedSeconds"
            },
            &[
                ("minutes", minutes.to_string()),
                ("seconds", remainder.to_string()),
            ],
        );
    }
    if let Some(summary) = completed_tool_group_summary(group, locale) {
        return summary;
    }
    if seconds == 0 {
        return locale.text("ui.worked").to_string();
    }
    locale.format(
        if minutes > 0 {
            "duration.processedMinutes"
        } else {
            "duration.processedSeconds"
        },
        &[
            ("minutes", minutes.to_string()),
            ("seconds", remainder.to_string()),
        ],
    )
}

pub(in crate::surfaces) fn processing_status(elapsed_ms: i64, locale: Locale) -> String {
    let seconds = (elapsed_ms / 1000).max(0);
    let minutes = seconds / 60;
    let remainder = seconds % 60;
    locale.format(
        if minutes > 0 {
            "duration.processingMinutes"
        } else {
            "duration.processingSeconds"
        },
        &[
            ("minutes", minutes.to_string()),
            ("seconds", remainder.to_string()),
        ],
    )
}

#[derive(Default)]
struct ProcessActivityCounts {
    read: usize,
    edit: usize,
    command: usize,
    search: usize,
    web_search: usize,
    fetch: usize,
    subagent: usize,
    browser: usize,
    plan: usize,
    other: usize,
}

fn process_activity_counts(group: &[Block]) -> ProcessActivityCounts {
    let mut counts = ProcessActivityCounts::default();
    let mut seen_tool_calls = HashSet::new();
    for block in group {
        if is_thinking_text(block) {
            continue;
        }
        if !is_process_tool_block(block) {
            continue;
        }
        let tool_call_id = block.tool_call_id.as_ref();
        if !tool_call_id.is_empty() && !seen_tool_calls.insert(tool_call_id) {
            continue;
        }
        match tool_activity_kind(block) {
            ToolActivityKind::Read => counts.read += 1,
            ToolActivityKind::Edit => counts.edit += 1,
            ToolActivityKind::Command | ToolActivityKind::Test => counts.command += 1,
            ToolActivityKind::Search => counts.search += 1,
            ToolActivityKind::WebSearch => counts.web_search += 1,
            ToolActivityKind::Fetch => counts.fetch += 1,
            ToolActivityKind::Subagent => counts.subagent += 1,
            ToolActivityKind::Browser => counts.browser += 1,
            ToolActivityKind::Plan => counts.plan += 1,
            ToolActivityKind::Other => counts.other += 1,
        }
    }
    counts
}

pub(in crate::surfaces) fn completed_tool_group_summary(
    group: &[Block],
    locale: Locale,
) -> Option<String> {
    let counts = process_activity_counts(group);
    let mut tools = Vec::with_capacity(10);
    for (count, key) in [
        (counts.read, "tools.readCount"),
        (counts.edit, "tools.editCount"),
        (counts.command, "tools.commandCount"),
        (counts.search, "tools.searchCount"),
        (counts.web_search, "tools.webSearchCount"),
        (counts.fetch, "tools.fetchCount"),
        (counts.subagent, "tools.subagentCount"),
        (counts.browser, "tools.browserCount"),
        (counts.plan, "tools.planCount"),
        (counts.other, "tools.otherCount"),
    ] {
        if count > 0 {
            tools.push(locale.format(key, &[("count", count.to_string())]));
        }
    }
    let separator = if locale.id().starts_with("zh") {
        "、"
    } else {
        ", "
    };
    (!tools.is_empty()).then(|| tools.join(separator))
}

fn latest_process_activity(group: &[Block], locale: Locale) -> Option<String> {
    group
        .iter()
        .rev()
        .find(|block| is_process_tool_block(block))
        .map(|block| process_step_label(block, locale))
}

pub(in crate::surfaces) fn is_agent_block(block: &Block) -> bool {
    block.kind.as_ref() == "agent"
}

#[derive(Clone)]
struct SubagentCardItem {
    id: String,
    role: String,
    detail: String,
    state: String,
    elapsed_ms: i64,
}

pub(in crate::surfaces) fn agent_belongs_to_run(agent: &serde_json::Value, run_id: &str) -> bool {
    ["parentRunId", "parentRunID", "parent_run_id"]
        .into_iter()
        .any(|key| agent.get(key).and_then(serde_json::Value::as_str) == Some(run_id))
}

fn block_agent_id(block: &Block) -> &str {
    block
        .extra
        .get("agentId")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
}

fn is_terminal_agent_state(state: &str) -> bool {
    matches!(
        state,
        "completed" | "failed" | "cancelled" | "canceled" | "interrupted"
    )
}

pub(in crate::surfaces) fn resolved_agent_state(
    block: Option<&Block>,
    snapshot_state: &str,
) -> String {
    let block_state = block.map_or("", |block| block.state.as_ref());
    if is_terminal_agent_state(block_state) {
        block_state.to_string()
    } else if !snapshot_state.is_empty() {
        snapshot_state.to_string()
    } else if !block_state.is_empty() {
        block_state.to_string()
    } else {
        "idle".to_string()
    }
}

pub(in crate::surfaces) fn agent_matches_group(
    agent: &serde_json::Value,
    group: &[Block],
    process_key: &str,
) -> bool {
    let snapshot_id = ["id", "agentId"]
        .into_iter()
        .find_map(|key| agent.get(key).and_then(serde_json::Value::as_str))
        .unwrap_or_default();
    let snapshot_parent_call = ["parentToolCallId", "parent_tool_call_id"]
        .into_iter()
        .find_map(|key| agent.get(key).and_then(serde_json::Value::as_str))
        .unwrap_or_default();
    let mut group_has_ids = false;
    let matches_group_id = group.iter().any(|block| {
        let block_id = block_agent_id(block);
        group_has_ids |= !block_id.is_empty();
        !block_id.is_empty() && block_id == snapshot_id
    });
    if group_has_ids {
        return matches_group_id;
    }
    let mut group_has_tool_call_ids = false;
    let matches_parent_call = group.iter().any(|block| {
        let call_id = block
            .extra
            .get("toolCallId")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default();
        group_has_tool_call_ids |= !call_id.is_empty();
        !call_id.is_empty() && call_id == snapshot_parent_call
    });
    if group_has_tool_call_ids {
        matches_parent_call
    } else {
        agent_belongs_to_run(agent, process_key)
    }
}

pub(in crate::surfaces) fn subagent_run_card(
    group_index: usize,
    group: &[Block],
    process_key: &str,
    agents: &[serde_json::Value],
    style: (ThemePalette, Locale),
    expansion: Rc<RefCell<ProcessExpansion>>,
    owner: Entity<AzemWindow>,
) -> gpui::AnyElement {
    let (palette, locale) = style;
    let snapshots = agents
        .iter()
        .filter(|agent| agent_matches_group(agent, group, process_key))
        .collect::<Vec<_>>();
    let items = if snapshots.is_empty() {
        group
            .iter()
            .filter(|block| is_agent_block(block))
            .map(|block| {
                let id = block_agent_id(block).to_string();
                let agent = agents
                    .iter()
                    .find(|agent| agent.get("id").and_then(serde_json::Value::as_str) == Some(&id));
                let field = |key| {
                    agent
                        .and_then(|agent| agent.get(key))
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or_default()
                        .trim()
                };
                let role = [field("type"), block.title.as_ref(), id.as_str()]
                    .into_iter()
                    .find(|value| !value.is_empty())
                    .unwrap_or(locale.text("ui.subagent"))
                    .to_string();
                let state = resolved_agent_state(Some(block), field("state"));
                let detail = if state == "failed" {
                    [field("error"), field("summary"), field("description")]
                } else {
                    [field("description"), field("summary"), block.content.trim()]
                }
                .into_iter()
                .find(|value| !value.is_empty())
                .unwrap_or_default()
                .to_string();
                let elapsed_ms = agent
                    .and_then(|agent| agent.get("elapsedMs"))
                    .and_then(json_integer)
                    .or_else(|| process_number(block, "elapsedMs"))
                    .unwrap_or_default();
                SubagentCardItem {
                    id,
                    role: truncate_label(&role, 28),
                    detail: truncate_label(&detail, 72),
                    state,
                    elapsed_ms,
                }
            })
            .collect::<Vec<_>>()
    } else {
        snapshots
            .into_iter()
            .map(|agent| {
                let field = |key| {
                    agent
                        .get(key)
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or_default()
                        .trim()
                };
                let id = [field("id"), field("agentId")]
                    .into_iter()
                    .find(|value| !value.is_empty())
                    .unwrap_or_default();
                let role = [field("type"), id]
                    .into_iter()
                    .find(|value| !value.is_empty())
                    .unwrap_or(locale.text("ui.subagent"));
                let block = group.iter().find(|block| block_agent_id(block) == id);
                let state = resolved_agent_state(block, field("state"));
                let detail = if state == "failed" {
                    [field("error"), field("summary"), field("description")]
                } else {
                    [field("description"), field("summary"), field("activity")]
                }
                .into_iter()
                .find(|value| !value.is_empty())
                .unwrap_or_default();
                SubagentCardItem {
                    id: id.to_string(),
                    role: truncate_label(role, 28),
                    detail: truncate_label(detail, 72),
                    state,
                    elapsed_ms: agent
                        .get("elapsedMs")
                        .and_then(json_integer)
                        .or_else(|| block.and_then(|block| process_number(block, "elapsedMs")))
                        .unwrap_or_default(),
                }
            })
            .collect::<Vec<_>>()
    };
    if items.is_empty() {
        return div().h(px(0.)).into_any_element();
    }
    let total = items.len();
    let active = items
        .iter()
        .filter(|item| is_open_agent_state(&item.state))
        .count();
    let failed = items.iter().filter(|item| item.state == "failed").count();
    let completed = items
        .iter()
        .filter(|item| item.state == "completed")
        .count();
    let cancelled = total.saturating_sub(active + failed + completed);
    let tag_key = format!("subagents:{process_key}:{group_index}");
    let expanded = expansion.borrow().is_expanded(&tag_key);
    let counted = |count: usize, label: &str| {
        if locale.id().starts_with("zh") {
            format!("{label} {count}")
        } else {
            format!("{count} {label}")
        }
    };
    let mut states = Vec::with_capacity(4);
    if active > 0 {
        states.push(counted(active, locale.text("ui.running2")));
    }
    if completed > 0 {
        states.push(counted(completed, locale.text("ui.completed")));
    }
    if failed > 0 {
        states.push(counted(failed, locale.text("ui.failed2")));
    }
    if cancelled > 0 {
        states.push(counted(cancelled, locale.text("ui.cancelled")));
    }
    let title = if locale.id().starts_with("zh") {
        format!("{} · {total} 个", locale.text("ui.subagents"))
    } else {
        format!("{} · {total}", locale.text("ui.subagents"))
    };
    let summary = states.join(" · ");
    let aria_label = format!("{title} · {summary}");
    let status_color = if active > 0 {
        palette.accent
    } else if failed > 0 {
        palette.danger
    } else {
        palette.positive
    };
    let rows = items
        .into_iter()
        .enumerate()
        .map(|(index, item)| {
            let target = item.id.clone();
            let click_owner = owner.clone();
            let state_label = subagent_status_label(&item.state, locale).to_string();
            let detail = if item.detail.is_empty() {
                state_label.clone()
            } else {
                format!("{state_label}: {}", item.detail)
            };
            let aria_label = format!("{}, {detail}", item.role);
            let state_color = if item.state == "failed" {
                palette.danger
            } else if is_active_agent_state(&item.state) {
                palette.ink_soft
            } else {
                palette.muted
            };
            div()
                .id(("subagent-run-row", group_index * 100 + index))
                .role(Role::Button)
                .aria_label(aria_label)
                .tab_stop(true)
                .h(px(34.))
                .min_w_0()
                .rounded(px(6.))
                .px_2()
                .flex()
                .items_center()
                .gap_2()
                .cursor_pointer()
                .hover(move |row| row.bg(palette.hover))
                .on_click(move |_, window, cx| {
                    let target = target.clone();
                    click_owner.update(cx, |this, cx| {
                        this.inspect_agent(target, window, cx);
                    });
                })
                .child(icon("bot", 14., palette.faint))
                .child(
                    div()
                        .flex_shrink_0()
                        .px_1()
                        .py(px(2.))
                        .rounded(px(5.))
                        .bg(palette.paper_muted)
                        .text_size(px(12.))
                        .font_weight(gpui::FontWeight::MEDIUM)
                        .text_color(palette.ink_soft)
                        .child(item.role),
                )
                .child(
                    div()
                        .min_w_0()
                        .flex_1()
                        .truncate()
                        .text_size(px(12.))
                        .text_color(state_color)
                        .child(detail),
                )
                .when(item.elapsed_ms > 0, |row| {
                    row.child(
                        div()
                            .flex_shrink_0()
                            .text_size(px(12.))
                            .text_color(palette.faint)
                            .child(format!(
                                "· {}",
                                format_usage_duration(item.elapsed_ms, locale)
                            )),
                    )
                })
                .child(
                    div()
                        .flex_shrink_0()
                        .child(icon("chevron-right", 13., palette.faint)),
                )
        })
        .collect::<Vec<_>>();
    let click_expansion = expansion;
    let click_key = tag_key;
    let click_owner = owner;
    div()
        .id(("subagent-run-card", group_index))
        .role(Role::Group)
        .aria_label(aria_label.clone())
        .w_full()
        .max_w(px(640.))
        .flex()
        .flex_col()
        .items_start()
        .child(
            div()
                .id(("subagent-run-tag", group_index))
                .role(Role::Button)
                .aria_label(aria_label)
                .aria_expanded(expanded)
                .tab_stop(true)
                .h(px(36.))
                .max_w(px(640.))
                .px_2()
                .rounded(px(7.))
                .flex()
                .items_center()
                .gap_2()
                .cursor_pointer()
                .hover(move |tag| tag.bg(palette.hover))
                .on_click(move |_, _, cx| {
                    click_expansion.borrow_mut().toggle(&click_key);
                    click_owner.update(cx, |this, cx| {
                        this.refresh_transcript_layout(group_index);
                        cx.notify();
                    });
                })
                .child(icon("bot", 14., status_color))
                .child(
                    div()
                        .flex_shrink_0()
                        .text_size(px(12.))
                        .font_weight(gpui::FontWeight::MEDIUM)
                        .text_color(palette.ink_soft)
                        .child(title),
                )
                .when(!summary.is_empty(), |tag| {
                    tag.child(
                        div()
                            .min_w_0()
                            .truncate()
                            .text_size(px(12.))
                            .text_color(if failed > 0 {
                                palette.danger
                            } else {
                                palette.faint
                            })
                            .child(summary),
                    )
                })
                .child(div().flex_shrink_0().child(icon(
                    if expanded {
                        "chevron-down"
                    } else {
                        "chevron-right"
                    },
                    12.,
                    palette.faint,
                ))),
        )
        .when(expanded, |card| {
            card.child(
                div()
                    .id(("subagent-list", group_index))
                    .w_full()
                    .max_w(px(640.))
                    .ml(px(9.))
                    .pl(px(8.))
                    .border_l_1()
                    .border_color(palette.border)
                    .children(rows),
            )
        })
        .into_any_element()
}

pub(in crate::surfaces) fn is_active_agent_state(state: &str) -> bool {
    matches!(state, "initializing" | "started" | "running" | "cancelling")
}

pub(in crate::surfaces) fn is_open_agent_state(state: &str) -> bool {
    is_active_agent_state(state) || matches!(state, "queued" | "pending")
}

pub(in crate::surfaces) fn subagent_status_label(state: &str, locale: Locale) -> &'static str {
    match state {
        "initializing" => locale.text("ui.starting"),
        "started" | "running" => locale.text("ui.running2"),
        "cancelling" => locale.text("ui.stopping"),
        "completed" => locale.text("ui.completed"),
        "failed" => locale.text("ui.failed2"),
        "cancelled" | "canceled" => locale.text("ui.cancelled"),
        "interrupted" => locale.text("ui.interrupted"),
        "queued" => locale.text("ui.queued"),
        _ => locale.text("ui.idle"),
    }
}

pub(in crate::surfaces) fn is_process_tool_block(block: &Block) -> bool {
    matches!(block.kind.as_ref(), "tool" | "diff") && !is_host_tool_announcement(block)
}

pub(in crate::surfaces) fn process_step_row(
    group_index: usize,
    row_index: usize,
    block: &Block,
    group: &[Block],
    style: (ThemePalette, Locale, bool),
    expansion: Rc<RefCell<ProcessExpansion>>,
    owner: Entity<AzemWindow>,
) -> gpui::AnyElement {
    let (palette, locale, reduced_motion) = style;
    if is_thinking_text(block) {
        return process_detail_row(
            group_index,
            row_index,
            block,
            palette,
            locale,
            reduced_motion,
        );
    }
    let row_id = group_index * 1000 + row_index;
    let active = is_active_process_block(block);
    let failed = block.state.as_ref() == "failed";
    let detail = tool_step_detail(block, group).or_else(|| {
        failed.then(|| ToolStepDetail {
            content: locale.text("ui.noFailureDetails").to_string(),
            is_diff: false,
            is_code: false,
        })
    });
    let can_expand = detail.is_some();
    let detail_identity = if !block.tool_call_id.is_empty() {
        block.tool_call_id.to_string()
    } else if !block.id.is_empty() {
        block.id.to_string()
    } else {
        format!("row-{row_id}")
    };
    let detail_key = format!("tool-detail:{group_index}:{detail_identity}");
    let expanded = can_expand && expansion.borrow().is_expanded(&detail_key);
    let (row_icon, action, target) = process_step_presentation(block, locale);
    let label = if target.is_empty() {
        action.clone()
    } else {
        format!("{action} {target}")
    };
    let state_label = if active {
        locale.text("ui.running2")
    } else if failed {
        locale.text("ui.failed2")
    } else {
        locale.text("ui.completed")
    };
    let aria_label = format!("{label}, {state_label}");
    let has_target = !target.is_empty();
    let color = if failed {
        palette.danger
    } else if active {
        palette.ink_soft
    } else {
        palette.muted
    };
    let click_expansion = expansion;
    let click_owner = owner;
    let click_key = detail_key;
    let row = div()
        .id(("process-step", row_id))
        .aria_label(aria_label)
        .h(px(32.))
        .min_w_0()
        .flex()
        .items_center()
        .gap_2()
        .text_size(px(12.))
        .when(can_expand, |row| {
            row.role(Role::Button)
                .aria_expanded(expanded)
                .tab_stop(true)
                .cursor_pointer()
                .on_click(move |_, _, cx| {
                    click_expansion.borrow_mut().toggle(&click_key);
                    click_owner.update(cx, |this, cx| {
                        this.refresh_transcript_layout(group_index);
                        cx.notify();
                    });
                })
        })
        .when(!can_expand, |row| row.role(Role::Status))
        .child(icon(
            row_icon,
            14.,
            if failed {
                palette.danger
            } else {
                palette.faint
            },
        ))
        .child(
            div()
                .min_w_0()
                .flex_1()
                .flex()
                .items_center()
                .gap_1()
                .child(
                    div()
                        .flex_shrink_0()
                        .font_weight(gpui::FontWeight::MEDIUM)
                        .text_color(color)
                        .child(action),
                )
                .when(has_target, |detail| {
                    detail.child(
                        div()
                            .min_w_0()
                            .truncate()
                            .text_color(if failed {
                                palette.danger
                            } else {
                                palette.faint
                            })
                            .child(target),
                    )
                }),
        )
        .child(process_tool_state_mark(
            row_id,
            block.state.as_ref(),
            reduced_motion,
            palette,
        ))
        .when(can_expand, |row| {
            row.child(icon(
                if expanded {
                    "chevron-down"
                } else {
                    "chevron-right"
                },
                12.,
                palette.faint,
            ))
        });
    let row = if reduced_motion {
        row.into_any_element()
    } else {
        row.with_animation(
            ("process-step-enter", row_id),
            Animation::new(Duration::from_millis(160)).with_easing(gpui::ease_out_quint()),
            |row, progress| {
                row.relative()
                    .top(px(4. * (1. - progress)))
                    .opacity(progress)
            },
        )
        .into_any_element()
    };
    div()
        .w_full()
        .min_w_0()
        .flex()
        .flex_col()
        .child(row)
        .when_some(expanded.then_some(detail).flatten(), |entry, detail| {
            let content = if detail.is_diff {
                fenced_tool_detail(&detail.content, "diff")
            } else if detail.is_code {
                fenced_tool_detail(&detail.content, "text")
            } else {
                detail.content
            };
            entry.child(
                div()
                    .id(("process-tool-detail", row_id))
                    .pl(px(22.))
                    .pr_2()
                    .pb_2()
                    .text_color(if failed {
                        palette.danger
                    } else {
                        palette.ink_soft
                    })
                    .child(markdown_view(
                        row_id + 500_000,
                        &content,
                        false,
                        reduced_motion,
                        palette,
                    )),
            )
        })
        .into_any_element()
}

fn process_tool_state_mark(
    row_id: usize,
    state: &str,
    reduced_motion: bool,
    palette: ThemePalette,
) -> gpui::AnyElement {
    if is_active_process_state(state) {
        let spinner = icon("loader", 13., palette.accent);
        if reduced_motion {
            spinner.into_any_element()
        } else {
            spinner
                .with_animation(
                    ("process-tool-running", row_id),
                    Animation::new(Duration::from_millis(800)).repeat(),
                    |spinner, progress| {
                        spinner.with_transformation(Transformation::rotate(percentage(progress)))
                    },
                )
                .into_any_element()
        }
    } else {
        let (name, color) = match state {
            "failed" => ("shield-alert", palette.danger),
            "completed" | "complete" => ("check", palette.positive),
            _ => ("circle", palette.faint),
        };
        icon(name, 13., color).into_any_element()
    }
}

#[derive(Clone)]
pub(in crate::surfaces) struct ToolStepDetail {
    pub(in crate::surfaces) content: String,
    pub(in crate::surfaces) is_diff: bool,
    pub(in crate::surfaces) is_code: bool,
}

pub(in crate::surfaces) fn tool_step_detail(
    block: &Block,
    group: &[Block],
) -> Option<ToolStepDetail> {
    if block.state.as_ref() == "failed" {
        for key in ["error", "reason", "message", "output"] {
            if let Some(content) = tool_detail_field(block, key) {
                return Some(ToolStepDetail {
                    content,
                    is_diff: false,
                    is_code: is_code_output_tool(tool_name(block)),
                });
            }
        }
        if let Some(value) = structured_tool_value(block) {
            for key in ["error", "reason", "message", "output"] {
                if let Some(content) = value
                    .get(key)
                    .and_then(serde_json::Value::as_str)
                    .map(str::trim)
                    .filter(|content| !content.is_empty())
                {
                    return Some(ToolStepDetail {
                        content: content.to_string(),
                        is_diff: false,
                        is_code: is_code_output_tool(tool_name(block)),
                    });
                }
            }
        }
    }

    if is_edit_tool(block) || is_file_change(block) {
        let paired = group.iter().find(|candidate| {
            candidate.kind.as_ref() == "diff"
                && !block.tool_call_id.is_empty()
                && candidate.tool_call_id == block.tool_call_id
        });
        for candidate in std::iter::once(block).chain(paired) {
            if let Some(content) = structured_tool_diff(candidate) {
                return Some(ToolStepDetail {
                    content,
                    is_diff: true,
                    is_code: false,
                });
            }
            if candidate.kind.as_ref() == "diff" && !candidate.content.trim().is_empty() {
                return Some(ToolStepDetail {
                    content: candidate.content.trim().to_string(),
                    is_diff: true,
                    is_code: false,
                });
            }
        }
    }

    let content = block.content.trim();
    (!content.is_empty() && !is_bare_tool_status(block, content)).then(|| ToolStepDetail {
        content: content.to_string(),
        is_diff: false,
        is_code: is_code_output_tool(tool_name(block)),
    })
}

fn fenced_tool_detail(content: &str, language: &str) -> String {
    let mut current = 0;
    let mut longest = 0;
    for byte in content.bytes() {
        if byte == b'`' {
            current += 1;
            longest = longest.max(current);
        } else {
            current = 0;
        }
    }
    let fence = "`".repeat((longest + 1).max(3));
    format!("{fence}{language}\n{}\n{fence}", content.trim())
}

fn tool_detail_field(block: &Block, key: &str) -> Option<String> {
    block_data_value(block, key)
        .and_then(serde_json::Value::as_str)
        .map(str::trim)
        .filter(|content| !content.is_empty())
        .map(str::to_string)
}

fn structured_tool_value(block: &Block) -> Option<serde_json::Value> {
    match block_data_value(block, "structured")? {
        serde_json::Value::String(value) => serde_json::from_str(value).ok(),
        value => Some(value.clone()),
    }
}

fn structured_tool_diff(block: &Block) -> Option<String> {
    let value = structured_tool_value(block)?;
    let mut sections = Vec::new();
    if let Some(items) = value.get("sections").and_then(serde_json::Value::as_array) {
        for item in items {
            let diff = item
                .get("diff")
                .and_then(serde_json::Value::as_str)
                .map(str::trim)
                .filter(|diff| !diff.is_empty());
            let Some(diff) = diff else {
                continue;
            };
            let path = item
                .get("path")
                .and_then(serde_json::Value::as_str)
                .map(str::trim)
                .unwrap_or_default();
            sections.push(if path.is_empty() {
                diff.to_string()
            } else {
                format!("--- {path}\n+++ {path}\n{diff}")
            });
        }
    }
    if sections.is_empty()
        && let Some(diff) = value
            .get("diff")
            .and_then(serde_json::Value::as_str)
            .map(str::trim)
            .filter(|diff| !diff.is_empty())
    {
        sections.push(diff.to_string());
    }
    (!sections.is_empty()).then(|| sections.join("\n\n"))
}

fn process_step_presentation(block: &Block, locale: Locale) -> (&'static str, String, String) {
    let kind = tool_activity_kind(block);
    let mut target = truncate_label(&tool_preview(block), 52);
    if kind == ToolActivityKind::Edit
        && let Some((additions, deletions)) = tool_file_change_counts(block)
    {
        target.push_str(&format!("  +{additions} −{deletions}"));
    }
    (kind.icon(), kind.action(locale).to_string(), target)
}

pub(in crate::surfaces) fn process_step_label(block: &Block, locale: Locale) -> String {
    if is_active_process_block(block) {
        return running_tool_summary(block, locale);
    }
    let preview = truncate_label(&tool_preview(block), 52);
    let name = tool_name(block);
    let target = if preview.is_empty() {
        tool_action(name, locale).0.to_string()
    } else {
        preview
    };
    if is_edit_tool(block) {
        let mut label = locale.format("process.editedFile", &[("target", target)]);
        if let Some((additions, deletions)) = tool_file_change_counts(block) {
            label.push_str(&format!(" +{additions} -{deletions}"));
        }
        return label;
    }
    if is_read_tool(name) {
        return locale.format("process.readFile", &[("target", target)]);
    }
    if tool_activity_kind_from_name(name) == ToolActivityKind::Test {
        return locale.format("process.ranTests", &[("target", target)]);
    }
    if is_shell_tool(name) {
        if let Some(elapsed_ms) = process_number(block, "elapsedMs").filter(|value| *value > 0) {
            return locale.format(
                "process.ranCommandIn",
                &[
                    ("target", target),
                    ("duration", format_usage_duration(elapsed_ms, locale)),
                ],
            );
        }
        return locale.format("process.ranCommand", &[("target", target)]);
    }
    locale.format("process.usedTool", &[("target", target)])
}

fn tool_file_change_counts(block: &Block) -> Option<(i64, i64)> {
    let summary = block_data_value(block, "fileChange").and_then(|value| match value {
        serde_json::Value::String(value) => serde_json::from_str(value).ok(),
        value => Some(value.clone()),
    });
    let file = summary
        .as_ref()
        .and_then(|value| value.get("files"))
        .and_then(serde_json::Value::as_array)
        .and_then(|files| files.first());
    let additions = file
        .and_then(|value| value.get("additions"))
        .or_else(|| summary.as_ref().and_then(|value| value.get("additions")))
        .and_then(serde_json::Value::as_i64)
        .or_else(|| process_number(block, "additions"))
        .unwrap_or_default();
    let deletions = file
        .and_then(|value| value.get("deletions"))
        .or_else(|| summary.as_ref().and_then(|value| value.get("deletions")))
        .and_then(serde_json::Value::as_i64)
        .or_else(|| process_number(block, "deletions"))
        .unwrap_or_default();
    (additions > 0 || deletions > 0).then_some((additions, deletions))
}

pub(in crate::surfaces) fn process_detail_row(
    group_index: usize,
    row_index: usize,
    block: &Block,
    palette: ThemePalette,
    locale: Locale,
    reduced_motion: bool,
) -> gpui::AnyElement {
    let row_id = group_index * 1000 + row_index;
    let active = is_active_process_block(block);
    if block.kind.as_ref() == "context_compaction" {
        let label = locale.text("ui.compactingContextAutomatically");
        return div()
            .id(("process-compaction", row_id))
            .role(Role::Status)
            .aria_label(label)
            .min_h(px(34.))
            .flex()
            .items_center()
            .gap_2()
            .text_size(px(12.))
            .text_color(palette.faint)
            .child(icon("file-text", 15., palette.faint))
            .when(active, |row| {
                row.child(animated_activity_label(
                    row_id,
                    label.to_string(),
                    palette,
                    reduced_motion,
                ))
            })
            .when(!active, |row| row.child(label))
            .into_any_element();
    }
    if is_file_change(block) {
        return file_change_card(row_id, block, palette, locale);
    }
    div()
        .id(("process-commentary", row_id))
        .py_1()
        .text_color(if is_thinking_text(block) {
            palette.faint
        } else {
            palette.ink
        })
        .text_size(px(palette.chat_font_size))
        .line_height(px(palette.chat_font_size * 1.6))
        .child(markdown_view(
            row_id,
            block.content.as_ref(),
            active,
            reduced_motion,
            palette,
        ))
        .into_any_element()
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(in crate::surfaces) enum ToolActivityKind {
    Read,
    Edit,
    Command,
    Test,
    Search,
    WebSearch,
    Fetch,
    Subagent,
    Browser,
    Plan,
    Other,
}

impl ToolActivityKind {
    fn action(self, locale: Locale) -> &'static str {
        locale.text(match self {
            Self::Read => "ui.readFile",
            Self::Edit => "ui.editFile",
            Self::Command => "ui.ranCommand",
            Self::Test => "ui.ranTests",
            Self::Search => "ui.searchedCode",
            Self::WebSearch => "ui.search",
            Self::Fetch => "ui.fetchPage",
            Self::Subagent => "ui.subagent",
            Self::Browser => "ui.browser",
            Self::Plan => "ui.updatedPlan",
            Self::Other => "ui.toolCall",
        })
    }

    fn icon(self) -> &'static str {
        match self {
            Self::Read => "file-code",
            Self::Edit => "pencil",
            Self::Command => "terminal",
            Self::Test => "check",
            Self::Search => "search",
            Self::WebSearch => "globe",
            Self::Fetch => "link",
            Self::Subagent => "bot",
            Self::Browser => "monitor",
            Self::Plan => "list-todo",
            Self::Other => "wrench",
        }
    }
}

fn tool_name(block: &Block) -> &str {
    let title = block.title.trim();
    if !title.is_empty() {
        title
    } else {
        block_data_value(block, "name")
            .and_then(serde_json::Value::as_str)
            .map(str::trim)
            .unwrap_or_default()
    }
}

pub(in crate::surfaces) fn tool_activity_kind(block: &Block) -> ToolActivityKind {
    let name = tool_name(block);
    if name.contains("gofmt") {
        return if is_edit_tool(block) {
            ToolActivityKind::Edit
        } else {
            ToolActivityKind::Other
        };
    }
    if is_edit_tool(block) {
        ToolActivityKind::Edit
    } else {
        tool_activity_kind_from_name(name)
    }
}

fn tool_activity_kind_from_name(name: &str) -> ToolActivityKind {
    let normalized = name.to_ascii_lowercase();
    if normalized.contains("read_file") || normalized.contains("read-file") {
        ToolActivityKind::Read
    } else if [
        "edit",
        "write",
        "replace",
        "apply_patch",
        "apply-patch",
        "delete_file",
        "gofmt",
    ]
    .iter()
    .any(|part| normalized.contains(part))
    {
        ToolActivityKind::Edit
    } else if normalized.contains("go_test") || normalized.contains("go-test") {
        ToolActivityKind::Test
    } else if normalized.contains("shell")
        || normalized.contains("command")
        || normalized.ends_with(".exec")
    {
        ToolActivityKind::Command
    } else if normalized.contains("web_search")
        || normalized.contains("web.search")
        || normalized.contains("search_web")
    {
        ToolActivityKind::WebSearch
    } else if normalized.contains("fetch")
        || normalized.contains("read_url")
        || normalized.contains("read-url")
    {
        ToolActivityKind::Fetch
    } else if normalized.contains("browser")
        || normalized.contains("computer")
        || normalized.contains("chrome")
        || normalized.contains("screenshot")
    {
        ToolActivityKind::Browser
    } else if normalized.contains("subagent") || normalized.ends_with("agent") {
        ToolActivityKind::Subagent
    } else if normalized.contains("search")
        || normalized.contains("grep")
        || normalized.contains("glob")
        || normalized.ends_with(".rg")
    {
        ToolActivityKind::Search
    } else if normalized == "todo" || normalized.ends_with(".todo") || normalized.contains("plan") {
        ToolActivityKind::Plan
    } else {
        ToolActivityKind::Other
    }
}

pub(in crate::surfaces) fn tool_action(name: &str, locale: Locale) -> (&'static str, &'static str) {
    let kind = tool_activity_kind_from_name(name);
    (kind.action(locale), kind.icon())
}

fn is_read_tool(name: &str) -> bool {
    tool_activity_kind_from_name(name) == ToolActivityKind::Read
}

fn is_shell_tool(name: &str) -> bool {
    tool_activity_kind_from_name(name) == ToolActivityKind::Command
}

fn is_code_output_tool(name: &str) -> bool {
    matches!(
        tool_activity_kind_from_name(name),
        ToolActivityKind::Command | ToolActivityKind::Test
    )
}

pub(in crate::surfaces) fn is_edit_tool(block: &Block) -> bool {
    let name = tool_name(block);
    let normalized = name.to_ascii_lowercase();
    if normalized.contains("gofmt") {
        return formatter_changed(block)
            .unwrap_or_else(|| block_data_value(block, "fileChange").is_some());
    }
    is_file_change(block) || tool_activity_kind_from_name(name) == ToolActivityKind::Edit
}

fn formatter_changed(block: &Block) -> Option<bool> {
    if let Some(changed) = block_data_value(block, "changed").and_then(json_boolean) {
        return Some(changed);
    }
    let structured = block_data_value(block, "structured")?;
    match structured {
        serde_json::Value::Object(fields) => fields.get("changed").and_then(json_boolean),
        serde_json::Value::String(value) => serde_json::from_str::<serde_json::Value>(value)
            .ok()
            .and_then(|value| value.get("changed").and_then(json_boolean)),
        _ => None,
    }
}

fn tool_preview(block: &Block) -> String {
    if let Some(path) = block_data_value(block, "fileChange")
        .and_then(|value| match value {
            serde_json::Value::String(value) => serde_json::from_str(value).ok(),
            value => Some(value.clone()),
        })
        .and_then(|value: serde_json::Value| value.pointer("/files/0/path").cloned())
        .and_then(|value| value.as_str().map(str::to_string))
    {
        return path;
    }
    if block.kind.as_ref() == "diff" && !block.title.trim().is_empty() {
        return block.title.to_string();
    }
    let preview = block_data_value(block, "arguments")
        .and_then(decoded_tool_arguments)
        .or_else(|| {
            serde_json::from_str::<serde_json::Value>(block.content.trim())
                .ok()
                .as_ref()
                .and_then(decoded_tool_arguments)
        })
        .or_else(|| {
            let content = block.content.trim();
            (!content.starts_with(['{', '[']) && !is_bare_tool_status(block, content))
                .then(|| content.lines().find(|line| !line.trim().is_empty()))
                .flatten()
                .map(str::trim)
                .map(str::to_string)
        })
        .unwrap_or_default();
    truncate_label(&compact_tool_preview(&preview), 96)
}

fn compact_tool_preview(value: &str) -> String {
    let value = value.trim();
    if value.contains('\n')
        && let Some(first_line) = value.lines().find(|line| !line.trim().is_empty())
        && let Some((command, _)) = first_line.split_once("<<")
    {
        return command.trim().trim_end_matches('-').trim_end().to_string();
    }
    value.split_whitespace().collect::<Vec<_>>().join(" ")
}

fn is_bare_tool_status(block: &Block, content: &str) -> bool {
    is_active_process_block(block)
        && [
            "running",
            "queued",
            "pending",
            "started",
            "progress",
            "arguments",
        ]
        .iter()
        .any(|status| content.eq_ignore_ascii_case(status))
}

fn decoded_tool_arguments(value: &serde_json::Value) -> Option<String> {
    match value {
        serde_json::Value::Object(arguments) => {
            let text = |key| {
                arguments
                    .get(key)
                    .and_then(serde_json::Value::as_str)
                    .map(str::trim)
                    .filter(|value| !value.is_empty())
            };
            for (first_key, second_key) in [("skill", "path"), ("query", "glob")] {
                if let (Some(first), Some(second)) = (text(first_key), text(second_key)) {
                    return Some(format!("{first} · {second}"));
                }
            }
            [
                "path",
                "query",
                "pattern",
                "command",
                "cmd",
                "package",
                "goal",
                "url",
                "task",
                "prompt",
                "artifact_id",
                "name",
                "skill",
            ]
            .into_iter()
            .find_map(text)
            .map(str::to_string)
        }
        serde_json::Value::String(value) => serde_json::from_str(value)
            .ok()
            .as_ref()
            .and_then(decoded_tool_arguments),
        _ => None,
    }
}

fn file_change_card(
    row_id: usize,
    block: &Block,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
    let summary = block_data_value(block, "fileChange").and_then(|value| match value {
        serde_json::Value::String(value) => serde_json::from_str(value).ok(),
        value => Some(value.clone()),
    });
    let files = summary
        .as_ref()
        .and_then(|value| value.get("files"))
        .and_then(serde_json::Value::as_array)
        .cloned()
        .unwrap_or_default();
    let additions = summary
        .as_ref()
        .and_then(|value| value.get("additions"))
        .and_then(serde_json::Value::as_i64)
        .unwrap_or_else(|| process_number(block, "additions").unwrap_or_default());
    let deletions = summary
        .as_ref()
        .and_then(|value| value.get("deletions"))
        .and_then(serde_json::Value::as_i64)
        .unwrap_or_else(|| process_number(block, "deletions").unwrap_or_default());
    let file_count = files.len().max(1);
    div()
        .id(("file-change-card", row_id))
        .w_full()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .overflow_hidden()
        .child(
            div()
                .min_h(px(58.))
                .px_3()
                .flex()
                .items_center()
                .gap_3()
                .child(
                    div()
                        .size(px(38.))
                        .rounded(px(10.))
                        .bg(palette.paper_muted)
                        .flex()
                        .items_center()
                        .justify_center()
                        .child(icon("file-diff", 18., palette.muted)),
                )
                .child(
                    div()
                        .flex_1()
                        .flex()
                        .flex_col()
                        .gap_1()
                        .child(
                            div()
                                .text_size(px(13.))
                                .font_weight(gpui::FontWeight::MEDIUM)
                                .text_color(palette.ink)
                                .child(locale.format(
                                    "ui.editedFileCountFiles",
                                    &[("file_count", (file_count).to_string())],
                                )),
                        )
                        .child(
                            div()
                                .text_size(px(11.))
                                .flex()
                                .gap_2()
                                .child(
                                    div()
                                        .text_color(palette.positive)
                                        .child(format!("+{additions}")),
                                )
                                .child(
                                    div()
                                        .text_color(palette.danger)
                                        .child(format!("−{deletions}")),
                                ),
                        ),
                ),
        )
        .when(!files.is_empty(), |card| {
            card.child(
                div()
                    .border_t_1()
                    .border_color(palette.border)
                    .flex()
                    .flex_col()
                    .children(files.into_iter().enumerate().map(|(index, file)| {
                        let path = file
                            .get("path")
                            .and_then(serde_json::Value::as_str)
                            .unwrap_or_default()
                            .to_string();
                        let additions = file
                            .get("additions")
                            .and_then(serde_json::Value::as_i64)
                            .unwrap_or_default();
                        let deletions = file
                            .get("deletions")
                            .and_then(serde_json::Value::as_i64)
                            .unwrap_or_default();
                        div()
                            .id(("file-change-row", row_id * 100 + index))
                            .min_h(px(38.))
                            .px_3()
                            .flex()
                            .items_center()
                            .gap_2()
                            .text_size(px(12.))
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .truncate()
                                    .text_color(palette.muted)
                                    .child(path),
                            )
                            .child(
                                div()
                                    .text_color(palette.positive)
                                    .child(format!("+{additions}")),
                            )
                            .child(
                                div()
                                    .text_color(palette.danger)
                                    .child(format!("−{deletions}")),
                            )
                    })),
            )
        })
        .into_any_element()
}

fn block_data_value<'a>(block: &'a Block, key: &str) -> Option<&'a serde_json::Value> {
    block
        .extra
        .get(key)
        .or_else(|| block.extra.get("data").and_then(|data| data.get(key)))
}

fn json_integer(value: &serde_json::Value) -> Option<i64> {
    value
        .as_i64()
        .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
}

fn json_boolean(value: &serde_json::Value) -> Option<bool> {
    value
        .as_bool()
        .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
}

fn truncate_label(value: &str, limit: usize) -> String {
    let mut chars = value.chars();
    let result = chars.by_ref().take(limit).collect::<String>();
    if chars.next().is_some() {
        result + "…"
    } else {
        result
    }
}

fn process_number(block: &Block, key: &str) -> Option<i64> {
    block
        .extra
        .get(key)
        .or_else(|| block.extra.get("data").and_then(|data| data.get(key)))
        .and_then(json_integer)
}

pub(in crate::surfaces) fn is_file_change(block: &Block) -> bool {
    if block.kind.as_ref() == "diff" || block_data_value(block, "fileChange").is_some() {
        return true;
    }
    matches!(
        block.title.as_ref(),
        "coding.write_file"
            | "coding.edit"
            | "coding.edit_hashline"
            | "coding.replace"
            | "coding.delete_file"
            | "coding.gofmt"
    )
}

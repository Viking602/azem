use super::*;
pub(super) mod process;
use process::{
    is_agent_block, is_hidden_process_block, is_process_tool_block, is_thinking_text,
    pending_process_entry, process_detail_row, thinking_belongs_to_tool_group,
    thinking_process_entry, tool_group_entry,
};

pub(crate) fn timeline_entry(
    index: usize,
    blocks: &[Block],
    style: (ThemePalette, Locale, bool, f32, i64),
    agents: &[serde_json::Value],
    expansion: Rc<RefCell<ProcessExpansion>>,
    owner: Entity<AzemWindow>,
    reply_actions: Option<&ReplyActionsSnapshot>,
) -> gpui::AnyElement {
    let (palette, locale, reduced_motion, horizontal_gutter, live_elapsed_ms) = style;
    let Some(block) = blocks.get(index) else {
        if index == blocks.len()
            && blocks
                .last()
                .is_some_and(|block| block.kind.as_ref() == "user")
        {
            return pending_process_entry(
                index,
                palette,
                locale,
                reduced_motion,
                horizontal_gutter,
                live_elapsed_ms,
            );
        }
        return div().h(px(0.)).into_any_element();
    };
    let kind = block.kind.as_ref();
    if is_thinking_text(block) && !thinking_belongs_to_tool_group(blocks, index) {
        return thinking_process_entry(
            index,
            block,
            palette,
            locale,
            reduced_motion,
            horizontal_gutter,
            live_elapsed_ms,
        );
    }
    if is_hidden_process_block(block) {
        return div().h(px(0.)).into_any_element();
    }
    if is_process_tool_block(block) {
        return tool_group_entry(
            index,
            blocks,
            (
                palette,
                locale,
                reduced_motion,
                horizontal_gutter,
                live_elapsed_ms,
            ),
            agents,
            expansion,
            owner,
        );
    }
    if is_agent_block(block) {
        return div().h(px(0.)).into_any_element();
    }
    if kind == "context_compaction" {
        let body = div()
            .w_full()
            .max_w(px(CHAT_COLUMN_MAX_WIDTH))
            .child(process_detail_row(
                index,
                0,
                block,
                palette,
                locale,
                reduced_motion,
            ));
        return div()
            .id(("timeline-block", index))
            .role(Role::Article)
            .aria_label(kind.to_string())
            .w_full()
            .min_w_0()
            .px(px(horizontal_gutter))
            .py(px(4.))
            .flex()
            .flex_col()
            .items_center()
            .child(body)
            .into_any_element();
    }
    let hover_key = timeline_message_key(index, block);
    let message_hovered = reply_actions.and_then(|actions| actions.hovered_message.as_deref())
        == Some(hover_key.as_str());
    let row = if kind == "user" {
        let time = message_time(block);
        let bubble = div()
            .w_full()
            .min_w_0()
            .max_w(px(CHAT_COLUMN_MAX_WIDTH))
            .flex()
            .justify_end()
            .child(
                div()
                    .min_w_0()
                    .max_w(px(680.))
                    .flex()
                    .flex_col()
                    .items_end()
                    .child(
                        div()
                            .px_3()
                            .py_2()
                            .rounded(px(16.))
                            .bg(palette.paper_muted)
                            .text_color(palette.ink)
                            .text_size(px(palette.chat_font_size))
                            .line_height(px(palette.chat_font_size * 1.6))
                            .whitespace_normal()
                            .child(block.content.clone()),
                    )
                    .when_some(time, |bubble, time| {
                        bubble.child(
                            div()
                                .h(px(22.))
                                .mt_1()
                                .pr_1()
                                .flex()
                                .items_center()
                                .text_size(px(11.))
                                .text_color(palette.faint)
                                .opacity(if message_hovered { 1. } else { 0. })
                                .child(time),
                        )
                    }),
            );
        if animate_submitted_user(block.state.as_ref(), reduced_motion) {
            bubble
                .with_animation(
                    ("user-message-submit", index),
                    Animation::new(USER_MESSAGE_SUBMIT_TRANSITION)
                        .with_easing(gpui::ease_out_quint()),
                    |bubble, progress| {
                        bubble
                            .relative()
                            .top(px(8. * (1. - progress)))
                            .opacity(progress)
                    },
                )
                .into_any_element()
        } else {
            bubble.into_any_element()
        }
    } else {
        let active = matches!(
            block.state.as_ref(),
            "streaming" | "running" | "started" | "progress"
        );
        let content = if kind == "assistant" {
            visible_assistant_content(block.content.as_ref())
        } else {
            block.content.as_ref()
        };
        if kind == "assistant" && content.is_empty() && !active {
            return div().h(px(0.)).into_any_element();
        }
        let message = div()
            .w_full()
            .min_w_0()
            .max_w(px(CHAT_COLUMN_MAX_WIDTH))
            .when(kind == "error", |message| {
                message
                    .px_3()
                    .py_2()
                    .rounded(px(10.))
                    .bg(palette.paper_muted)
                    .text_color(palette.danger)
            })
            .when(is_thinking_text(block), |message| {
                message.text_color(palette.faint)
            })
            .when(kind != "error" && !is_thinking_text(block), |message| {
                message.text_color(palette.ink)
            })
            .text_size(px(palette.chat_font_size))
            .line_height(px(palette.chat_font_size * 1.6))
            .child(markdown_view(
                index,
                content,
                active,
                reduced_motion,
                palette,
            ));
        if final_reply_footer_target(index, blocks)
            && let Some(actions) = reply_actions
        {
            message
                .child(reply_footer(
                    index,
                    block,
                    blocks,
                    (palette, locale, reduced_motion),
                    actions,
                    message_hovered,
                    owner.clone(),
                ))
                .into_any_element()
        } else {
            message.into_any_element()
        }
    };
    let hover_owner = owner.clone();
    let hover_enabled = reply_actions.is_some();
    let hover_message_key = hover_key.clone();
    let message = div()
        .id(("timeline-message", index))
        .w_full()
        .max_w(px(CHAT_COLUMN_MAX_WIDTH))
        .child(row);
    let message = if hover_enabled {
        message.on_hover(move |hovered: &bool, _, cx| {
            let key = hover_message_key.clone();
            hover_owner.update(cx, |this, cx| {
                if *hovered {
                    if this.hovered_message.as_deref() != Some(key.as_str()) {
                        this.hovered_message = Some(key);
                        cx.notify();
                    }
                } else if this.hovered_message.as_deref() == Some(key.as_str()) {
                    this.hovered_message = None;
                    cx.notify();
                }
            });
        })
    } else {
        message
    };
    div()
        .id(("timeline-block", index))
        .role(Role::Article)
        .aria_label(kind.to_string())
        .w_full()
        .min_w_0()
        .px(px(horizontal_gutter))
        .py(px(8.))
        .flex()
        .flex_col()
        .items_center()
        .child(message)
        .into_any_element()
}

struct ReplyActionTooltip {
    label: String,
    palette: ThemePalette,
}

impl Render for ReplyActionTooltip {
    fn render(&mut self, _: &mut Window, _: &mut Context<Self>) -> impl IntoElement {
        div().p_1().child(
            div()
                .px_2()
                .py_1()
                .rounded(px(7.))
                .bg(self.palette.button)
                .text_color(self.palette.button_text)
                .text_xs()
                .shadow(vec![
                    BoxShadow::new(px(0.), px(5.), hsla(220. / 360., 0.12, 0.12, 0.2))
                        .blur_radius(px(14.)),
                ])
                .child(self.label.clone()),
        )
    }
}

#[derive(Clone)]
struct ReplyHookItem {
    event: String,
    origin: String,
    detail: String,
}

pub(in crate::surfaces) fn final_reply_footer_target(index: usize, blocks: &[Block]) -> bool {
    let Some(block) = blocks.get(index) else {
        return false;
    };
    block.kind.as_ref() == "assistant"
        && block.text_phase.as_ref() == "final_answer"
        && matches!(block.state.as_ref(), "completed" | "complete")
        && !blocks[index + 1..]
            .iter()
            .any(|later| later.run_id == block.run_id)
}

fn reply_footer(
    index: usize,
    block: &Block,
    blocks: &[Block],
    style: (ThemePalette, Locale, bool),
    actions: &ReplyActionsSnapshot,
    show_time: bool,
    owner: Entity<AzemWindow>,
) -> gpui::AnyElement {
    let (palette, locale, _reduced_motion) = style;
    let key = reply_key(index, block);
    let memory_notes = assistant_memory_notes(&block.content);
    let hooks = reply_hooks(
        &actions.hooks,
        &actions.hook_catalog,
        block.run_id.as_ref(),
        locale,
    );
    let popover = actions
        .popover
        .as_ref()
        .filter(|(open_key, _)| open_key == &key)
        .map(|(_, kind)| *kind);
    let feedback = actions.feedback.get(&key).copied().unwrap_or_default();
    let copy_text = visible_assistant_content(&block.content).to_string();
    let sequence = block
        .extra
        .get("sequence")
        .and_then(serde_json::Value::as_i64);
    let assistant_ordinal = blocks[..=index]
        .iter()
        .filter(|candidate| {
            candidate.kind.as_ref() == "assistant"
                && candidate.text_phase.as_ref() == "final_answer"
        })
        .count()
        .saturating_sub(1);
    let anchor = ReplyForkAnchor {
        sequence,
        assistant_ordinal,
    };

    let copy = reply_action_button(
        format!("reply-copy-{index}"),
        "copy",
        locale.text("reply.copy").to_string(),
        false,
        palette,
        owner.clone(),
        move |_, cx| cx.write_to_clipboard(ClipboardItem::new_string(copy_text.clone())),
    );
    let good_key = key.clone();
    let good = reply_action_button(
        format!("reply-good-{index}"),
        "thumbs-up",
        locale.text("reply.good").to_string(),
        feedback == 1,
        palette,
        owner.clone(),
        move |owner, cx| {
            let key = good_key.clone();
            owner.update(cx, |this, cx| {
                if this.reply_feedback.get(&key) == Some(&1) {
                    this.reply_feedback.remove(&key);
                } else {
                    this.reply_feedback.insert(key, 1);
                }
                cx.notify();
            });
        },
    );
    let bad_key = key.clone();
    let bad = reply_action_button(
        format!("reply-bad-{index}"),
        "thumbs-down",
        locale.text("reply.bad").to_string(),
        feedback == -1,
        palette,
        owner.clone(),
        move |owner, cx| {
            let key = bad_key.clone();
            owner.update(cx, |this, cx| {
                if this.reply_feedback.get(&key) == Some(&-1) {
                    this.reply_feedback.remove(&key);
                } else {
                    this.reply_feedback.insert(key, -1);
                }
                cx.notify();
            });
        },
    );
    let branch = reply_action_button(
        format!("reply-branch-{index}"),
        "git-branch",
        locale.text("reply.branch").to_string(),
        false,
        palette,
        owner.clone(),
        move |owner, cx| {
            owner.update(cx, |this, cx| this.fork_reply(anchor, cx));
        },
    );

    let mut footer = div()
        .id(("reply-actions", index))
        .role(Role::Toolbar)
        .aria_label(locale.text("reply.actions"))
        .relative()
        .mt(px(7.))
        .h(px(30.))
        .flex()
        .items_center()
        .gap(px(2.))
        .text_size(px(11.))
        .text_color(palette.faint)
        .child(copy)
        .child(good)
        .child(bad)
        .child(branch);
    if !hooks.is_empty() {
        let hook_key = key.clone();
        footer = footer.child(reply_action_button(
            format!("reply-hooks-{index}"),
            "anchor",
            locale.text("reply.hooks").to_string(),
            popover == Some(ReplyPopoverKind::Hooks),
            palette,
            owner.clone(),
            move |owner, cx| {
                let key = hook_key.clone();
                owner.update(cx, |this, cx| {
                    let next = (key, ReplyPopoverKind::Hooks);
                    this.reply_popover =
                        (this.reply_popover.as_ref() != Some(&next)).then_some(next);
                    cx.notify();
                });
            },
        ));
    }
    if !memory_notes.is_empty() {
        let memory_key = key.clone();
        footer = footer.child(reply_action_button(
            format!("reply-memories-{index}"),
            "notebook-pen",
            locale.text("reply.memories").to_string(),
            popover == Some(ReplyPopoverKind::Memories),
            palette,
            owner.clone(),
            move |owner, cx| {
                let key = memory_key.clone();
                owner.update(cx, |this, cx| {
                    let next = (key, ReplyPopoverKind::Memories);
                    this.reply_popover =
                        (this.reply_popover.as_ref() != Some(&next)).then_some(next);
                    cx.notify();
                });
            },
        ));
    }
    if show_time && let Some(time) = message_time(block) {
        footer = footer.child(div().ml_2().text_color(palette.faint).child(time));
    }
    if let Some(kind) = popover {
        footer = footer.child(reply_metadata_popover(
            index,
            kind,
            &hooks,
            &memory_notes,
            style,
        ));
        let dismiss_owner = owner;
        footer = footer.on_mouse_down_out(move |_, _, cx| {
            dismiss_owner.update(cx, |this, cx| {
                this.reply_popover = None;
                cx.notify();
            });
            cx.stop_propagation();
        });
    }
    footer.into_any_element()
}

fn reply_action_button(
    id: String,
    icon_name: &'static str,
    label: String,
    selected: bool,
    palette: ThemePalette,
    owner: Entity<AzemWindow>,
    on_click: impl Fn(&Entity<AzemWindow>, &mut gpui::App) + 'static,
) -> gpui::AnyElement {
    let tooltip_label = label.clone();
    div()
        .id(id)
        .role(Role::Button)
        .aria_label(label)
        .aria_selected(selected)
        .tab_stop(true)
        .size(px(28.))
        .rounded(px(8.))
        .bg(if selected {
            palette.paper_muted
        } else {
            palette.paper
        })
        .flex()
        .items_center()
        .justify_center()
        .cursor_pointer()
        .hover(move |style| style.bg(palette.hover))
        .on_click(move |_, _, cx| on_click(&owner, cx))
        .tooltip(move |_, cx| {
            cx.new(|_| ReplyActionTooltip {
                label: tooltip_label.clone(),
                palette,
            })
            .into()
        })
        .child(icon(
            icon_name,
            16.,
            if selected {
                palette.ink_soft
            } else {
                palette.faint
            },
        ))
        .into_any_element()
}

fn reply_key(index: usize, block: &Block) -> String {
    if !block.run_id.is_empty() {
        format!("run:{}", block.run_id)
    } else if let Some(sequence) = block
        .extra
        .get("sequence")
        .and_then(serde_json::Value::as_i64)
    {
        format!("sequence:{sequence}")
    } else {
        format!("reply:{index}")
    }
}

pub(super) fn timeline_message_key(index: usize, block: &Block) -> String {
    format!("{}:{}", block.kind, reply_key(index, block))
}

pub(super) fn message_time(block: &Block) -> Option<String> {
    let value = ["completedAt", "createdAt", "submittedAt"]
        .into_iter()
        .find_map(|key| {
            block
                .extra
                .get(key)
                .or_else(|| block.extra.get("data").and_then(|data| data.get(key)))
        })?;
    let local = if let Some(milliseconds) = value
        .as_i64()
        .or_else(|| value.as_str().and_then(|value| value.parse::<i64>().ok()))
    {
        Local.timestamp_millis_opt(milliseconds).single()?
    } else {
        DateTime::parse_from_rfc3339(value.as_str()?)
            .ok()?
            .with_timezone(&Local)
    };
    Some(local.format("%H:%M").to_string())
}

fn reply_hooks(
    hooks: &[serde_json::Value],
    catalog: &serde_json::Value,
    run_id: &str,
    locale: Locale,
) -> Vec<ReplyHookItem> {
    hooks
        .iter()
        .filter(|hook| hook.get("runId").and_then(serde_json::Value::as_str) == Some(run_id))
        .map(|hook| {
            let data = hook.get("data").unwrap_or(&serde_json::Value::Null);
            let event = data
                .get("event")
                .and_then(serde_json::Value::as_str)
                .unwrap_or("Hook")
                .to_string();
            let origin = hook_origin(data, catalog);
            let origin = locale
                .text(if origin == "plugin" {
                    "reply.originPlugin"
                } else {
                    "reply.originUser"
                })
                .to_string();
            let detail = ["statusMessage", "reason", "stdout", "stderr", "tool"]
                .into_iter()
                .find_map(|key| {
                    data.get(key)
                        .and_then(serde_json::Value::as_str)
                        .map(str::trim)
                        .filter(|value| !value.is_empty())
                })
                .map(compact_reply_detail)
                .unwrap_or_default();
            ReplyHookItem {
                event,
                origin,
                detail,
            }
        })
        .collect()
}

fn hook_origin(data: &serde_json::Value, catalog: &serde_json::Value) -> &'static str {
    let event = data.get("event").and_then(serde_json::Value::as_str);
    let name = data.get("name").and_then(serde_json::Value::as_str);
    if let Some(origin) = catalog
        .get("commands")
        .and_then(serde_json::Value::as_array)
        .and_then(|commands| {
            commands.iter().find(|command| {
                command.get("event").and_then(serde_json::Value::as_str) == event
                    && command.get("name").and_then(serde_json::Value::as_str) == name
            })
        })
        .and_then(|command| command.get("origin"))
        .and_then(serde_json::Value::as_str)
    {
        return if origin == "plugin" { "plugin" } else { "user" };
    }
    if data
        .get("source")
        .and_then(serde_json::Value::as_str)
        .is_some_and(|source| source.contains("plugin"))
    {
        "plugin"
    } else {
        "user"
    }
}

fn compact_reply_detail(value: &str) -> String {
    let value = value.lines().next().unwrap_or_default().trim();
    let mut characters = value.chars();
    let compact = characters.by_ref().take(72).collect::<String>();
    if characters.next().is_some() {
        compact + "…"
    } else {
        compact
    }
}

fn reply_metadata_popover(
    index: usize,
    kind: ReplyPopoverKind,
    hooks: &[ReplyHookItem],
    memories: &[String],
    style: (ThemePalette, Locale, bool),
) -> gpui::AnyElement {
    let (palette, locale, reduced_motion) = style;
    let title = locale.text(match kind {
        ReplyPopoverKind::Hooks => "reply.hooksTitle",
        ReplyPopoverKind::Memories => "reply.memoriesTitle",
    });
    let rows = match kind {
        ReplyPopoverKind::Hooks => hooks
            .iter()
            .enumerate()
            .map(|(row, hook)| {
                div()
                    .id(("reply-hook-row", index * 256 + row))
                    .flex()
                    .items_start()
                    .gap_3()
                    .child(
                        div()
                            .w(px(180.))
                            .text_color(palette.button_text)
                            .child(hook.event.clone()),
                    )
                    .child(div().flex_1().min_w_0().text_color(rgba(0xc7c9cdff)).child(
                        if hook.detail.is_empty() {
                            hook.origin.clone()
                        } else {
                            format!("{} · {}", hook.origin, hook.detail)
                        },
                    ))
                    .into_any_element()
            })
            .collect::<Vec<_>>(),
        ReplyPopoverKind::Memories => memories
            .iter()
            .enumerate()
            .map(|(row, memory)| {
                div()
                    .id(("reply-memory-row", index * 256 + row))
                    .flex()
                    .items_start()
                    .gap_2()
                    .text_color(rgba(0xc7c9cdff))
                    .child("•")
                    .child(div().flex_1().min_w_0().child(memory.clone()))
                    .into_any_element()
            })
            .collect::<Vec<_>>(),
    };
    let popover = div()
        .id(("reply-metadata-popover", index))
        .role(Role::Region)
        .aria_label(title)
        .absolute()
        .bottom(px(34.))
        .left(px(92.))
        .w(px(520.))
        .max_h(px(520.))
        .rounded(px(12.))
        .bg(palette.button)
        .text_size(px(12.))
        .line_height(px(18.))
        .shadow(vec![
            BoxShadow::new(px(0.), px(10.), hsla(220. / 360., 0.12, 0.12, 0.25))
                .blur_radius(px(28.)),
        ])
        .occlude()
        .overflow_hidden()
        .flex()
        .flex_col()
        .child(
            div()
                .px_3()
                .pt_3()
                .pb_2()
                .text_color(palette.button_text)
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .child(title),
        )
        .child(
            div()
                .id(("reply-metadata-list", index))
                .px_3()
                .pb_3()
                .overflow_y_scroll()
                .flex()
                .flex_col()
                .gap_2()
                .children(rows),
        );
    if reduced_motion {
        popover.into_any_element()
    } else {
        popover
            .with_animation(
                (
                    "reply-popover-open",
                    index * 2 + usize::from(kind == ReplyPopoverKind::Memories),
                ),
                Animation::new(Duration::from_millis(150)).with_easing(gpui::ease_out_quint()),
                |popover, progress| {
                    popover
                        .relative()
                        .top(px(5. * (1. - progress)))
                        .opacity(progress)
                },
            )
            .into_any_element()
    }
}

use super::*;
pub(super) fn settings_governance_body(
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let session_id = state.navigation.current_session_id.to_string();
    let approval_controls = [
        ("prompt", locale.text("ui.askEveryTime")),
        ("auto_review", locale.text("ui.autoReview")),
        ("yolo", locale.text("approval.yolo")),
    ]
    .into_iter()
    .enumerate()
    .map(|(id, (value, label))| {
        settings_action_button(
            id,
            label,
            state.connection.connected,
            state.settings.approval_mode.as_ref() == value,
            palette,
            cx,
            json!({"kind": "set_approval_mode", "target": value, "sessionId": session_id}),
        )
    })
    .collect::<Vec<_>>();
    let delivery_controls = [
        ("queue", locale.text("ui.queue")),
        ("guide", locale.text("ui.guideLive")),
    ]
    .into_iter()
    .enumerate()
    .map(|(index, (value, label))| {
        settings_action_button(
            index + 3,
            label,
            state.connection.connected,
            state.settings.queue_mode.as_ref() == value,
            palette,
            cx,
            json!({"kind": "set_queue_mode", "target": value, "sessionId": session_id}),
        )
    })
    .collect::<Vec<_>>();
    div()
        .w_full()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .overflow_hidden()
        .child(settings_control_row(
            locale.text("ui.defaultApprovalMode"),
            locale.text("ui.controlsToolExecutionBoundaries"),
            300.,
            approval_controls,
            palette,
        ))
        .child(settings_control_row(
            locale.text("ui.newMessagesWhileRunning"),
            locale.text("ui.whenFollowUpInputArrives"),
            200.,
            delivery_controls,
            palette,
        ))
        .child(
            div()
                .min_h(px(64.))
                .px_4()
                .border_t_1()
                .border_color(palette.border)
                .flex()
                .items_center()
                .child(
                    div()
                        .flex_1()
                        .flex()
                        .flex_col()
                        .gap_1()
                        .child(
                            div()
                                .text_sm()
                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                .child(locale.text("ui.approvalValidation")),
                        )
                        .child(div().text_xs().text_color(palette.muted).child(
                            locale.text("ui.onlyCompleteJsonIsAcceptedInvalidContentNeverExecutes"),
                        )),
                )
                .child(
                    div()
                        .text_sm()
                        .text_color(palette.positive)
                        .child(locale.text("ui.failClosed")),
                ),
        )
        .into_any_element()
}

fn settings_control_row(
    title: &'static str,
    description: &'static str,
    control_width: f32,
    controls: Vec<gpui::AnyElement>,
    palette: ThemePalette,
) -> gpui::Div {
    div()
        .min_h(px(68.))
        .px_4()
        .py(px(11.))
        .border_t_1()
        .border_color(palette.border)
        .flex()
        .items_center()
        .gap_5()
        .child(
            div()
                .min_w_0()
                .flex_1()
                .flex()
                .flex_col()
                .gap_1()
                .child(
                    div()
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(title),
                )
                .child(div().text_sm().text_color(palette.muted).child(description)),
        )
        .child(
            div()
                .w(px(control_width))
                .p(px(2.))
                .rounded(px(9.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper)
                .flex()
                .children(controls),
        )
}

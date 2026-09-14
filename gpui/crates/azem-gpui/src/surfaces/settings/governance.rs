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
        .flex()
        .flex_col()
        .gap_6()
        .child(settings_group(
            locale.text("ui.defaultApprovalMode"),
            "",
            settings_rows(
                vec![
                    settings_control_row(
                        locale.text("ui.defaultApprovalMode"),
                        locale.text("ui.controlsToolExecutionBoundaries"),
                        300.,
                        approval_controls,
                        palette,
                    )
                    .into_any_element(),
                    settings_children_row(
                        locale.text("ui.approvalValidation"),
                        locale.text("ui.onlyCompleteJsonIsAcceptedInvalidContentNeverExecutes"),
                        div()
                            .text_sm()
                            .text_color(palette.positive)
                            .child(locale.text("ui.failClosed"))
                            .into_any_element(),
                        palette,
                    )
                    .into_any_element(),
                ],
                palette,
            ),
            palette,
        ))
        .child(settings_group(
            locale.text("ui.newMessagesWhileRunning"),
            "",
            div().child(settings_control_row(
                locale.text("ui.newMessagesWhileRunning"),
                locale.text("ui.whenFollowUpInputArrives"),
                200.,
                delivery_controls,
                palette,
            )),
            palette,
        ))
        .into_any_element()
}

fn settings_control_row(
    title: &'static str,
    description: &'static str,
    control_width: f32,
    controls: Vec<gpui::AnyElement>,
    palette: ThemePalette,
) -> gpui::Div {
    settings_children_row(
        title,
        description,
        div()
            .w(px(control_width))
            .max_w_full()
            .p(px(2.))
            .rounded(px(9.))
            .border_1()
            .border_color(palette.border)
            .bg(palette.paper)
            .flex()
            .children(controls)
            .into_any_element(),
        palette,
    )
}

use super::*;
pub(super) fn settings_security_body(
    state: &AppState,
    native: &NativeSettings,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    if native.security_draft.is_null() {
        return div().flex().flex_col().gap_3()
            .child(locale.text("ui.loadingSecuritySettings"))
            .child(settings_action_button(30, locale.text("ui.reload"),
                state.connection.connected, false, palette, cx,
                json!({"kind":"get_security_config", "sessionId":state.navigation.current_session_id})))
            .into_any_element();
    }
    let editable = state.connection.connected && !native.security_busy;
    let config = &native.security_draft;
    let enabled = config["enabled"].as_bool().unwrap_or(false);
    let mode = config["defaultMode"].as_str().unwrap_or("standard");
    let dirty = native.security_dirty(cx);
    let field_labels = [
        (locale.text("ui.deepScanWorkers"), "1–32"),
        (locale.text("ui.investigatorsPerAudit"), "0–32"),
        (locale.text("ui.noNewThreshold"), "1–1000"),
        (locale.text("ui.consecutiveErrorThreshold"), "1–1000"),
        (locale.text("ui.discoveryRunCap"), "1–1000"),
        (locale.text("ui.deadlineHours"), "0.5–96"),
    ];
    let numbers = native
        .security_fields
        .iter()
        .enumerate()
        .map(|(index, input)| {
            let (key, _, _) = SECURITY_NUMBERS[index];
            let (label, hint) = field_labels[index];
            settings_children_row(
                label,
                hint,
                div()
                    .id(format!("security-field-{key}"))
                    .role(Role::Group)
                    .aria_label(label)
                    .w(px(130.))
                    .h(px(36.))
                    .rounded(px(8.))
                    .border_1()
                    .border_color(palette.border_strong)
                    .bg(palette.paper)
                    .child(input.clone())
                    .into_any_element(),
                palette,
            )
            .into_any_element()
        })
        .collect::<Vec<_>>();
    let modes = [
        ("standard", locale.text("ui.standard")),
        ("deep", locale.text("ui.deep")),
    ]
    .into_iter()
    .map(|(value, label)| {
        div()
            .id(format!("security-mode-{value}"))
            .role(Role::RadioButton)
            .aria_label(label)
            .aria_selected(mode == value)
            .tab_stop(editable)
            .h(px(32.))
            .px_3()
            .rounded(px(7.))
            .flex()
            .items_center()
            .bg(if mode == value {
                palette.accent_soft
            } else {
                palette.paper
            })
            .when(editable, |b| {
                b.cursor_pointer().hover(move |s| s.bg(palette.hover))
            })
            .on_click(cx.listener(move |this, _, _, cx| {
                if editable {
                    this.native_settings.security_draft["defaultMode"] = json!(value);
                    this.native_settings.security_saved = false;
                    cx.notify();
                }
            }))
            .child(label)
            .into_any_element()
    })
    .collect::<Vec<_>>();
    div()
        .w_full()
        .flex()
        .flex_col()
        .gap_4()
        .child(
            div()
                .rounded(px(12.))
                .border_1()
                .border_color(palette.border)
                .bg(palette.paper)
                .child(settings_card_header(
                    locale.text("ui.executionPolicy"),
                    locale.text("ui.appliesToNewScansActiveScansRetainTheirCapturedSettings"),
                    palette,
                ))
                .child(settings_children_row(
                    locale.text("ui.enableSecurityScans"),
                    locale.text("ui.allowStandardAndDeepScansWithoutDeletingResults"),
                    div()
                        .id("security-enabled")
                        .role(Role::Switch)
                        .aria_label(locale.text("ui.enableSecurityScans"))
                        .aria_toggled(enabled.into())
                        .tab_stop(editable)
                        .when(editable, |b| b.cursor_pointer())
                        .on_click(cx.listener(move |this, _, _, cx| {
                            if editable {
                                this.native_settings.security_draft["enabled"] = json!(!enabled);
                                this.native_settings.security_saved = false;
                                cx.notify();
                            }
                        }))
                        .child(settings_switch(enabled, palette))
                        .into_any_element(),
                    palette,
                ))
                .child(settings_children_row(
                    locale.text("ui.defaultMode"),
                    locale.text("ui.defaultAuditDepthForNewScans"),
                    div().flex().gap_2().children(modes).into_any_element(),
                    palette,
                ))
                .children(numbers),
        )
        .child(settings_detail_card(
            locale.text("ui.publication"),
            locale.text("ui.readOnlyExternalPublicationToolsAreConfiguredByTheHost"),
            vec![settings_detail_row(
                locale.text("ui.publicationTool"),
                locale.text("ui.thisPageNeverModifiesToolArgumentsCredentialsOrModelRoutes"),
                state.security.config["publicationTool"]
                    .as_str()
                    .filter(|s| !s.is_empty())
                    .unwrap_or(locale.text("ui.disabled2"))
                    .to_string(),
                palette,
            )],
            palette,
        ))
        .child(
            div()
                .flex()
                .items_center()
                .justify_between()
                .child(
                    div()
                        .id("security-save-status")
                        .role(Role::Status)
                        .text_sm()
                        .text_color(palette.muted)
                        .child(if dirty {
                            locale.text("ui.unsavedChanges")
                        } else if native.security_saved {
                            locale.text("ui.securitySettingsSaved")
                        } else {
                            ""
                        }),
                )
                .child(
                    div()
                        .id("security-save")
                        .role(Role::Button)
                        .aria_label(locale.text("ui.saveSecuritySettings"))
                        .tab_stop(editable && dirty)
                        .h(px(36.))
                        .px_3()
                        .rounded(px(8.))
                        .bg(if editable && dirty {
                            palette.button
                        } else {
                            palette.paper_muted
                        })
                        .text_color(if editable && dirty {
                            palette.button_text
                        } else {
                            palette.muted
                        })
                        .flex()
                        .items_center()
                        .when(editable && dirty, |b| b.cursor_pointer())
                        .on_click(cx.listener(move |this, _, _, cx| {
                            if editable && dirty {
                                this.save_security_settings(cx);
                            }
                        }))
                        .child(if native.security_busy {
                            locale.text("ui.saving")
                        } else {
                            locale.text("ui.saveSecuritySettings")
                        }),
                ),
        )
        .into_any_element()
}

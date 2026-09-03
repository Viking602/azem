use super::*;
pub(super) fn settings_appearance_body(
    state: &AppState,
    native: &NativeSettings,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let preferences = AppearancePreferences::current(cx);
    let reduced_motion = preferences.reduced_motion;
    let theme_names = [
        locale.text("ui.warm"),
        locale.text("ui.dark"),
        locale.text("ui.system"),
    ];
    let themes = div()
        .flex()
        .items_start()
        .gap(px(14.))
        .children(theme_names.into_iter().enumerate().map(|(index, name)| {
            settings_theme_preview(index, name, &preferences.theme, palette, cx)
        }))
        .into_any_element();
    let primary = div()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .child(settings_children_row(
            locale.text("ui.interfaceLanguage"),
            locale.text("ui.menusButtonsAndSystemMessages"),
            settings_language_control(state, native, locale, palette, cx),
            palette,
        ))
        .child(settings_children_row(
            locale.text("ui.theme"),
            locale.text("ui.systemModeFollowsLightAndDarkAppearance"),
            themes,
            palette,
        ))
        .child(settings_children_row(
            locale.text("ui.interfaceFont"),
            locale.text("ui.chooseFromInstalledFontsCodeRemainsMonospaced"),
            settings_font_family_control(native, &preferences, locale, palette, cx),
            palette,
        ))
        .child(settings_children_row(
            locale.text("ui.interfaceFontSize"),
            locale.text("ui.adjustSidebarSettingsAndOtherUiText"),
            settings_font_size_control(
                "uiFontSize",
                preferences.ui_font_size,
                11.,
                20.,
                locale,
                palette,
                cx,
            ),
            palette,
        ))
        .child(settings_children_row(
            locale.text("ui.reduceMotion"),
            locale.text("ui.makeTransitionsAndStreamingUpdatesImmediate"),
            div()
                .id("appearance-reduced-motion")
                .role(Role::Switch)
                .aria_label(locale.text("ui.reduceMotion"))
                .aria_toggled(reduced_motion.into())
                .tab_stop(true)
                .cursor_pointer()
                .on_click(cx.listener(move |this, _, window, cx| {
                    this.set_appearance("reducedMotion", json!(!reduced_motion), window, cx)
                }))
                .child(settings_switch(reduced_motion, palette))
                .into_any_element(),
            palette,
        ));
    let chat = settings_detail_card(
        locale.text("ui.chatText"),
        locale.text("ui.onlyAffectsTranscriptAndCodeBlockSizes"),
        vec![
            settings_children_row(
                locale.text("ui.uiText"),
                locale.text("ui.messageBubblesAssistantTextLabelsAndComposer"),
                settings_font_size_control(
                    "chatFontSize",
                    preferences.chat_font_size,
                    12.,
                    20.,
                    locale,
                    palette,
                    cx,
                ),
                palette,
            )
            .into_any_element(),
            settings_children_row(
                locale.text("ui.codeFontSize"),
                locale.text("ui.monospacedSizeForFencedCodeBlocks"),
                settings_font_size_control(
                    "codeFontSize",
                    preferences.code_font_size,
                    11.,
                    18.,
                    locale,
                    palette,
                    cx,
                ),
                palette,
            )
            .into_any_element(),
        ],
        palette,
    );
    div()
        .w_full()
        .flex()
        .flex_col()
        .gap_4()
        .child(primary)
        .child(chat)
        .into_any_element()
}

fn settings_theme_preview(
    index: usize,
    name: &'static str,
    theme: &str,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let value = ["light", "dark", "system"][index];
    let selected = theme == value;
    let (preview_color, border_color, sidebar_color, paper_color, line_color) = match index {
        1 => (
            rgb(0x151613),
            rgb(0x343530),
            rgb(0x2d2e28),
            rgb(0x20211d),
            rgb(0x44463f),
        ),
        _ => (
            rgb(0xf5f4ef),
            pick(selected, palette.accent, palette.border),
            rgb(0xe3e2dc),
            rgb(0xffffff),
            rgb(0xd4d3cd),
        ),
    };
    let preview = div()
        .w(px(54.))
        .h(px(42.))
        .relative()
        .overflow_hidden()
        .rounded(px(8.))
        .border_1()
        .border_color(border_color)
        .bg(preview_color)
        .when(index == 2, |preview| {
            preview.child(
                div()
                    .absolute()
                    .top_0()
                    .right_0()
                    .bottom_0()
                    .w(px(27.))
                    .bg(rgb(0x151613)),
            )
        })
        .child(
            div()
                .absolute()
                .top(px(7.))
                .bottom(px(7.))
                .left(px(8.))
                .w(px(14.))
                .rounded(px(3.))
                .bg(sidebar_color),
        )
        .child(
            div()
                .absolute()
                .top(px(7.))
                .right(px(8.))
                .bottom(px(7.))
                .left(px(22.))
                .rounded(px(3.))
                .bg(paper_color),
        )
        .child(
            div()
                .absolute()
                .top(px(13.))
                .right(px(10.))
                .left(px(28.))
                .h(px(4.))
                .rounded_full()
                .bg(line_color),
        )
        .child(
            div()
                .absolute()
                .top(px(22.))
                .right(px(10.))
                .left(px(28.))
                .h(px(4.))
                .rounded_full()
                .bg(line_color),
        );
    div()
        .id(("theme-preview", index))
        .w(px(64.))
        .p(px(3.))
        .rounded(px(9.))
        .border_1()
        .border_color(pick(selected, palette.accent, palette.paper))
        .bg(palette.paper)
        .role(Role::RadioButton)
        .aria_label(name)
        .aria_selected(selected)
        .tab_stop(true)
        .cursor_pointer()
        .on_click(cx.listener(move |this, _, window, cx| {
            this.set_appearance("theme", json!(value), window, cx)
        }))
        .flex()
        .flex_col()
        .items_center()
        .gap_1()
        .child(preview)
        .child(
            div()
                .text_size(px(10.))
                .text_color(palette.muted)
                .child(name),
        )
        .into_any_element()
}

fn settings_language_control(
    state: &AppState,
    native: &NativeSettings,
    locale: Locale,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let languages = crate::localization::available();
    let selected = languages
        .iter()
        .position(|pack| pack.id == locale.id())
        .unwrap_or(0);
    let enabled = state.connection.connected && !native.language_saving;
    div()
        .id("language-control")
        .relative()
        .w(px(200.))
        .child(
            div()
                .id("language-picker")
                .role(Role::ComboBox)
                .aria_label(locale.text("ui.interfaceLanguage"))
                .aria_value(languages[selected].name.clone())
                .aria_expanded(native.language_menu_open)
                .tab_stop(enabled)
                .track_focus(&native.language_focus)
                .h(px(40.))
                .px_3()
                .rounded(px(9.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper)
                .flex()
                .items_center()
                .gap_2()
                .when(enabled, |button| {
                    button
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                })
                .on_click(cx.listener(move |this, _, window, cx| {
                    if enabled {
                        this.native_settings.language_focus.focus(window, cx);
                        this.native_settings.language_menu_open =
                            !this.native_settings.language_menu_open;
                        this.native_settings.language_index = selected;
                        this.native_settings
                            .language_scroll
                            .scroll_to_item(selected);
                        this.native_settings.font_menu_open = false;
                        cx.notify();
                    }
                }))
                .capture_key_down(cx.listener(
                    move |this, event: &gpui::KeyDownEvent, window, cx| {
                        if !enabled {
                            return;
                        }
                        match event.keystroke.key.as_str() {
                            "up" | "down" => {
                                let count = languages.len();
                                if !this.native_settings.language_menu_open {
                                    this.native_settings.language_menu_open = true;
                                    this.native_settings.language_index = selected;
                                } else {
                                    let step = if event.keystroke.key == "down" {
                                        1
                                    } else {
                                        count - 1
                                    };
                                    this.native_settings.language_index =
                                        (this.native_settings.language_index + step) % count;
                                }
                            }
                            "enter" | "space" => {
                                if this.native_settings.language_menu_open {
                                    let index = this
                                        .native_settings
                                        .language_index
                                        .min(languages.len() - 1);
                                    this.change_language(&languages[index].id, cx);
                                } else {
                                    this.native_settings.language_menu_open = true;
                                    this.native_settings.language_index = selected;
                                }
                            }
                            "escape" => this.native_settings.language_menu_open = false,
                            "tab" => {
                                this.native_settings.language_menu_open = false;
                                cx.notify();
                                return;
                            }
                            _ => return,
                        }
                        this.native_settings
                            .language_scroll
                            .scroll_to_item(this.native_settings.language_index);
                        this.native_settings.language_focus.focus(window, cx);
                        window.prevent_default();
                        cx.stop_propagation();
                        cx.notify();
                    },
                ))
                .child(
                    div()
                        .flex_1()
                        .truncate()
                        .child(languages[selected].name.clone()),
                )
                .child(icon(
                    if native.language_saving {
                        "loader"
                    } else {
                        "chevron-down"
                    },
                    14.,
                    palette.faint,
                )),
        )
        .when(native.language_menu_open, |control| {
            control.child(
                deferred(
                    div()
                        .id("language-options")
                        .on_mouse_down_out(cx.listener(AzemWindow::dismiss_picker))
                        .role(Role::ListBox)
                        .aria_label(locale.text("ui.interfaceLanguage"))
                        .absolute()
                        .top(px(44.))
                        .right_0()
                        .w(px(200.))
                        .max_h(px(260.))
                        .overflow_y_scroll()
                        .track_scroll(&native.language_scroll)
                        .rounded(px(9.))
                        .border_1()
                        .border_color(palette.border_strong)
                        .bg(palette.paper)
                        .occlude()
                        .p_1()
                        .shadow(vec![
                            BoxShadow::new(px(0.), px(8.), hsla(0., 0., 0., 0.15))
                                .blur_radius(px(20.)),
                        ])
                        .children(languages.iter().enumerate().map(|(index, pack)| {
                            div()
                                .id(("language-option", index))
                                .role(Role::ListBoxOption)
                                .aria_label(pack.name.clone())
                                .aria_selected(index == selected)
                                .h(px(36.))
                                .px_2()
                                .rounded(px(6.))
                                .flex()
                                .items_center()
                                .gap_2()
                                .bg(if index == native.language_index {
                                    palette.hover
                                } else {
                                    palette.paper
                                })
                                .cursor_pointer()
                                .hover(move |style| style.bg(palette.hover))
                                .on_click(cx.listener(move |this, _, window, cx| {
                                    this.change_language(&pack.id, cx);
                                    this.native_settings.language_focus.focus(window, cx);
                                }))
                                .child(div().flex_1().truncate().child(pack.name.clone()))
                                .when(index == selected, |row| {
                                    row.child(icon("check", 14., palette.accent))
                                })
                        })),
                )
                .with_priority(30),
            )
        })
        .into_any_element()
}

fn settings_font_family_control(
    native: &NativeSettings,
    preferences: &AppearancePreferences,
    locale: Locale,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let family = preferences.font.clone();
    let selected_label = native
        .fonts
        .iter()
        .find(|font| font["family"].as_str() == Some(&family))
        .and_then(|font| font["label"].as_str())
        .unwrap_or(&family)
        .to_string();
    let query = native.font_search.read(cx).text().trim().to_lowercase();
    let mut fonts = vec![json!({"family":"system", "label":locale.text("ui.systemDefault")})];
    fonts.extend(
        native
            .fonts
            .iter()
            .filter(|font| {
                format!(
                    "{} {}",
                    font["family"].as_str().unwrap_or_default(),
                    font["label"].as_str().unwrap_or_default()
                )
                .to_lowercase()
                .contains(&query)
            })
            .cloned(),
    );
    div()
        .id("appearance-font-control")
        .relative()
        .w(px(200.))
        .child(
            div()
                .id("appearance-font-picker")
                .role(Role::Button)
                .aria_label(locale.text("ui.interfaceFont"))
                .aria_expanded(native.font_menu_open)
                .tab_stop(true)
                .min_h(px(42.))
                .px_3()
                .py_1()
                .rounded(px(9.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper)
                .flex()
                .items_center()
                .gap_2()
                .cursor_pointer()
                .on_click(cx.listener(|this, _, window, cx| {
                    this.native_settings.font_menu_open = !this.native_settings.font_menu_open;
                    this.native_settings.language_menu_open = false;
                    if this.native_settings.font_menu_open {
                        this.native_settings
                            .font_search
                            .focus_handle(cx)
                            .focus(window, cx);
                        if this.native_settings.fonts.is_empty()
                            && !this.native_settings.fonts_loading
                        {
                            this.request_system_fonts();
                        }
                    }
                    cx.notify();
                }))
                .child(div().text_color(palette.muted).child("Aa"))
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .truncate()
                        .child(if family == "system" {
                            locale.text("ui.systemDefault").to_string()
                        } else {
                            selected_label
                        }),
                )
                .child(icon("chevron-down", 12., palette.faint)),
        )
        .when(native.font_menu_open, |control| {
            control.child(
                deferred(
                    div()
                        .id("appearance-font-menu")
                        .on_mouse_down_out(cx.listener(AzemWindow::dismiss_picker))
                        .role(Role::RadioGroup)
                        .absolute()
                        .top(px(46.))
                        .right_0()
                        .w(px(200.))
                        .rounded(px(9.))
                        .border_1()
                        .border_color(palette.border_strong)
                        .bg(palette.paper)
                        .occlude()
                        .p_2()
                        .shadow(vec![
                            BoxShadow::new(px(0.), px(8.), hsla(0., 0., 0., 0.15))
                                .blur_radius(px(20.)),
                        ])
                        .child(
                            div()
                                .h(px(34.))
                                .border_b_1()
                                .border_color(palette.border)
                                .child(native.font_search.clone()),
                        )
                        .child(
                            div()
                                .id("appearance-font-list")
                                .h(px(210.))
                                .overflow_y_scroll()
                                .when(native.fonts_loading, |list| {
                                    list.child(locale.text("ui.loadingFonts"))
                                })
                                .children(fonts.into_iter().enumerate().map(|(index, font)| {
                                    let value =
                                        font["family"].as_str().unwrap_or_default().to_string();
                                    let label =
                                        font["label"].as_str().unwrap_or(&value).to_string();
                                    let selected = family == value;
                                    div()
                                        .id(("appearance-font-option", index))
                                        .role(Role::RadioButton)
                                        .aria_label(label.clone())
                                        .aria_selected(selected)
                                        .tab_stop(true)
                                        .h(px(32.))
                                        .px_2()
                                        .rounded(px(5.))
                                        .flex()
                                        .items_center()
                                        .bg(if selected {
                                            palette.accent_soft
                                        } else {
                                            palette.paper
                                        })
                                        .hover(move |s| s.bg(palette.hover))
                                        .cursor_pointer()
                                        .on_click(cx.listener(move |this, _, window, cx| {
                                            this.set_appearance("font", json!(value), window, cx)
                                        }))
                                        .child(div().flex_1().truncate().child(label))
                                        .when(selected, |row| {
                                            row.child(icon("check", 12., palette.accent))
                                        })
                                })),
                        ),
                )
                .with_priority(30),
            )
        })
        .into_any_element()
}

#[allow(clippy::too_many_arguments)]
fn settings_font_size_control(
    key: &'static str,
    value: f32,
    min: f32,
    max: f32,
    locale: Locale,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let label = match key {
        "uiFontSize" => locale.text("ui.interfaceFontSize"),
        "chatFontSize" => locale.text("ui.chatFontSize"),
        _ => locale.text("ui.codeFontSize2"),
    };
    let button = |increase: bool, cx: &mut Context<AzemWindow>| {
        let enabled = if increase { value < max } else { value > min };
        div()
            .id(format!(
                "{key}-{}",
                if increase { "increase" } else { "decrease" }
            ))
            .role(Role::Button)
            .aria_label(format!("{} {label}", if increase { "+" } else { "−" }))
            .tab_stop(enabled)
            .w(px(44.))
            .h_full()
            .flex()
            .items_center()
            .justify_center()
            .text_color(if enabled {
                palette.ink_soft
            } else {
                palette.faint
            })
            .when(enabled, |button| {
                button.cursor_pointer().hover(move |s| s.bg(palette.hover))
            })
            .on_click(cx.listener(move |this, _, window, cx| {
                if enabled {
                    this.set_appearance(
                        key,
                        json!(value + if increase { 1. } else { -1. }),
                        window,
                        cx,
                    );
                }
            }))
            .child(if increase { "A+" } else { "A−" })
    };
    div()
        .w(px(160.))
        .h(px(34.))
        .rounded(px(8.))
        .border_1()
        .border_color(palette.border_strong)
        .bg(palette.paper)
        .overflow_hidden()
        .flex()
        .child(button(false, cx))
        .child(
            div()
                .flex_1()
                .h_full()
                .border_l_1()
                .border_r_1()
                .border_color(palette.border)
                .flex()
                .items_center()
                .justify_center()
                .text_sm()
                .child(format!("{value} px")),
        )
        .child(button(true, cx))
        .into_any_element()
}

pub(super) fn settings_children_row(
    title: &'static str,
    description: &'static str,
    control: gpui::AnyElement,
    palette: ThemePalette,
) -> gpui::Div {
    div()
        .min_h(px(76.))
        .px_4()
        .py_2()
        .border_t_1()
        .border_color(palette.border)
        .flex()
        .items_center()
        .gap_4()
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
        .child(control)
}

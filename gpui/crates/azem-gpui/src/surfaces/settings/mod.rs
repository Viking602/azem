use super::*;
mod appearance;
mod archive;
mod catalog;
mod extensions;
mod governance;
mod routes;
mod security;
mod subagents;
mod usage;

use appearance::{settings_appearance_body, settings_children_row};
use archive::settings_archive_body;
use catalog::{provider_display_name, provider_logo_id, settings_catalog_body};
use extensions::settings_extensions_body;
use governance::settings_governance_body;
use routes::settings_routes_body;
use security::settings_security_body;
use subagents::settings_subagents_body;
use usage::settings_usage_body;

pub(crate) use catalog::ModelCapabilityTooltip;
pub(super) use catalog::pick;
#[cfg(test)]
pub(super) use catalog::provider_quota_remaining;
pub(crate) use extensions::extension_confirmation;
#[cfg(test)]
pub(super) use extensions::{extension_safe_target, plugin_logo};
pub(super) use routes::settings_route_model_name;
pub(super) use usage::format_usage_duration;

#[cfg(test)]
pub(super) use archive::archived_session_groups;
#[cfg(test)]
pub(super) use catalog::{
    model_capability_label, model_discovery_request, model_matches_query, model_provider_action,
    provider_matches_query,
};
#[cfg(test)]
pub(super) use extensions::{
    extension_items, extension_matches, marketplace_action, marketplace_entries,
    plugin_import_action,
};
#[cfg(test)]
pub(super) use routes::{is_core_settings_route, settings_route_title};
#[cfg(test)]
pub(super) use usage::{
    format_usage_count, format_usage_exact, usage_activity_level, usage_heatmap,
};

#[allow(clippy::too_many_arguments)]
pub(crate) fn settings_surface(
    state: &AppState,
    palette: ThemePalette,
    section: &str,
    selected_provider: &str,
    catalog_searches: (Entity<TextInput>, Entity<TextInput>),
    scrolls: (UniformListScrollHandle, UniformListScrollHandle),
    route_picker: (Option<&RoutePickerTarget>, Option<gpui::AnyElement>),
    subagent_setting_menu: Option<SubagentSettingKind>,
    archive: (u32, bool, &HashSet<String>),
    usage_hover: Option<&(String, i64)>,
    extensions: &ExtensionSettings,
    native: &NativeSettings,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let (section, extension_tab) = settings_section_parts(section);
    let (provider_scroll, model_scroll) = scrolls;
    let (route_picker_target, route_picker) = route_picker;
    let locale = Locale::resolve(&state.settings.language);
    let settings_query = native.search.read(cx).text().to_string();
    let nav_groups = [
        (
            locale.text("ui.system2"),
            vec![
                ("catalog", locale.text("ui.modelCatalog"), "database"),
                ("routes", locale.text("ui.modelRouting"), "bot"),
                ("agents", locale.text("ui.subagents"), "gauge"),
                ("security", locale.text("ui.securityScan"), "shield-check"),
            ],
        ),
        (
            locale.text("ui.preferences"),
            vec![
                (
                    "governance",
                    locale.text("ui.governance"),
                    "sliders-horizontal",
                ),
                ("appearance", locale.text("ui.appearance"), "palette"),
                ("extensions", locale.text("ui.extensions"), "puzzle"),
                ("archive", locale.text("ui.archive"), "archive"),
                ("usage", locale.text("ui.usage"), "chart"),
            ],
        ),
    ];
    let (title, description) = match section {
        "routes" => (
            locale.text("ui.modelRouting"),
            locale.text("ui.inspectProvidersAndModelsUsedByTheMainAgentTeam"),
        ),
        "agents" => (
            locale.text("ui.subagents"),
            locale.text("ui.configureConcurrencyAndIsolationThenInspectSchedulingAndMainSession"),
        ),
        "security" => (
            locale.text("ui.securityScans"),
            locale.text("ui.reviewSecurityPolicyScanStateAndFindings"),
        ),
        "governance" => (
            locale.text("ui.governance"),
            locale.text("ui.controlToolApprovalAndHowFollowUpMessagesAreDelivered"),
        ),
        "appearance" => (
            locale.text("ui.appearance"),
            locale.text("ui.chooseTheDesktopLanguageAndInspectAvailableThemes"),
        ),
        "extensions" => (
            locale.text("ui.extensions"),
            locale.text("ui.reviewSkillsPluginsMcpServersAndHooks"),
        ),
        "archive" => (
            locale.text("ui.archive"),
            locale.text("ui.inspectDeterministicContextArchivesRecapsAndRecoveryState"),
        ),
        "usage" => (
            locale.text("ui.usage"),
            locale.text("ui.reviewCurrentSessionAndProviderUsageSignals"),
        ),
        _ => (
            locale.text("ui.modelCatalog"),
            locale
                .text("ui.manageSubscriptionModelsAndOpenaiCompatibleProvidersCredentialsRemainIn"),
        ),
    };
    let body = match section {
        "routes" => settings_routes_body(
            state,
            palette,
            locale,
            route_picker_target,
            route_picker,
            cx,
        ),
        "agents" => settings_subagents_body(state, palette, locale, subagent_setting_menu, cx),
        "security" => settings_security_body(state, native, palette, locale, cx),
        "governance" => settings_governance_body(state, palette, locale, cx),
        "appearance" => settings_appearance_body(state, native, palette, locale, cx),
        "extensions" => {
            settings_extensions_body(state, palette, locale, extension_tab, extensions, cx)
        }
        "archive" => settings_archive_body(state, palette, locale, archive, cx),
        "usage" => settings_usage_body(state, palette, locale, usage_hover, cx),
        _ => settings_catalog_body(
            state,
            palette,
            locale,
            selected_provider,
            catalog_searches,
            (provider_scroll, model_scroll),
            cx,
        ),
    };
    div()
        .id("settings-surface")
        .relative()
        .role(Role::Region)
        .aria_label(locale.text("ui.settings"))
        .flex_1()
        .min_w_0()
        .min_h_0()
        .h_full()
        .rounded(px(17.))
        .overflow_hidden()
        .flex()
        .child(
            div()
                .id("settings-navigation")
                .role(Role::Navigation)
                .aria_label(locale.text("ui.settingsCategories"))
                .w(px(220.))
                .h_full()
                .min_h_0()
                .border_r_1()
                .border_color(palette.border)
                .bg(palette.sidebar)
                .rounded_tl(px(13.))
                .rounded_bl(px(13.))
                .px_3()
                .pt(px(16.))
                .pb(px(14.))
                .flex()
                .flex_col()
                .gap_3()
                .child(
                    div()
                        .id("settings-back")
                        .role(Role::Button)
                        .aria_label(locale.text("ui.backToWorkspace"))
                        .tab_stop(true)
                        .h(px(34.))
                        .px_2()
                        .rounded(px(8.))
                        .text_color(palette.ink)
                        .text_sm()
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.settings_open = false;
                            this.archive_days_menu_open = false;
                            cx.notify();
                        }))
                        .child("←")
                        .child(locale.text("ui.backToWorkspace")),
                )
                .child(
                    div()
                        .h(px(34.))
                        .px_2()
                        .rounded(px(8.))
                        .border_1()
                        .border_color(palette.border)
                        .bg(palette.paper)
                        .text_color(palette.faint)
                        .text_xs()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(icon("search", 13., palette.faint))
                        .child(
                            div()
                                .flex_1()
                                .min_w_0()
                                .h_full()
                                .child(native.search.clone()),
                        )
                        .child(div().text_size(px(9.)).child("⌘F")),
                )
                .children(nav_groups.into_iter().map(|(group, entries)| {
                    div()
                        .flex()
                        .flex_col()
                        .gap_1()
                        .child(
                            div()
                                .h(px(24.))
                                .px_2()
                                .text_color(palette.faint)
                                .text_size(px(10.))
                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                .flex()
                                .items_center()
                                .child(group),
                        )
                        .children(
                            entries
                                .into_iter()
                                .filter(|(id, label, _)| {
                                    settings_search_matches(id, label, &settings_query, locale)
                                })
                                .map(|(id, label, icon_name)| {
                                    settings_navigation_item(
                                        id,
                                        label,
                                        icon_name,
                                        section == id,
                                        palette,
                                        cx,
                                    )
                                }),
                        )
                }))
                .child(div().flex_1())
                .child(
                    div()
                        .h(px(32.))
                        .px_2()
                        .border_t_1()
                        .border_color(palette.border)
                        .text_color(palette.faint)
                        .text_size(px(9.))
                        .flex()
                        .items_center()
                        .child(locale.text("ui.settingsStoredLocally"))
                        .child(div().flex_1())
                        .child(format!("Azem v{}", env!("CARGO_PKG_VERSION"))),
                ),
        )
        .child(
            div()
                .id("settings-content")
                .flex_1()
                .min_w_0()
                .min_h_0()
                .h_full()
                .bg(palette.paper)
                .rounded_tr(px(13.))
                .rounded_br(px(13.))
                .when(section == "catalog", |content| content.overflow_hidden())
                .when(section != "catalog", |content| content.overflow_y_scroll())
                .px(px(34.))
                .pt(px(28.))
                .pb(px(if section == "catalog" { 16. } else { 64. }))
                .flex()
                .flex_col()
                .gap_5()
                .child(
                    div()
                        .flex()
                        .items_start()
                        .gap_4()
                        .child(
                            div()
                                .flex_1()
                                .flex()
                                .flex_col()
                                .gap_2()
                                .child(
                                    div()
                                        .text_color(palette.ink)
                                        .text_size(px(26.))
                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                        .child(title),
                                )
                                .child(
                                    div()
                                        .max_w(px(680.))
                                        .text_color(palette.muted)
                                        .text_sm()
                                        .line_height(px(21.))
                                        .child(description),
                                ),
                        )
                        .when(section == "catalog", |header| {
                            header.child(
                                div()
                                    .id("add-model-provider")
                                    .role(Role::Button)
                                    .aria_label(locale.text("ui.addProvider"))
                                    .tab_stop(true)
                                    .h(px(38.))
                                    .px_3()
                                    .rounded(px(9.))
                                    .bg(palette.button)
                                    .text_color(palette.button_text)
                                    .text_sm()
                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                    .flex()
                                    .items_center()
                                    .gap_2()
                                    .cursor_pointer()
                                    .on_click(cx.listener(|this, _, _, cx| {
                                        this.settings_provider = this
                                            .state
                                            .catalogs
                                            .providers
                                            .iter()
                                            .find(|provider| {
                                                !provider
                                                    .get("enabled")
                                                    .and_then(serde_json::Value::as_bool)
                                                    .unwrap_or(false)
                                            })
                                            .and_then(|provider| provider.get("id"))
                                            .and_then(serde_json::Value::as_str)
                                            .map(str::to_string);
                                        if this.settings_provider.is_none() {
                                            this.refresh_model_catalog();
                                        }
                                        cx.notify();
                                    }))
                                    .child("+")
                                    .child(locale.text("ui.addProvider")),
                            )
                        }),
                )
                .child(body),
        )
        .when(!state.settings.error.is_empty(), |root| {
            root.child(
                div()
                    .id("settings-error")
                    .role(Role::Alert)
                    .aria_label(state.settings.error.to_string())
                    .absolute()
                    .bottom(px(10.))
                    .left(px(232.))
                    .right(px(20.))
                    .px_3()
                    .py_2()
                    .rounded(px(8.))
                    .border_1()
                    .border_color(palette.danger)
                    .bg(palette.paper)
                    .text_color(palette.danger)
                    .text_sm()
                    .occlude()
                    .flex()
                    .items_center()
                    .gap_3()
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .child(state.settings.error.to_string()),
                    )
                    .child(
                        div()
                            .id("dismiss-settings-error")
                            .role(Role::Button)
                            .aria_label(locale.text("ui.dismissError"))
                            .tab_stop(true)
                            .cursor_pointer()
                            .p_1()
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.state.settings.error = "".into();
                                cx.notify();
                            }))
                            .child("×"),
                    ),
            )
        })
        .into_any_element()
}

pub(super) fn settings_section_parts(section: &str) -> (&str, &str) {
    section
        .strip_prefix("extensions:")
        .map_or((section, "mcp"), |tab| ("extensions", tab))
}

pub(super) fn settings_search_matches(
    section: &str,
    label: &str,
    query: &str,
    locale: Locale,
) -> bool {
    let key = match section {
        "catalog" => "search.catalog",
        "routes" => "search.routes",
        "agents" => "search.agents",
        "security" => "search.security",
        "governance" => "search.governance",
        "appearance" => "search.appearance",
        "extensions" => "search.extensions",
        "archive" => "search.archive",
        "usage" => "search.usage",
        _ => "",
    };
    let keywords = locale.text(key);
    let haystack = format!("{label} {keywords}").to_lowercase();
    query
        .split_whitespace()
        .all(|word| haystack.contains(&word.to_lowercase()))
}

fn settings_navigation_item(
    id: &'static str,
    label: &'static str,
    icon_name: &'static str,
    selected: bool,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    div()
        .id(format!("settings-nav-{id}"))
        .role(Role::Button)
        .aria_label(label)
        .aria_selected(selected)
        .tab_stop(true)
        .h(px(38.))
        .px_2()
        .rounded(px(8.))
        .bg(pick(selected, palette.hover, palette.sidebar))
        .text_color(pick(selected, palette.ink, palette.muted))
        .text_sm()
        .font_weight(pick(
            selected,
            gpui::FontWeight::MEDIUM,
            gpui::FontWeight::NORMAL,
        ))
        .flex()
        .items_center()
        .gap_2()
        .cursor_pointer()
        .hover(move |style| style.bg(palette.hover))
        .on_click(cx.listener(move |this, _, _, cx| {
            this.select_settings_section(id, cx);
        }))
        .child(icon(
            icon_name,
            15.,
            pick(selected, palette.accent, palette.faint),
        ))
        .child(label)
        .into_any_element()
}

fn settings_detail_card(
    title: &'static str,
    description: &'static str,
    rows: Vec<gpui::AnyElement>,
    palette: ThemePalette,
) -> gpui::AnyElement {
    div()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .overflow_hidden()
        .child(settings_card_header(title, description, palette))
        .children(rows)
        .into_any_element()
}

fn settings_unclipped_detail_card(
    title: &'static str,
    description: &'static str,
    rows: Vec<gpui::AnyElement>,
    palette: ThemePalette,
) -> gpui::AnyElement {
    div()
        .relative()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .child(settings_card_header(title, description, palette))
        .children(rows)
        .into_any_element()
}

fn settings_detail_row(
    title: &'static str,
    description: &'static str,
    value: String,
    palette: ThemePalette,
) -> gpui::AnyElement {
    settings_control_detail_row(
        title,
        description,
        div()
            .min_w(px(112.))
            .h(px(34.))
            .px_3()
            .rounded(px(8.))
            .border_1()
            .border_color(palette.border_strong)
            .bg(palette.paper)
            .text_color(palette.ink_soft)
            .text_sm()
            .font_weight(gpui::FontWeight::SEMIBOLD)
            .flex()
            .items_center()
            .justify_center()
            .child(value)
            .into_any_element(),
        palette,
    )
}

fn settings_control_detail_row(
    title: &'static str,
    description: &'static str,
    control: gpui::AnyElement,
    palette: ThemePalette,
) -> gpui::AnyElement {
    div()
        .min_h(px(66.))
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
                        .text_color(palette.ink)
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(title),
                )
                .child(
                    div()
                        .text_color(palette.muted)
                        .text_xs()
                        .line_height(px(18.))
                        .child(description),
                ),
        )
        .child(control)
        .into_any_element()
}

fn settings_switch(on: bool, palette: ThemePalette) -> gpui::Div {
    div()
        .w(px(34.))
        .h(px(20.))
        .p(px(2.))
        .rounded_full()
        .bg(if on {
            palette.positive
        } else {
            palette.border_strong
        })
        .flex()
        .justify_end()
        .when(!on, |switch| switch.justify_start())
        .child(
            div()
                .size(px(16.))
                .rounded_full()
                .bg(rgb(0xffffff))
                .shadow(vec![
                    BoxShadow::new(px(0.), px(1.), hsla(0., 0., 0., 0.18)).blur_radius(px(2.)),
                ]),
        )
}

pub(super) fn settings_card_header(
    title: &'static str,
    description: &'static str,
    palette: ThemePalette,
) -> gpui::Div {
    div()
        .min_h(px(64.))
        .px_4()
        .py_3()
        .flex()
        .flex_col()
        .justify_center()
        .gap_1()
        .child(
            div()
                .text_color(palette.ink)
                .text_sm()
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .child(title),
        )
        .child(
            div()
                .text_color(palette.muted)
                .text_xs()
                .line_height(px(18.))
                .child(description),
        )
}

fn settings_empty_card(label: &'static str, palette: ThemePalette) -> gpui::AnyElement {
    div()
        .h(px(148.))
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .text_color(palette.faint)
        .text_sm()
        .flex()
        .items_center()
        .justify_center()
        .child(label)
        .into_any_element()
}

fn inventory_label(value: &serde_json::Value, index: usize, locale: Locale) -> String {
    [
        "label",
        "name",
        "displayName",
        "title",
        "id",
        "role",
        "scope",
        "path",
        "command",
        "provider",
    ]
    .into_iter()
    .find_map(|key| {
        value
            .get(key)
            .and_then(serde_json::Value::as_str)
            .filter(|value| !value.trim().is_empty())
    })
    .map(str::to_string)
    .unwrap_or_else(|| locale.format("inventory.item", &[("number", (index + 1).to_string())]))
}

fn settings_action_button(
    id: usize,
    label: &'static str,
    enabled: bool,
    selected: bool,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
    payload: serde_json::Value,
) -> gpui::AnyElement {
    let selectable = id < 20;
    div()
        .id(("settings-action", id))
        .role(pick(selectable, Role::RadioButton, Role::Button))
        .aria_label(label)
        .aria_selected(selected)
        .tab_stop(enabled)
        .h(px(32.))
        .px(px(10.))
        .rounded(px(7.))
        .bg(pick(selected, palette.paper_muted, palette.paper))
        .border_1()
        .border_color(pick(selected, palette.border_strong, palette.paper))
        .when(selected, |button| {
            button.shadow(vec![
                BoxShadow::new(px(0.), px(1.), hsla(220. / 360., 0.15, 0.15, 0.07))
                    .blur_radius(px(2.)),
            ])
        })
        .text_color(pick(enabled, palette.ink_soft, palette.faint))
        .text_xs()
        .font_weight(pick(
            selected,
            gpui::FontWeight::SEMIBOLD,
            gpui::FontWeight::NORMAL,
        ))
        .flex()
        .items_center()
        .justify_center()
        .when(selectable, |button| button.flex_1())
        .when(enabled, |button| {
            button
                .cursor_pointer()
                .hover(move |style| style.bg(palette.hover))
        })
        .on_click(cx.listener(move |this, _, _, cx| {
            if enabled {
                this.runtime.request(Method::Execute, payload.clone());
                cx.notify();
            }
        }))
        .child(label)
        .into_any_element()
}

use super::*;
pub(super) fn settings_routes_body(
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
    route_picker_target: Option<&RoutePickerTarget>,
    route_picker: Option<gpui::AnyElement>,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let core = state
        .catalogs
        .routes
        .iter()
        .filter(|route| {
            is_core_settings_route(
                route
                    .get("scope")
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or_default(),
            )
        })
        .cloned()
        .collect::<Vec<_>>();
    let subagents = state
        .catalogs
        .routes
        .iter()
        .filter(|route| route.get("scope").and_then(serde_json::Value::as_str) == Some("subagent"))
        .cloned()
        .collect::<Vec<_>>();
    let (core_picker, subagent_picker) =
        if route_picker_target.is_some_and(|target| target.scope == "subagent") {
            (None, route_picker)
        } else {
            (route_picker, None)
        };
    div()
        .w_full()
        .flex()
        .items_start()
        .gap_4()
        .child(settings_route_card(
            (
                locale.text("ui.coreWorkflows"),
                locale.text("ui.mainConversationAndCriticalDecisions"),
            ),
            core,
            state,
            (route_picker_target, core_picker),
            palette,
            locale,
            cx,
        ))
        .child(settings_route_card(
            (
                locale.text("ui.subagentDefaults"),
                locale.text("ui.rolesCanStillOverrideThisSetting"),
            ),
            subagents,
            state,
            (route_picker_target, subagent_picker),
            palette,
            locale,
            cx,
        ))
        .into_any_element()
}

pub(in crate::surfaces) fn is_core_settings_route(scope: &str) -> bool {
    !matches!(scope, "main" | "subagent" | "security")
}

fn settings_route_card(
    copy: (&'static str, &'static str),
    routes: Vec<serde_json::Value>,
    state: &AppState,
    route_picker: (Option<&RoutePickerTarget>, Option<gpui::AnyElement>),
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let (title, description) = copy;
    let (route_picker_target, mut route_picker) = route_picker;
    let empty = routes.is_empty();
    let has_picker = route_picker.is_some();
    let mut rows = Vec::with_capacity(routes.len());
    for route in routes {
        let scope = route
            .get("scope")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default();
        let role = route
            .get("role")
            .and_then(serde_json::Value::as_str)
            .unwrap_or_default();
        let expanded_kind = route_picker_target
            .filter(|target| target.scope == scope && target.role == role)
            .map(|target| target.kind);
        let row_picker = expanded_kind.and_then(|_| route_picker.take());
        rows.push(settings_route_row(
            route,
            state,
            expanded_kind,
            row_picker,
            palette,
            locale,
            cx,
        ));
    }
    div()
        .flex_1()
        .min_w(px(320.))
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .when(!has_picker, |card| card.overflow_hidden())
        .child(settings_card_header(title, description, palette))
        .when(empty, |card| {
            card.child(
                div()
                    .h(px(72.))
                    .border_t_1()
                    .border_color(palette.border)
                    .text_color(palette.faint)
                    .text_sm()
                    .flex()
                    .items_center()
                    .justify_center()
                    .child(locale.text("ui.noModelRoutes")),
            )
        })
        .children(rows)
        .into_any_element()
}

pub(in crate::surfaces) fn settings_route_title(
    scope: &str,
    role: &str,
    label: &str,
    locale: Locale,
) -> String {
    match scope {
        "main" => locale.text("ui.main").to_string(),
        "title" => locale.text("ui.conversationTitle").into(),
        "plan" => locale.text("ui.planningModel").into(),
        "approval" => locale.text("ui.approvalModel").into(),
        "vision" => locale.text("ui.visionModel").into(),
        "recap" => locale.text("ui.recapModel").into(),
        _ if role == "research" => locale.text("ui.researchAndDocumentation").into(),
        _ if role == "review" => locale.text("ui.codingAndReview").into(),
        _ if !role.is_empty() => role.to_string(),
        _ => label.to_string(),
    }
}

fn settings_route_provider_name(providers: &[serde_json::Value], provider_id: &str) -> String {
    providers
        .iter()
        .find(|provider| {
            provider.get("id").and_then(serde_json::Value::as_str) == Some(provider_id)
        })
        .and_then(|provider| {
            ["displayName", "name"]
                .into_iter()
                .find_map(|key| provider.get(key).and_then(serde_json::Value::as_str))
        })
        .unwrap_or(provider_id)
        .to_string()
}

pub(in crate::surfaces) fn settings_route_model_name(
    providers: &[serde_json::Value],
    provider_id: &str,
    model_id: &str,
) -> String {
    providers
        .iter()
        .find(|provider| {
            provider.get("id").and_then(serde_json::Value::as_str) == Some(provider_id)
        })
        .and_then(|provider| provider.get("models"))
        .and_then(serde_json::Value::as_array)
        .and_then(|models| {
            models
                .iter()
                .find(|model| model.get("id").and_then(serde_json::Value::as_str) == Some(model_id))
        })
        .map(|model| catalog_model_name(model, model_id))
        .unwrap_or_else(|| model_id.to_string())
}

fn settings_route_row(
    route: serde_json::Value,
    state: &AppState,
    expanded_kind: Option<RoutePickerKind>,
    route_picker: Option<gpui::AnyElement>,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let providers = &state.catalogs.providers;
    let scope = route
        .get("scope")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let role = route
        .get("role")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let label = route
        .get("label")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
        .to_string();
    let title = settings_route_title(scope, role, &label, locale);
    let description = match scope {
        "main" => locale.text("ui.mainConversationAndTools"),
        "title" => locale.text("ui.generateTheSidebarTitleFromTheFirstUserMessage"),
        "plan" => locale.text("ui.planningAndTaskDecomposition"),
        "approval" => locale.text("ui.structuredApprovalReview"),
        "vision" => locale.text("ui.imageAndVisualRouting"),
        "recap" => locale.text("ui.briefPostTurnRecap"),
        _ if role == "research" => locale.text("ui.researchAndDocumentation2"),
        _ if role == "review" => locale.text("ui.implementationAndReview"),
        _ => "",
    }
    .to_string();
    let description = if description.is_empty() && !label.is_empty() && label != role {
        label.clone()
    } else if description.is_empty() {
        locale.text("ui.defaultRoleRoute").to_string()
    } else {
        description
    };
    let route_value = route.get("route").unwrap_or(&route);
    let configured_provider = route_value
        .get("provider")
        .and_then(serde_json::Value::as_str)
        .filter(|value| !value.is_empty())
        .unwrap_or(state.settings.provider.as_ref());
    let configured_model = route_value
        .get("model")
        .and_then(serde_json::Value::as_str)
        .filter(|value| !value.is_empty())
        .unwrap_or(state.settings.model.as_ref());
    let provider = configured_provider.to_string();
    let model = settings_route_model_name(providers, configured_provider, configured_model);
    let provider_name = settings_route_provider_name(providers, configured_provider);
    let reasoning = route_value
        .get("reasoning")
        .and_then(serde_json::Value::as_str)
        .filter(|value| !value.is_empty())
        .unwrap_or(state.settings.reasoning.as_ref());
    let reasoning = Some(reasoning)
        .map(|value| match value {
            "low" => locale.text("ui.low"),
            "medium" => locale.text("ui.medium"),
            "high" => locale.text("ui.high"),
            "xhigh" => locale.text("ui.xhigh"),
            "max" | "ultra" => locale.text("ui.max"),
            _ => value,
        })
        .unwrap_or("—")
        .to_string();
    let route_id = format!("{}-{}", scope, role);
    let model_aria = format!("{} {}", title, locale.text("ui.model"));
    let reasoning_aria = format!("{} {}", title, locale.text("ui.reasoning"));
    let model_scope = scope.to_string();
    let model_role = role.to_string();
    let model_label = label.clone();
    let reasoning_scope = scope.to_string();
    let reasoning_role = role.to_string();
    let reasoning_label = label;
    div()
        .id(format!("settings-route-{route_id}"))
        .min_h(px(62.))
        .px_3()
        .py_2()
        .border_t_1()
        .border_color(palette.border)
        .flex()
        .items_center()
        .gap_3()
        .child(provider_logo(&provider, 19., palette.ink))
        .child(
            div()
                .min_w_0()
                .flex_1()
                .flex()
                .flex_col()
                .gap_1()
                .child(
                    div()
                        .truncate()
                        .text_color(palette.ink)
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(title),
                )
                .child(
                    div()
                        .truncate()
                        .text_color(palette.faint)
                        .text_xs()
                        .child(description),
                ),
        )
        .child(
            div()
                .relative()
                .w(px(270.))
                .flex()
                .items_center()
                .gap(px(6.))
                .child(
                    settings_route_value(
                        model,
                        provider_name,
                        px(176.),
                        expanded_kind == Some(RoutePickerKind::Model),
                        palette,
                    )
                    .id(format!("settings-route-model-{route_id}"))
                    .role(Role::Button)
                    .aria_label(model_aria)
                    .tab_stop(true)
                    .cursor_pointer()
                    .hover(move |style| style.bg(palette.hover))
                    .on_click(cx.listener(move |this, _, window, cx| {
                        this.open_route_picker(
                            model_scope.clone(),
                            model_role.clone(),
                            model_label.clone(),
                            RoutePickerKind::Model,
                            window,
                            cx,
                        );
                    })),
                )
                .child(
                    settings_route_value(
                        reasoning,
                        String::new(),
                        px(88.),
                        expanded_kind == Some(RoutePickerKind::Reasoning),
                        palette,
                    )
                    .id(format!("settings-route-reasoning-{route_id}"))
                    .role(Role::Button)
                    .aria_label(reasoning_aria)
                    .tab_stop(true)
                    .cursor_pointer()
                    .hover(move |style| style.bg(palette.hover))
                    .on_click(cx.listener(move |this, _, window, cx| {
                        this.open_route_picker(
                            reasoning_scope.clone(),
                            reasoning_role.clone(),
                            reasoning_label.clone(),
                            RoutePickerKind::Reasoning,
                            window,
                            cx,
                        );
                    })),
                )
                .when_some(route_picker, |controls, picker| {
                    controls.child(deferred(picker).with_priority(10))
                }),
        )
        .into_any_element()
}

fn settings_route_value(
    primary: String,
    secondary: String,
    width: Pixels,
    expanded: bool,
    palette: ThemePalette,
) -> gpui::Div {
    div()
        .w(width)
        .h(px(40.))
        .px_2()
        .rounded(px(8.))
        .bg(palette.paper_muted)
        .text_color(palette.muted)
        .flex()
        .items_center()
        .gap_2()
        .child(
            div()
                .min_w_0()
                .flex_1()
                .flex()
                .flex_col()
                .child(
                    div()
                        .truncate()
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(primary),
                )
                .when(!secondary.is_empty(), |value| {
                    value.child(div().truncate().text_xs().child(secondary))
                }),
        )
        .child(icon(
            if expanded {
                "chevron-up"
            } else {
                "chevron-down"
            },
            12.,
            palette.muted,
        ))
}

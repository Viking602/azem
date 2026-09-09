use super::*;
pub(super) fn settings_extensions_body(
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
    selected_tab: &str,
    controls: &ExtensionSettings,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let mcp = state
        .catalogs
        .mcp
        .as_array()
        .map(Vec::as_slice)
        .unwrap_or_default();
    let marketplaces = extension_items(&state.catalogs.marketplace, "marketplaces");
    let hooks = extension_items(&state.catalogs.hooks, "commands");
    let enabled = state.connection.connected && !controls.busy;
    let query = controls.search.read(cx).text().trim().to_lowercase();
    let refresh = match selected_tab {
        "skills" => "reload_skills",
        "plugins" => "list_plugins",
        "marketplace" => "marketplace_list",
        "hooks" => "list_hooks",
        _ => "refresh_mcp",
    };
    let tabs = [
        ("mcp", "server", locale.text("ui.mcpServers"), mcp.len()),
        (
            "skills",
            "sparkles",
            locale.text("extension.skills"),
            state.catalogs.skills.len(),
        ),
        (
            "plugins",
            "puzzle",
            locale.text("ui.plugins"),
            state.catalogs.plugins.len(),
        ),
        (
            "marketplace",
            "blocks",
            locale.text("ui.marketplace"),
            marketplaces.len(),
        ),
        (
            "hooks",
            "wrench",
            locale.text("extension.hooks"),
            hooks.len(),
        ),
    ];
    let values = match selected_tab {
        "skills" => state.catalogs.skills.as_slice(),
        "plugins" => state.catalogs.plugins.as_slice(),
        "hooks" => hooks,
        "marketplace" => &[],
        _ => mcp,
    };
    let mut rows = Vec::new();
    for value in values
        .iter()
        .filter(|value| extension_matches(value, &query))
    {
        let name = extension_text(value, "name");
        let detail;
        let mut actions = div().flex().items_center().gap_2().flex_shrink_0();
        let toggle = |kind: &str, target: &str, on: bool| json!({"kind":kind,"target":target,"decision":(!on).to_string()});
        match selected_tab {
            "skills" => {
                let on = !extension_flag(value, "disabled");
                detail = format!(
                    "{} · {}\n{}",
                    if on {
                        locale.text("ui.enabled")
                    } else {
                        locale.text("ui.disabled")
                    },
                    extension_text(value, "sourcePath"),
                    extension_text(value, "description")
                );
                actions = actions.child(extension_button(
                    locale.text("ui.enableSkill"),
                    toggle("set_skill_enabled", name, on),
                    enabled && !name.is_empty(),
                    palette,
                    cx,
                ));
            }
            "plugins" => {
                detail = [
                    "description",
                    "origin",
                    "scope",
                    "version",
                    "status",
                    "warning",
                ]
                .into_iter()
                .map(|key| {
                    let text = extension_text(value, key);
                    match key {
                        "origin" | "scope" => locale.value("extension", text),
                        "status" => locale.value("status", text),
                        _ => text,
                    }
                })
                .filter(|s| !s.is_empty())
                .collect::<Vec<_>>()
                .join(" · ");
                if let Some(payload) = plugin_import_action(value) {
                    let importing = payload["decision"] == "true";
                    actions = actions.child(extension_button(
                        if importing {
                            locale.text("ui.import")
                        } else {
                            locale.text("ui.remove")
                        },
                        payload,
                        enabled,
                        palette,
                        cx,
                    ));
                }
            }
            "hooks" => {
                let on = extension_flag(value, "enabled");
                let trusted = extension_text(value, "origin") != "plugin"
                    || extension_flag(&state.catalogs.hooks, "trustHooks");
                let status = if !on {
                    locale.text("ui.disabled")
                } else if !trusted {
                    locale.text("ui.awaitingTrust")
                } else {
                    locale.text("ui.enabled")
                };
                detail = format!(
                    "{} · {} · {}\n{}\n{}",
                    extension_text(value, "event"),
                    extension_text(value, "matcher"),
                    status,
                    extension_text(value, "command"),
                    extension_text(value, "source")
                );
                let id = extension_text(value, "id");
                actions = actions.child(extension_button(
                    locale.text("ui.enableHook"),
                    toggle("set_hook_enabled", id, on),
                    enabled && !id.is_empty(),
                    palette,
                    cx,
                ));
            }
            _ => {
                detail = format!(
                    "{} · {} · {} {}\n{}\n{}",
                    locale.value("status", extension_text(value, "state")),
                    extension_text(value, "transport"),
                    value["toolCount"].as_u64().unwrap_or(0),
                    locale.text("ui.tools2"),
                    extension_safe_target(value),
                    extension_text(value, "error")
                );
                actions = actions
                    .child(extension_button(
                        locale.text("ui.enableMcp"),
                        toggle("set_mcp_enabled", name, extension_flag(value, "enabled")),
                        enabled,
                        palette,
                        cx,
                    ))
                    .child(extension_button(
                        locale.text("ui.reconnect"),
                        json!({"kind":"reconnect_mcp","target":name}),
                        enabled && extension_flag(value, "enabled"),
                        palette,
                        cx,
                    ));
                if extension_flag(value, "removable") {
                    actions = actions.child(extension_button(
                        locale.text("ui.remove"),
                        json!({"kind":"delete_mcp_server","target":name}),
                        enabled,
                        palette,
                        cx,
                    ));
                }
            }
        }
        rows.push(
            extension_row(
                name,
                &detail,
                (selected_tab == "plugins").then(|| plugin_mark(value, palette)),
                palette,
            )
            .child(actions)
            .into_any_element(),
        );
    }
    let mut body = div().flex().flex_col().gap_4();
    if selected_tab == "marketplace" {
        body = body.child(settings_marketplace_body(
            state, controls, &query, enabled, palette, locale, cx,
        ));
    } else {
        if selected_tab == "hooks" {
            body = body.child(extension_row(locale.text("ui.trustPluginHooks"),
                locale.text("ui.pluginHooksRunLocalCommandsOffByDefaultProjectAnd"), None, palette)
                .child(extension_button(locale.text("ui.trustPluginHooks"),
                    json!({"kind":"set_plugin_hooks_trusted","decision":(!extension_flag(&state.catalogs.hooks,"trustHooks")).to_string()}), enabled, palette, cx)));
            let sources = extension_items(&state.catalogs.hooks, "sources")
                .iter()
                .map(|source| {
                    let detail = format!(
                        "{} · {} {} · {}\n{}\n{}",
                        locale.value("extension", extension_text(source, "origin")),
                        source["hookCount"].as_u64().unwrap_or(0),
                        locale.text("extension.hooks"),
                        if extension_flag(source, "trusted") {
                            locale.text("ui.trusted")
                        } else {
                            locale.text("ui.untrusted")
                        },
                        extension_text(source, "source"),
                        extension_text(source, "warning")
                    );
                    extension_row(
                        extension_text(source, "name"),
                        &detail,
                        Some(plugin_mark(source, palette)),
                        palette,
                    )
                    .into_any_element()
                })
                .collect();
            body = body.child(extension_list(
                locale.text("ui.hookSources"),
                sources,
                palette,
                locale,
            ));
        }
        let title = match selected_tab {
            "skills" => locale.text("extension.skills"),
            "plugins" => locale.text("ui.plugins"),
            "hooks" => locale.text("ui.hookCommands"),
            _ => locale.text("ui.mcpServers"),
        };
        body = body.child(extension_list(title, rows, palette, locale));
    }
    let diagnostics = match selected_tab {
        "skills" => state.catalogs.skill_diagnostics.as_slice(),
        "plugins" => state.catalogs.plugin_diagnostics.as_slice(),
        "hooks" => extension_items(&state.catalogs.hooks, "diagnostics"),
        _ => &[],
    };
    if !diagnostics.is_empty() {
        body = body.child(extension_list(
            locale.text("ui.diagnostics"),
            diagnostics
                .iter()
                .enumerate()
                .map(|(index, value)| {
                    extension_row(
                        &inventory_label(value, index, locale),
                        extension_text(value, "message"),
                        None,
                        palette,
                    )
                    .into_any_element()
                })
                .collect(),
            palette,
            locale,
        ));
    }
    div()
        .w_full()
        .flex()
        .flex_col()
        .gap_4()
        .child(
            div()
                .id("extensions-tab-list")
                .role(Role::TabList)
                .h(px(42.))
                .p(px(3.))
                .rounded(px(10.))
                .border_1()
                .border_color(palette.border)
                .bg(palette.paper_muted)
                .flex()
                .items_center()
                .gap_1()
                .children(tabs.into_iter().map(|(tab, icon_name, label, count)| {
                    let selected = selected_tab == tab;
                    div()
                        .id(format!("extensions-tab-{tab}"))
                        .role(Role::Tab)
                        .aria_label(label)
                        .aria_selected(selected)
                        .tab_stop(true)
                        .h_full()
                        .px_3()
                        .rounded(px(8.))
                        .bg(if selected {
                            palette.paper
                        } else {
                            palette.paper_muted
                        })
                        .text_color(if selected { palette.ink } else { palette.muted })
                        .text_sm()
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                        .on_click(cx.listener(move |this, _, _, cx| {
                            this.settings_section = format!("extensions:{tab}");
                            this.extension_settings
                                .search
                                .update(cx, |input, cx| input.clear(cx));
                            cx.notify();
                        }))
                        .child(icon(icon_name, 14., palette.muted))
                        .child(label)
                        .child(
                            div()
                                .px_1()
                                .rounded_full()
                                .bg(palette.hover)
                                .text_size(px(9.))
                                .child(count.to_string()),
                        )
                })),
        )
        .child(
            div()
                .flex()
                .items_center()
                .gap_3()
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .h(px(36.))
                        .relative()
                        .px_3()
                        .pr(px(36.))
                        .rounded(px(8.))
                        .border_1()
                        .border_color(palette.border)
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(icon("search", 14., palette.faint))
                        .child(controls.search.clone())
                        .when(controls.busy, |view| {
                            view.child(
                                div()
                                    .id("extension-busy")
                                    .absolute()
                                    .top_0()
                                    .right_2()
                                    .w(px(20.))
                                    .h_full()
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .role(Role::Status)
                                    .aria_label(locale.text("ui.workingPleaseWait"))
                                    .child(icon("loader", 14., palette.muted)),
                            )
                        }),
                )
                .child(extension_button(
                    locale.text("ui.refresh"),
                    json!({"kind":refresh}),
                    enabled,
                    palette,
                    cx,
                )),
        )
        .child(body)
        .into_any_element()
}

pub(in crate::surfaces) fn extension_items<'a>(
    catalog: &'a serde_json::Value,
    key: &str,
) -> &'a [serde_json::Value] {
    catalog
        .get(key)
        .and_then(serde_json::Value::as_array)
        .map(Vec::as_slice)
        .unwrap_or_default()
}

fn extension_text<'a>(value: &'a serde_json::Value, key: &str) -> &'a str {
    value
        .get(key)
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
}

fn extension_flag(value: &serde_json::Value, key: &str) -> bool {
    value
        .get(key)
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
}

pub(in crate::surfaces) fn extension_safe_target(value: &serde_json::Value) -> String {
    let target = extension_text(value, "target");
    let Some((scheme, address)) = target.split_once("://") else {
        // Arguments may contain credentials; show only the executable.
        return extension_text(value, "command").to_owned();
    };
    let address = address.split(['?', '#']).next().unwrap_or_default();
    let (authority, path) = address.split_once('/').unwrap_or((address, ""));
    let host = authority.rsplit('@').next().unwrap_or_default();
    format!("{scheme}://{host}/{path}")
}

pub(in crate::surfaces) fn extension_matches(value: &serde_json::Value, query: &str) -> bool {
    query.is_empty()
        || [
            "name",
            "displayName",
            "id",
            "description",
            "sourcePath",
            "origin",
            "source",
            "command",
            "event",
            "marketplace",
            "target",
        ]
        .iter()
        .any(|key| extension_text(value, key).to_lowercase().contains(query))
}

pub(in crate::surfaces) fn plugin_import_action(
    value: &serde_json::Value,
) -> Option<serde_json::Value> {
    let imported = match extension_text(value, "origin") {
        "codex_available" => false,
        "codex" => true,
        _ => return None,
    };
    let mut id = extension_text(value, "id").trim().to_owned();
    if id.is_empty() {
        id = extension_text(value, "name").trim().to_owned();
        let market = extension_text(value, "marketplace").trim();
        if !id.is_empty() && !market.is_empty() {
            id = format!("{id}@{market}");
        }
    }
    (!id.is_empty()).then(
        || json!({"kind":"set_plugin_imported","target":id,"decision":(!imported).to_string()}),
    )
}

pub(crate) fn extension_confirmation(
    payload: &serde_json::Value,
    locale: Locale,
) -> Option<String> {
    let detail = match extension_text(payload, "kind") {
        "set_plugin_hooks_trusted" if payload["decision"] == "true" => {
            locale.text("ui.trustedPluginHooksCanAutomaticallyRunLocalCommandsWithYour")
        }
        "set_plugin_imported" if payload["decision"] == "false" => {
            locale.text("ui.removeTheAzemCopyAndIntegrationsTheOriginalCodexPlugin")
        }
        "delete_mcp_server" => {
            locale.text("ui.deleteThisMcpConfigurationAndDisconnectItThisCannotBe")
        }
        "marketplace_remove" => {
            locale.text("ui.removeThisMarketplaceSourceAndCacheInstalledPluginsRemain")
        }
        "marketplace_uninstall" => locale.text("ui.uninstallThePluginAndItsLocalCopyInTheSelected"),
        _ => return None,
    };
    Some(format!("{}\n{}", extension_text(payload, "target"), detail))
}

fn extension_button(
    label: &'static str,
    payload: serde_json::Value,
    enabled: bool,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let kind = extension_text(&payload, "kind");
    let target = extension_text(&payload, "target");
    let switch = matches!(
        kind,
        "set_skill_enabled" | "set_hook_enabled" | "set_mcp_enabled" | "set_plugin_hooks_trusted"
    );
    let checked = payload["decision"] == "false";
    div()
        .id(format!("extension-{kind}-{target}"))
        .role(if switch { Role::Switch } else { Role::Button })
        .aria_label(
            format!("{label} {}", target.replace('\u{1f}', " · "))
                .trim()
                .to_owned(),
        )
        .tab_stop(enabled)
        .when(switch, |button| {
            button.aria_toggled(if checked {
                gpui::Toggled::True
            } else {
                gpui::Toggled::False
            })
        })
        .h(px(32.))
        .px_2()
        .rounded(px(7.))
        .flex()
        .items_center()
        .justify_center()
        .flex_shrink_0()
        .when(!switch, |button| {
            button
                .border_1()
                .border_color(palette.border)
                .bg(palette.paper)
        })
        .text_xs()
        .text_color(palette.ink)
        .when(enabled, |button| {
            button
                .cursor_pointer()
                .hover(move |style| style.bg(palette.hover))
        })
        .on_click(cx.listener(move |this, _, window, cx| {
            if enabled {
                this.confirm_extension_action(payload.clone(), window, cx);
            }
        }))
        .child(if switch {
            settings_switch(checked, palette).into_any_element()
        } else {
            div().child(label).into_any_element()
        })
        .into_any_element()
}

pub(in crate::surfaces) fn plugin_logo(source: &str) -> Option<Arc<gpui::Image>> {
    let (mime, encoded) = source.strip_prefix("data:")?.split_once(";base64,")?;
    let format = gpui::ImageFormat::from_mime_type(mime)?;
    if !matches!(
        format,
        gpui::ImageFormat::Png
            | gpui::ImageFormat::Jpeg
            | gpui::ImageFormat::Gif
            | gpui::ImageFormat::Webp
            | gpui::ImageFormat::Svg
    ) || encoded.len() > (1_usize << 20).div_ceil(3) * 4
    {
        return None;
    }
    let bytes = STANDARD.decode(encoded).ok()?;
    if bytes.is_empty() || bytes.len() > 1 << 20 {
        return None;
    }
    if format == gpui::ImageFormat::Svg {
        let document = roxmltree::Document::parse(std::str::from_utf8(&bytes).ok()?).ok()?;
        // GPUI's SVG loader can read local hrefs. Plugin images must be self-contained.
        if document.root_element().tag_name().name() != "svg"
            || document
                .descendants()
                .flat_map(|node| node.attributes())
                .any(|attribute| attribute.name() == "href" && !attribute.value().starts_with('#'))
        {
            return None;
        }
    }
    Some(Arc::new(gpui::Image::from_bytes(format, bytes)))
}

fn plugin_mark(value: &serde_json::Value, palette: ThemePalette) -> gpui::AnyElement {
    // Lucide Plug, under the ISC license in gpui/THIRD_PARTY_NOTICES.
    let fallback = move || {
        svg()
        .data(br#"<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22v-5M9 8V2M15 8V2M18 8v5a4 4 0 0 1-4 4h-4a4 4 0 0 1-4-4V8Z"/></svg>"#)
        .size(px(20.))
        .text_color(palette.muted)
        .into_any_element()
    };
    let mark = match plugin_logo(extension_text(value, "logoPath")) {
        Some(logo) => img(logo)
            .size(px(24.))
            .with_fallback(fallback)
            .into_any_element(),
        None => fallback(),
    };
    div()
        .size(px(36.))
        .flex_shrink_0()
        .rounded(px(8.))
        .bg(palette.paper_muted)
        .flex()
        .items_center()
        .justify_center()
        .child(mark)
        .into_any_element()
}

fn extension_row(
    title: &str,
    detail: &str,
    mark: Option<gpui::AnyElement>,
    palette: ThemePalette,
) -> gpui::Div {
    div()
        .w_full()
        .px_4()
        .py_3()
        .border_b_1()
        .border_color(palette.border)
        .flex()
        .items_center()
        .gap_4()
        .children(mark)
        .child(
            div()
                .flex_1()
                .min_w_0()
                .flex()
                .flex_col()
                .gap_1()
                .child(
                    div()
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .text_color(palette.ink)
                        .child(title.to_owned()),
                )
                .child(
                    div()
                        .text_xs()
                        .line_height(px(18.))
                        .text_color(palette.muted)
                        .whitespace_normal()
                        .child(detail.trim().to_owned()),
                ),
        )
}

fn extension_list(
    title: &'static str,
    rows: Vec<gpui::AnyElement>,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
    div()
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .overflow_hidden()
        .child(
            div()
                .px_4()
                .py_3()
                .text_sm()
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .text_color(palette.ink)
                .child(title),
        )
        .when(rows.is_empty(), |view| {
            view.child(
                div()
                    .px_4()
                    .py_5()
                    .text_sm()
                    .text_color(palette.faint)
                    .child(locale.text("ui.noItemsOrMatchingResults")),
            )
        })
        .children(rows)
        .into_any_element()
}

pub(in crate::surfaces) fn marketplace_action(
    kind: &str,
    id: &str,
    scope: &str,
) -> serde_json::Value {
    json!({"kind":kind,"target":id,"decision":scope,"payload":{"scope":scope}})
}

pub(in crate::surfaces) fn marketplace_entries(
    catalog: &serde_json::Value,
    scope: &str,
) -> Vec<serde_json::Value> {
    let mut entries = extension_items(catalog, "available").to_vec();
    for installed in extension_items(catalog, "installed")
        .iter()
        .filter(|v| extension_text(v, "scope") == scope)
    {
        if !entries.iter().any(|v| v["id"] == installed["id"]) {
            entries.push(installed.clone());
        }
    }
    entries
}

fn settings_marketplace_body(
    state: &AppState,
    controls: &ExtensionSettings,
    query: &str,
    enabled: bool,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let catalog = &state.catalogs.marketplace;
    let source = controls.source.read(cx).text().trim().to_owned();
    let mut rows = Vec::new();
    for market in extension_items(catalog, "marketplaces") {
        let name = extension_text(market, "name");
        rows.push(
            extension_row(name, extension_text(market, "source"), None, palette)
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(extension_button(
                            locale.text("ui.update"),
                            json!({"kind":"marketplace_update","target":name}),
                            enabled,
                            palette,
                            cx,
                        ))
                        .child(extension_button(
                            locale.text("ui.remove2"),
                            json!({"kind":"marketplace_remove","target":name}),
                            enabled,
                            palette,
                            cx,
                        )),
                )
                .into_any_element(),
        );
    }
    let mut plugins = Vec::new();
    for plugin in marketplace_entries(catalog, controls.scope)
        .iter()
        .filter(|v| extension_matches(v, query))
    {
        let id = extension_text(plugin, "id");
        let installed = extension_items(catalog, "installed")
            .iter()
            .find(|v| v["id"] == plugin["id"] && extension_text(v, "scope") == controls.scope);
        let mut actions = div().flex().items_center().gap_2().flex_shrink_0();
        if let Some(installed) = installed {
            let on = extension_flag(installed, "enabled");
            actions = actions
                .child(extension_button(
                    if on {
                        locale.text("ui.disable")
                    } else {
                        locale.text("ui.enable")
                    },
                    marketplace_action(
                        if on {
                            "marketplace_disable"
                        } else {
                            "marketplace_enable"
                        },
                        id,
                        controls.scope,
                    ),
                    enabled,
                    palette,
                    cx,
                ))
                .child(extension_button(
                    locale.text("ui.uninstall"),
                    marketplace_action("marketplace_uninstall", id, controls.scope),
                    enabled,
                    palette,
                    cx,
                ));
            if extension_items(catalog, "upgrades").iter().any(|v| {
                v["plugin"]["id"] == plugin["id"] && v["plugin"]["scope"] == controls.scope
            }) {
                actions = actions.child(extension_button(
                    locale.text("ui.upgrade"),
                    marketplace_action("marketplace_upgrade", id, controls.scope),
                    enabled,
                    palette,
                    cx,
                ));
            }
        } else {
            actions = actions.child(extension_button(
                locale.text("ui.install"),
                marketplace_action("marketplace_install", id, controls.scope),
                enabled && !id.is_empty(),
                palette,
                cx,
            ));
        }
        let detail = format!(
            "{} · {} · {}\n{}",
            id,
            extension_text(plugin, "version"),
            if installed.is_some() {
                locale.text("ui.installed")
            } else {
                locale.text("ui.available")
            },
            extension_text(plugin, "description")
        );
        plugins.push(
            extension_row(
                extension_text(plugin, "name"),
                &detail,
                Some(plugin_mark(plugin, palette)),
                palette,
            )
            .child(actions)
            .into_any_element(),
        );
    }
    div()
        .flex()
        .flex_col()
        .gap_4()
        .child(
            div()
                .flex()
                .items_center()
                .gap_3()
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .h(px(36.))
                        .px_3()
                        .rounded(px(8.))
                        .border_1()
                        .border_color(palette.border)
                        .flex()
                        .items_center()
                        .child(controls.source.clone()),
                )
                .child(extension_button(
                    locale.text("ui.addMarketplace"),
                    json!({"kind":"marketplace_add","target":source}),
                    enabled && !source.is_empty(),
                    palette,
                    cx,
                )),
        )
        .child(extension_list(
            locale.text("ui.marketplaceSources"),
            rows,
            palette,
            locale,
        ))
        .child(
            div()
                .id("marketplace-scope")
                .role(Role::RadioGroup)
                .aria_label(locale.text("ui.installationScope"))
                .flex()
                .items_center()
                .gap_2()
                .children(
                    [
                        ("user", locale.text("ui.userScope")),
                        ("project", locale.text("ui.currentProject")),
                    ]
                    .into_iter()
                    .map(|(scope, label)| {
                        div()
                            .id(format!("marketplace-scope-{scope}"))
                            .role(Role::RadioButton)
                            .aria_label(label)
                            .aria_selected(controls.scope == scope)
                            .tab_stop(true)
                            .px_3()
                            .py_2()
                            .rounded(px(7.))
                            .text_xs()
                            .bg(if controls.scope == scope {
                                palette.hover
                            } else {
                                palette.paper
                            })
                            .text_color(palette.ink)
                            .cursor_pointer()
                            .on_click(cx.listener(move |this, _, _, cx| {
                                this.extension_settings.scope = scope;
                                cx.notify();
                            }))
                            .child(label)
                    }),
                ),
        )
        .child(extension_list(
            locale.text("ui.marketplacePlugins"),
            plugins,
            palette,
            locale,
        ))
        .into_any_element()
}

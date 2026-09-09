use super::*;
pub(crate) fn sidebar(
    state: &AppState,
    palette: ThemePalette,
    labels: Labels,
    open_projects: &HashSet<String>,
    show_all_sessions: bool,
    sidebar_context_target: Option<&SidebarMenuTarget>,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let locale = Locale::resolve(&state.settings.language);
    let mut projects = Vec::new();
    if !state.workspace.root.is_empty() {
        projects.push(state.workspace.root.to_string());
    }
    for project in &state.navigation.projects {
        let path = project.path.to_string();
        if !path.is_empty() && !projects.contains(&path) {
            projects.push(path);
        }
    }
    let current_workspace = state.workspace.root.to_string();
    let current_surface = state.navigation.surface;
    let current_pull_request = state.pull_requests.dashboard.get("current").cloned();
    div()
        .id("sidebar")
        .role(Role::Navigation)
        .aria_label(labels.projects)
        .w(px(SIDEBAR_WIDTH))
        .h_full()
        .bg(palette.sidebar)
        .border_r_1()
        .border_color(palette.border)
        .px(px(10.))
        .pt(px(9.))
        .pb(px(12.))
        .flex()
        .flex_col()
        .child(
            div()
                .id("sidebar-switcher")
                .role(Role::TabList)
                .h(px(32.))
                .p(px(2.))
                .mb(px(12.))
                .rounded(px(9.))
                .border_1()
                .border_color(palette.border)
                .bg(palette.paper_muted)
                .flex()
                .gap(px(2.))
                .children(
                    [
                        (labels.conversation, Surface::Thread),
                        (labels.workspace, Surface::Projects),
                    ]
                    .into_iter()
                    .enumerate()
                    .map(|(index, (label, destination))| {
                        let selected = matches!(
                            (destination, current_surface),
                            (Surface::Thread, Surface::Thread | Surface::Search)
                                | (
                                    Surface::Projects,
                                    Surface::Projects
                                        | Surface::Files
                                        | Surface::Changes
                                        | Surface::PullRequests
                                        | Surface::Security
                                        | Surface::Terminal
                                )
                        );
                        div()
                            .id(("sidebar-tab", index))
                            .role(Role::Tab)
                            .aria_label(label)
                            .aria_selected(selected)
                            .tab_stop(true)
                            .flex_1()
                            .h_full()
                            .rounded(px(6.))
                            .bg(if selected {
                                palette.paper
                            } else {
                                palette.paper_muted
                            })
                            .text_color(if selected { palette.ink } else { palette.muted })
                            .text_xs()
                            .flex()
                            .items_center()
                            .justify_center()
                            .cursor_pointer()
                            .when(selected, |tab| {
                                tab.shadow(vec![
                                    BoxShadow::new(
                                        px(0.),
                                        px(1.),
                                        hsla(220. / 360., 0.1, 0.15, 0.09),
                                    )
                                    .blur_radius(px(5.)),
                                ])
                            })
                            .on_click(cx.listener(move |this, _, _, cx| {
                                this.state.navigation.surface = destination;
                                this.request_surface(destination);
                                cx.notify();
                            }))
                            .child(label)
                    }),
                ),
        )
        .child(
            div()
                .id("sidebar-primary")
                .role(Role::Navigation)
                .aria_label(locale.text("navigation.primary"))
                .flex()
                .flex_col()
                .gap(px(2.))
                .mb(px(18.))
                .child(
                    div()
                        .id("sidebar-new")
                        .role(Role::Button)
                        .aria_label(labels.new_conversation)
                        .tab_stop(true)
                        .h(px(32.))
                        .px_2()
                        .rounded(px(8.))
                        .text_color(palette.ink_soft)
                        .text_xs()
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                        .on_click(cx.listener(AzemWindow::new_session))
                        .child(
                            div()
                                .w(px(16.))
                                .text_color(palette.accent)
                                .text_base()
                                .child(icon("plus", 15., palette.accent)),
                        )
                        .child(labels.new_conversation)
                        .child(div().flex_1())
                        .child(div().text_color(palette.faint).text_xs().child("⌘N")),
                )
                .child(
                    div()
                        .id("sidebar-search")
                        .role(Role::Button)
                        .aria_label(labels.search)
                        .tab_stop(true)
                        .h(px(32.))
                        .px_2()
                        .rounded(px(8.))
                        .text_color(palette.ink_soft)
                        .text_xs()
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                        .on_click(cx.listener(|this, _, window, cx| {
                            if this.state.navigation.surface != Surface::Search {
                                this.search_return_surface = this.state.navigation.surface;
                            }
                            this.state.navigation.surface = Surface::Search;
                            this.search_input.focus_handle(cx).focus(window, cx);
                            cx.notify();
                        }))
                        .child(
                            div()
                                .w(px(16.))
                                .text_color(palette.muted)
                                .text_base()
                                .child(icon("search", 15., palette.muted)),
                        )
                        .child(labels.search)
                        .child(div().flex_1())
                        .child(div().text_color(palette.faint).text_xs().child("⌘K")),
                )
                .child(
                    div()
                        .id("sidebar-security")
                        .role(Role::Button)
                        .aria_label(labels.security)
                        .tab_stop(true)
                        .h(px(32.))
                        .px_2()
                        .rounded(px(8.))
                        .bg(if current_surface == Surface::Security {
                            palette.hover
                        } else {
                            palette.sidebar
                        })
                        .text_color(if current_surface == Surface::Security {
                            palette.ink
                        } else {
                            palette.ink_soft
                        })
                        .text_xs()
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.state.navigation.surface = Surface::Security;
                            this.request_surface(Surface::Security);
                            cx.notify();
                        }))
                        .child(
                            div()
                                .w(px(16.))
                                .text_color(palette.muted)
                                .text_sm()
                                .child(icon("shield-check", 15., palette.muted)),
                        )
                        .child(labels.security),
                ),
        )
        .child(
            div()
                .h(px(34.))
                .px(px(6.))
                .flex()
                .items_center()
                .child(
                    div()
                        .text_color(palette.faint)
                        .text_xs()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(labels.projects),
                )
                .child(div().flex_1())
                .child(
                    div()
                        .id("project-add")
                        .role(Role::Button)
                        .aria_label(labels.projects)
                        .tab_stop(true)
                        .size(px(25.))
                        .rounded(px(7.))
                        .text_color(palette.faint)
                        .flex()
                        .items_center()
                        .justify_center()
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.state.navigation.surface = Surface::Projects;
                            cx.notify();
                        }))
                        .child(icon("plus", 15., palette.faint)),
                ),
        )
        .child(
            div()
                .id("project-tree")
                .role(Role::List)
                .aria_label(labels.projects)
                .flex_1()
                .overflow_y_scroll()
                .flex()
                .flex_col()
                .children(
                    projects
                        .into_iter()
                        .enumerate()
                        .map(|(project_index, project)| {
                            let active = project == current_workspace;
                            let context_selected = matches!(
                                sidebar_context_target,
                                Some(SidebarMenuTarget::Project { workspace })
                                    if workspace == &project
                            );
                            let expanded = active || open_projects.contains(&project);
                            let project_name = std::path::Path::new(&project)
                                .file_name()
                                .and_then(|name| name.to_str())
                                .unwrap_or("workspace")
                                .to_string();
                            let project_pull_request = if active {
                                current_pull_request.clone()
                            } else {
                                None
                            };
                            let pull_request_title = project_pull_request
                                .as_ref()
                                .and_then(|pull_request| pull_request.get("title"))
                                .and_then(serde_json::Value::as_str);
                            let sessions = state
                                .navigation
                                .sessions
                                .iter()
                                .filter(|session| {
                                    !session.archived
                                        && (session.workspace.as_ref() == project
                                            || (session.workspace.is_empty() && active))
                                        && pull_request_title != Some(session.title.as_ref())
                                })
                                .collect::<Vec<_>>();
                            let visible_count = if active && show_all_sessions {
                                sessions.len()
                            } else {
                                sessions.len().min(5)
                            };
                            let toggle_project = project.clone();
                            let new_project_session = project.clone();
                            let context_project = project.clone();
                            div()
                                .id(("project-node", project_index))
                                .flex()
                                .flex_col()
                                .mb(px(3.))
                                .child(
                                    div()
                                        .h(px(35.))
                                        .pl(px(3.))
                                        .pr(px(4.))
                                        .rounded(px(9.))
                                        .bg(if active {
                                            palette.accent_soft
                                        } else if context_selected {
                                            palette.hover
                                        } else {
                                            palette.sidebar
                                        })
                                        .flex()
                                        .items_center()
                                        .on_mouse_down(
                                            MouseButton::Right,
                                            cx.listener(move |this, event: &gpui::MouseDownEvent, _, cx| {
                                                this.sidebar_context_menu =
                                                    Some(SidebarContextMenu {
                                                        target: SidebarMenuTarget::Project {
                                                            workspace: context_project.clone(),
                                                        },
                                                        position: event.position,
                                                    });
                                                cx.stop_propagation();
                                                cx.notify();
                                            }),
                                        )
                                        .child(
                                            div()
                                                .id(("project-toggle", project_index))
                                                .role(Role::Button)
                                                .aria_label(project_name.clone())
                                                .aria_expanded(expanded)
                                                .tab_stop(true)
                                                .flex_1()
                                                .h(px(31.))
                                                .flex()
                                                .items_center()
                                                .gap(px(6.))
                                                .cursor_pointer()
                                                .on_click(cx.listener(move |this, _, _, cx| {
                                                    if this.open_projects.contains(&toggle_project)
                                                    {
                                                        this.open_projects.remove(&toggle_project);
                                                    } else {
                                                        this.open_projects
                                                            .insert(toggle_project.clone());
                                                    }
                                                    cx.notify();
                                                }))
                                                .child(div().w(px(10.)).child(icon(
                                                    if expanded {
                                                        "chevron-down"
                                                    } else {
                                                        "chevron-right"
                                                    },
                                                    12.,
                                                    palette.faint,
                                                )))
                                                .child(
                                                    div()
                                                        .overflow_hidden()
                                                        .text_color(palette.ink)
                                                        .text_xs()
                                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                                        .child(project_name),
                                                ),
                                        )
                                        .child(
                                            div()
                                                .id(("project-new-session", project_index))
                                                .role(Role::Button)
                                                .aria_label(labels.new_conversation)
                                                .tab_stop(true)
                                                .size(px(24.))
                                                .rounded(px(7.))
                                                .text_color(palette.faint)
                                                .flex()
                                                .items_center()
                                                .justify_center()
                                                .cursor_pointer()
                                                .hover(move |style| style.bg(palette.paper))
                                                .on_click(cx.listener(move |this, _, _, cx| {
                                                    if active {
                                                        this.runtime.request(
                                                            Method::Execute,
                                                            json!({"kind": "new_session"}),
                                                        );
                                                        this.state.navigation.surface =
                                                            Surface::Thread;
                                                        cx.notify();
                                                    } else {
                                                        this.switch_workspace(
                                                            &new_project_session,
                                                            "",
                                                            None,
                                                            true,
                                                            cx,
                                                        );
                                                    }
                                                }))
                                                .child(icon("plus", 15., palette.faint)),
                                        ),
                                )
                                .when(expanded, |node| {
                                    let node = node.when_some(
                                        project_pull_request,
                                        |node, pull_request| {
                                            let number = pull_request
                                                .get("number")
                                                .and_then(serde_json::Value::as_i64)
                                                .unwrap_or_default();
                                            let title = pull_request
                                                .get("title")
                                                .and_then(serde_json::Value::as_str)
                                                .unwrap_or(locale.text("pr.single"))
                                                .to_string();
                                            let checks = pull_request
                                                .pointer("/checks/total")
                                                .and_then(serde_json::Value::as_i64)
                                                .unwrap_or_default();
                                            node.child(
                                                div()
                                                    .id(("project-pull-request", project_index))
                                                    .role(Role::Button)
                                                    .aria_label(title.clone())
                                                    .tab_stop(true)
                                                    .ml(px(27.))
                                                    .h(px(52.))
                                                    .px(px(6.))
                                                    .rounded(px(8.))
                                                    .text_color(palette.muted)
                                                    .flex()
                                                    .items_center()
                                                    .gap_2()
                                                    .cursor_pointer()
                                                    .hover(move |style| style.bg(palette.hover))
                                                    .on_click(cx.listener(move |this, _, _, cx| {
                                                        let request_id = this.runtime.request(
                                                            Method::PullRequestDetail,
                                                            json!({"number": number}),
                                                        );
                                                        this.pending_requests.insert(
                                                            request_id,
                                                            PendingRequest::PullRequestDetail,
                                                        );
                                                        this.state.navigation.surface =
                                                            Surface::PullRequests;
                                                        cx.notify();
                                                    }))
                                                    .child(icon(
                                                        "git-pull-request",
                                                        14.,
                                                        palette.accent,
                                                    ))
                                                    .child(
                                                        div()
                                                            .flex_1()
                                                            .min_w_0()
                                                            .overflow_hidden()
                                                            .flex()
                                                            .flex_col()
                                                            .child(
                                                                div()
                                                                    .w_full()
                                                                    .truncate()
                                                                    .text_xs()
                                                                    .font_weight(
                                                                        gpui::FontWeight::MEDIUM,
                                                                    )
                                                                    .child(title),
                                                            )
                                                            .child(
                                                                div()
                                                                    .text_size(px(10.))
                                                                    .text_color(palette.faint)
                                                                    .child(format!(
                                                                        "#{number} · {checks} checks"
                                                                    )),
                                                            ),
                                                    )
                                                    .child(
                                                        div()
                                                            .text_size(px(9.))
                                                            .text_color(palette.accent)
                                                            .child("PR"),
                                                    ),
                                            )
                                        },
                                    );
                                    node.child(
                                        div()
                                            .ml(px(17.))
                                            .pl(px(10.))
                                            .border_l_1()
                                            .border_color(palette.border_strong)
                                            .flex()
                                            .flex_col()
                                            .children(
                                                sessions
                                                    .into_iter()
                                                    .take(visible_count)
                                                    .enumerate()
                                                    .map(|(session_index, session)| {
                                                        let session_id = session.id.to_string();
                                                        let workspace = project.clone();
                                                        let selected = active
                                                            && state.navigation.current_session_id
                                                                == session.id
                                                            && current_surface == Surface::Thread;
                                                        let context_selected = matches!(
                                                            sidebar_context_target,
                                                            Some(SidebarMenuTarget::Session {
                                                                id,
                                                                ..
                                                            }) if id == session.id.as_ref()
                                                        );
                                                        let title = if session.title.is_empty() {
                                                            labels.new_conversation.to_string()
                                                        } else {
                                                            session.title.to_string()
                                                        };
                                                        let context_session_id = session_id.clone();
                                                        let context_workspace = workspace.clone();
                                                        let context_title = title.clone();
                                                        let context_pinned = session.pinned;
                                                        div()
                                                .id((
                                                    "project-session",
                                                    project_index * 1000 + session_index,
                                                ))
                                                .role(Role::Button)
                                                .aria_label(title.clone())
                                                .aria_selected(selected || context_selected)
                                                .tab_stop(true)
                                                .h(px(35.))
                                                .px(px(6.))
                                                .rounded(px(8.))
                                                .bg(if selected || context_selected {
                                                    palette.hover
                                                } else {
                                                    palette.sidebar
                                                })
                                                .text_color(if selected || context_selected {
                                                    palette.ink
                                                } else {
                                                    palette.muted
                                                })
                                                .text_xs()
                                                .flex()
                                                .items_center()
                                                .gap(px(7.))
                                                .cursor_pointer()
                                                .hover(move |style| style.bg(palette.hover))
                                                .on_mouse_down(
                                                    MouseButton::Right,
                                                    cx.listener(move |this, event: &gpui::MouseDownEvent, _, cx| {
                                                        this.sidebar_context_menu =
                                                            Some(SidebarContextMenu {
                                                                target: SidebarMenuTarget::Session {
                                                                    id: context_session_id.clone(),
                                                                    title: context_title.clone(),
                                                                    workspace: context_workspace.clone(),
                                                                    pinned: context_pinned,
                                                                },
                                                                position: event.position,
                                                            });
                                                        cx.stop_propagation();
                                                        cx.notify();
                                                    }),
                                                )
                                                .on_click(cx.listener(move |this, _, _, cx| {
                                                    if active {
                                                        let request_id = this.runtime.request(
                                                            Method::ResumeSession,
                                                            json!({"sessionId": session_id}),
                                                        );
                                                        this.pending_requests.insert(
                                                            request_id,
                                                            PendingRequest::ResumeSession {
                                                                sequence: None,
                                                            },
                                                        );
                                                        this.state.navigation.surface =
                                                            Surface::Thread;
                                                        cx.notify();
                                                    } else {
                                                        this.switch_workspace(
                                                            &workspace,
                                                            &session_id,
                                                            None,
                                                            false,
                                                            cx,
                                                        );
                                                    }
                                                }))
                                                .child(
                                                    div()
                                                        .flex_1()
                                                        .min_w_0()
                                                        .overflow_hidden()
                                                        .truncate()
                                                        .child(title),
                                                )
                                                .when(session.running, |row| {
                                                    row.child(
                                                        icon("loader", 13., palette.muted)
                                                            .with_animation(
                                                                (
                                                                    "sidebar-session-running",
                                                                    project_index * 1000
                                                                        + session_index,
                                                                ),
                                                                Animation::new(
                                                                    Duration::from_millis(800),
                                                                )
                                                                .repeat(),
                                                                |spinner, progress| {
                                                                    spinner.with_transformation(
                                                                        Transformation::rotate(
                                                                            percentage(progress),
                                                                        ),
                                                                    )
                                                                },
                                                            ),
                                                    )
                                                })
                                                .when(session.unread && !session.running, |row| {
                                                    row.child(
                                                        div()
                                                            .size(px(6.))
                                                            .flex_shrink_0()
                                                            .rounded_full()
                                                            .bg(palette.accent),
                                                    )
                                                })
                                                    }),
                                            )
                                            .when(
                                                active && state.navigation.sessions.len() > 5,
                                                |list| {
                                                    list.child(
                                                        div()
                                                            .id("show-more-sessions")
                                                            .role(Role::Button)
                                                            .tab_stop(true)
                                                            .h(px(30.))
                                                            .px(px(6.))
                                                            .text_color(palette.faint)
                                                            .text_xs()
                                                            .flex()
                                                            .items_center()
                                                            .cursor_pointer()
                                                            .on_click(cx.listener(
                                                                |this, _, _, cx| {
                                                                    this.show_all_sessions =
                                                                        !this.show_all_sessions;
                                                                    cx.notify();
                                                                },
                                                            ))
                                                            .child(if show_all_sessions {
                                                                locale.text("ui.showLess")
                                                            } else { locale.text("common.showMore") }),
                                                    )
                                                },
                                            ),
                                    )
                                })
                        }),
                ),
        )
        .child(
            div()
                .border_t_1()
                .border_color(palette.border)
                .pt(px(8.))
                .child(
                    div()
                        .id("sidebar-settings")
                        .role(Role::Button)
                        .aria_label(labels.settings)
                        .tab_stop(true)
                        .h(px(32.))
                        .px_2()
                        .rounded(px(8.))
                        .text_color(palette.muted)
                        .text_xs()
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .hover(move |style| style.bg(palette.hover))
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.settings_open = true;
                            this.settings_provider = None;
                            if this.state.catalogs.providers.is_empty() {
                                this.refresh_model_catalog();
                            }
                            cx.notify();
                        }))
                        .child(div().w(px(16.)).child(icon("settings", 15., palette.muted)))
                        .child(labels.settings)
                        .child(div().flex_1())
                        .child(div().text_color(palette.faint).text_xs().child("⌘,")),
                ),
        )
        .into_any_element()
}

fn sidebar_context_menu_item(
    id: &'static str,
    icon_name: &'static str,
    label: &'static str,
    action: SidebarMenuAction,
    palette: ThemePalette,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    div()
        .id(id)
        .role(Role::MenuItem)
        .aria_label(label)
        .tab_stop(true)
        .h(px(30.))
        .px(px(8.))
        .rounded(px(6.))
        .text_size(px(13.))
        .text_color(palette.ink)
        .flex()
        .items_center()
        .gap(px(8.))
        .cursor_pointer()
        .hover(move |row| row.bg(palette.hover))
        .on_click(cx.listener(move |this, _, window, cx| {
            this.run_sidebar_menu_action(action, window, cx);
        }))
        .child(
            div()
                .w(px(16.))
                .child(icon(icon_name, 15., palette.ink_soft)),
        )
        .child(label)
        .into_any_element()
}

pub(crate) fn sidebar_context_menu_view(
    this: &AzemWindow,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let menu = this
        .sidebar_context_menu
        .as_ref()
        .expect("sidebar context menu target exists");
    let (aria_label, items, separator_at) = match &menu.target {
        SidebarMenuTarget::Session { pinned, .. } => (
            locale.text("sidebar.sessionActions"),
            vec![
                (
                    "sidebar-menu-pin-session",
                    "anchor",
                    locale.text(if *pinned {
                        "sidebar.unpinSession"
                    } else {
                        "sidebar.pinSession"
                    }),
                    SidebarMenuAction::ToggleSessionPin,
                ),
                (
                    "sidebar-menu-rename-session",
                    "notebook-pen",
                    locale.text("sidebar.renameSession"),
                    SidebarMenuAction::RenameSession,
                ),
                (
                    "sidebar-menu-mark-unread",
                    "circle",
                    locale.text("sidebar.markUnread"),
                    SidebarMenuAction::MarkSessionUnread,
                ),
                (
                    "sidebar-menu-archive-session",
                    "archive",
                    locale.text("sidebar.archiveSession"),
                    SidebarMenuAction::ArchiveSession,
                ),
                (
                    "sidebar-menu-copy-workspace",
                    "folder",
                    locale.text("sidebar.copyWorkspace"),
                    SidebarMenuAction::CopyWorkspace,
                ),
                (
                    "sidebar-menu-copy-session-id",
                    "copy",
                    locale.text("sidebar.copySessionId"),
                    SidebarMenuAction::CopySessionId,
                ),
            ],
            Some(4),
        ),
        SidebarMenuTarget::Project { .. } => (
            locale.text("sidebar.projectActions"),
            vec![
                (
                    "sidebar-menu-reveal-project",
                    "folder",
                    locale.text("sidebar.showInFinder"),
                    SidebarMenuAction::RevealProject,
                ),
                (
                    "sidebar-menu-archive-project-sessions",
                    "archive",
                    locale.text("sidebar.archiveProjectSessions"),
                    SidebarMenuAction::ArchiveProjectSessions,
                ),
                (
                    "sidebar-menu-copy-project-path",
                    "copy",
                    locale.text("sidebar.copyProjectPath"),
                    SidebarMenuAction::CopyProjectPath,
                ),
                (
                    "sidebar-menu-remove-project",
                    "eye-off",
                    locale.text("sidebar.removeProject"),
                    SidebarMenuAction::RemoveProject,
                ),
            ],
            Some(3),
        ),
    };
    let mut content = div()
        .id("sidebar-context-menu")
        .role(Role::Menu)
        .aria_label(aria_label)
        .occlude()
        .w(px(172.))
        .p(px(5.))
        .rounded(px(12.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .shadow(vec![
            BoxShadow::new(px(0.), px(8.), hsla(220. / 360., 0.12, 0.12, 0.18))
                .blur_radius(px(24.)),
        ])
        .on_mouse_down_out(cx.listener(AzemWindow::dismiss_sidebar_context_menu));
    for (index, (id, icon_name, label, action)) in items.into_iter().enumerate() {
        if separator_at == Some(index) {
            content = content.child(div().h(px(1.)).mx(px(6.)).my(px(3.)).bg(palette.border));
        }
        content = content.child(sidebar_context_menu_item(
            id, icon_name, label, action, palette, cx,
        ));
    }
    deferred(
        gpui::anchored()
            .anchor(gpui::Anchor::TopLeft)
            .position(menu.position)
            .snap_to_window_with_margin(px(8.))
            .child(content),
    )
    .with_priority(50)
    .into_any_element()
}

pub(crate) fn session_rename_modal(
    this: &AzemWindow,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let dialog = div()
        .id("session-rename-dialog")
        .role(Role::Region)
        .aria_label(locale.text("sidebar.renameTitle"))
        .occlude()
        .w(px(420.))
        .p(px(18.))
        .rounded(px(14.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .shadow(vec![
            BoxShadow::new(px(0.), px(16.), hsla(220. / 360., 0.12, 0.12, 0.2))
                .blur_radius(px(38.)),
        ])
        .on_mouse_down_out(cx.listener(AzemWindow::cancel_session_rename))
        .child(
            div()
                .mb(px(12.))
                .text_size(px(15.))
                .font_weight(gpui::FontWeight::SEMIBOLD)
                .text_color(palette.ink)
                .child(locale.text("sidebar.renameTitle")),
        )
        .child(
            div()
                .h(px(38.))
                .px_2()
                .rounded(px(8.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper_muted)
                .child(this.session_rename_input.clone()),
        )
        .child(
            div()
                .mt(px(16.))
                .flex()
                .justify_end()
                .gap_2()
                .child(
                    div()
                        .id("session-rename-cancel")
                        .role(Role::Button)
                        .aria_label(locale.text("ui.cancel"))
                        .tab_stop(true)
                        .px_3()
                        .h(px(34.))
                        .rounded(px(8.))
                        .border_1()
                        .border_color(palette.border)
                        .text_size(px(13.))
                        .text_color(palette.ink_soft)
                        .flex()
                        .items_center()
                        .cursor_pointer()
                        .hover(move |button| button.bg(palette.hover))
                        .on_click(cx.listener(AzemWindow::cancel_session_rename_click))
                        .child(locale.text("ui.cancel")),
                )
                .child(
                    div()
                        .id("session-rename-save")
                        .role(Role::Button)
                        .aria_label(locale.text("sidebar.saveRename"))
                        .tab_stop(true)
                        .px_3()
                        .h(px(34.))
                        .rounded(px(8.))
                        .bg(palette.button)
                        .text_size(px(13.))
                        .text_color(palette.button_text)
                        .flex()
                        .items_center()
                        .cursor_pointer()
                        .on_click(cx.listener(AzemWindow::commit_session_rename_click))
                        .child(locale.text("sidebar.saveRename")),
                ),
        );
    div()
        .id("session-rename-backdrop")
        .absolute()
        .occlude()
        .size_full()
        .bg(hsla(220. / 360., 0.08, 0.18, 0.24))
        .flex()
        .items_center()
        .justify_center()
        .child(dialog)
        .into_any_element()
}

pub(crate) fn search_surface(
    state: &AppState,
    input: Entity<TextInput>,
    palette: ThemePalette,
    labels: Labels,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let locale = Locale::resolve(&state.settings.language);
    let commands = [
        ("plus", locale.text("ui.newConversation"), "⌘N", "new"),
        (
            "file-code",
            locale.text("ui.viewProjectFiles"),
            "⌘2",
            "files",
        ),
        (
            "file-diff",
            locale.text("ui.viewCodeChanges"),
            "⌘3",
            "changes",
        ),
        (
            "shield-check",
            locale.text("ui.openSecurityScans"),
            locale.text("ui.workspace"),
            "security",
        ),
        (
            "sparkles",
            locale.text("ui.openMotionSettings"),
            "⌘,",
            "settings",
        ),
        (
            "terminal",
            locale.text("ui.openOrCloseTerminal"),
            "⌘`",
            "terminal",
        ),
    ];
    div()
        .id("search-surface")
        .role(Role::Search)
        .aria_label(labels.search)
        .absolute()
        .size_full()
        .bg(rgba(0x16161338))
        .px(px(22.))
        .pt(px(150.))
        .flex()
        .justify_center()
        .items_center()
        .child(
            div()
                .w_full()
                .max_w(px(590.))
                .max_h(px(560.))
                .rounded(px(15.))
                .border_1()
                .border_color(palette.border_strong)
                .bg(palette.paper)
                .shadow(vec![
                    BoxShadow::new(px(0.), px(24.), hsla(40. / 360., 0.1, 0.1, 0.22))
                        .blur_radius(px(70.)),
                ])
                .overflow_hidden()
                .flex()
                .flex_col()
                .child(
                    div()
                        .id("search-field")
                        .h(px(53.))
                        .px(px(14.))
                        .border_b_1()
                        .border_color(palette.border)
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(div().w(px(18.)).child(icon("search", 17., palette.faint)))
                        .child(div().flex_1().min_w_0().child(input))
                        .child(
                            div()
                                .text_color(palette.faint)
                                .text_size(px(9.))
                                .child("esc"),
                        ),
                )
                .child(
                    div()
                        .id("search-results")
                        .max_h(px(470.))
                        .overflow_y_scroll()
                        .p(px(8.))
                        .flex()
                        .flex_col()
                        .gap(px(2.))
                        .when(state.navigation.search_results.is_empty(), |list| {
                            list.child(
                                div()
                                    .h(px(24.))
                                    .px_2()
                                    .text_color(palette.faint)
                                    .text_size(px(9.))
                                    .font_weight(gpui::FontWeight::BOLD)
                                    .flex()
                                    .items_center()
                                    .child(locale.text("ui.actions")),
                            )
                            .children(
                                commands.into_iter().enumerate().map(
                                    |(index, (icon_name, label, shortcut, action))| {
                                        div()
                                            .id(("search-command", index))
                                            .role(Role::Button)
                                            .aria_label(label)
                                            .tab_stop(true)
                                            .h(px(40.))
                                            .px_2()
                                            .rounded(px(8.))
                                            .bg(if index == 0 {
                                                palette.hover
                                            } else {
                                                palette.paper
                                            })
                                            .text_color(if index == 0 {
                                                palette.ink
                                            } else {
                                                palette.muted
                                            })
                                            .text_size(px(12.))
                                            .font_weight(gpui::FontWeight::MEDIUM)
                                            .flex()
                                            .items_center()
                                            .gap_2()
                                            .cursor_pointer()
                                            .hover(move |style| {
                                                style.bg(palette.hover).text_color(palette.ink)
                                            })
                                            .on_click(cx.listener(move |this, _, window, cx| {
                                                match action {
                                                    "new" => {
                                                        this.runtime.request(
                                                            Method::Execute,
                                                            json!({"kind": "new_session"}),
                                                        );
                                                        this.state.navigation.surface =
                                                            Surface::Thread;
                                                    }
                                                    "files" => {
                                                        this.state.navigation.surface =
                                                            Surface::Files;
                                                        this.request_surface(Surface::Files);
                                                    }
                                                    "changes" => {
                                                        this.state.navigation.surface =
                                                            Surface::Changes;
                                                        this.request_surface(Surface::Changes);
                                                    }
                                                    "security" => {
                                                        this.state.navigation.surface =
                                                            Surface::Security;
                                                        this.request_surface(Surface::Security);
                                                    }
                                                    "settings" => {
                                                        this.state.navigation.surface =
                                                            this.search_return_surface;
                                                        this.settings_section =
                                                            "appearance".to_string();
                                                        this.settings_open = true;
                                                    }
                                                    "terminal" => {
                                                        this.state.navigation.surface =
                                                            this.search_return_surface;
                                                        this.set_terminal_open(true, window, cx);
                                                    }
                                                    _ => {}
                                                }
                                                cx.notify();
                                            }))
                                            .child(
                                                div()
                                                    .w(px(24.))
                                                    .flex()
                                                    .justify_center()
                                                    .child(icon(icon_name, 15., rgb(0x1f7af0))),
                                            )
                                            .child(div().flex_1().child(label))
                                            .child(
                                                div()
                                                    .text_color(palette.faint)
                                                    .text_size(px(9.))
                                                    .child(shortcut),
                                            )
                                    },
                                ),
                            )
                        })
                        .when(!state.navigation.search_error.is_empty(), |list| {
                            list.child(
                                div()
                                    .id("search-error")
                                    .role(Role::Alert)
                                    .px_3()
                                    .py_2()
                                    .text_color(palette.danger)
                                    .text_xs()
                                    .child(state.navigation.search_error.to_string()),
                            )
                        })
                        .children(state.navigation.search_results.iter().enumerate().map(
                            |(index, result)| {
                                let session_id = result
                                    .get("sessionId")
                                    .or_else(|| result.get("session_id"))
                                    .and_then(serde_json::Value::as_str)
                                    .unwrap_or_default()
                                    .to_string();
                                let workspace = result
                                    .get("workspace")
                                    .and_then(serde_json::Value::as_str)
                                    .unwrap_or_default()
                                    .to_string();
                                let title = result
                                    .get("title")
                                    .and_then(serde_json::Value::as_str)
                                    .unwrap_or(locale.text("ui.untitledConversation"))
                                    .to_string();
                                let preview = result
                                    .get("preview")
                                    .and_then(serde_json::Value::as_str)
                                    .unwrap_or_default()
                                    .to_string();
                                let sequence =
                                    result.get("sequence").and_then(serde_json::Value::as_i64);
                                let current_workspace = state.workspace.root.to_string();
                                div()
                                    .id(("search-result", index))
                                    .role(Role::Button)
                                    .aria_label(title.clone())
                                    .tab_stop(true)
                                    .min_h(px(44.))
                                    .px_3()
                                    .py_2()
                                    .rounded(px(8.))
                                    .text_color(palette.muted)
                                    .flex()
                                    .flex_col()
                                    .gap_1()
                                    .cursor_pointer()
                                    .hover(move |style| {
                                        style.bg(palette.hover).text_color(palette.ink)
                                    })
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        if workspace.is_empty() || workspace == current_workspace {
                                            let request_id = this.runtime.request(
                                                Method::ResumeSession,
                                                json!({"sessionId": session_id}),
                                            );
                                            this.pending_requests.insert(
                                                request_id,
                                                PendingRequest::ResumeSession { sequence },
                                            );
                                            this.state.navigation.surface = Surface::Thread;
                                        } else {
                                            this.switch_workspace(
                                                &workspace,
                                                &session_id,
                                                sequence,
                                                false,
                                                cx,
                                            );
                                        }
                                        cx.notify();
                                    }))
                                    .child(
                                        div()
                                            .text_sm()
                                            .font_weight(gpui::FontWeight::SEMIBOLD)
                                            .child(title),
                                    )
                                    .when(!preview.is_empty(), |row| {
                                        row.child(
                                            div()
                                                .truncate()
                                                .text_color(palette.faint)
                                                .text_xs()
                                                .child(preview),
                                        )
                                    })
                            },
                        )),
                )
                .child(
                    div()
                        .h(px(31.))
                        .px_3()
                        .border_t_1()
                        .border_color(palette.border)
                        .text_color(palette.faint)
                        .text_size(px(9.))
                        .flex()
                        .items_center()
                        .justify_end()
                        .gap_3()
                        .child(locale.text("ui.open"))
                        .child(locale.text("ui.navigate"))
                        .child(locale.text("ui.escClose")),
                ),
        )
        .into_any_element()
}

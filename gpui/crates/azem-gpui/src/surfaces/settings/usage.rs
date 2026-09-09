use super::*;
#[derive(Clone)]
pub(in crate::surfaces) struct UsageHeatCell {
    pub(in crate::surfaces) in_range: bool,
    pub(in crate::surfaces) tokens: i64,
    pub(in crate::surfaces) date: Option<String>,
}

fn usage_i64(value: &serde_json::Value, key: &str) -> i64 {
    value
        .get(key)
        .and_then(serde_json::Value::as_i64)
        .unwrap_or_default()
}

fn usage_compact(value: f64, digits: usize) -> String {
    let mut rendered = format!("{value:.digits$}");
    while rendered.contains('.') && rendered.ends_with('0') {
        rendered.pop();
    }
    if rendered.ends_with('.') {
        rendered.pop();
    }
    rendered
}

pub(in crate::surfaces) fn format_usage_count(value: i64, locale: Locale) -> String {
    let abs = value.unsigned_abs();
    if let Some(unit) = locale
        .compact_units()
        .iter()
        .find(|unit| abs >= unit.divisor)
    {
        return format!(
            "{}{}",
            usage_compact(value as f64 / unit.divisor as f64, unit.digits),
            unit.suffix
        );
    }
    format_usage_exact(value)
}

pub(in crate::surfaces) fn format_usage_exact(value: i64) -> String {
    let mut rendered = value.unsigned_abs().to_string();
    let mut index = rendered.len();
    while index > 3 {
        index -= 3;
        rendered.insert(index, ',');
    }
    format!("{}{rendered}", if value < 0 { "-" } else { "" })
}

pub(in crate::surfaces) fn format_usage_duration(milliseconds: i64, locale: Locale) -> String {
    if milliseconds <= 0 {
        return "—".to_string();
    }
    let total_minutes = ((milliseconds as f64 / 60_000.).round() as i64).max(0);
    let hours = total_minutes / 60;
    let minutes = total_minutes % 60;
    if hours > 0 && minutes > 0 {
        locale.format(
            "duration.hoursMinutes",
            &[
                ("hours", hours.to_string()),
                ("minutes", minutes.to_string()),
            ],
        )
    } else if hours > 0 {
        locale.format("duration.hours", &[("hours", hours.to_string())])
    } else if total_minutes > 0 {
        locale.format(
            "duration.minutes",
            &[("minutes", total_minutes.max(1).to_string())],
        )
    } else {
        locale.format(
            "duration.seconds",
            &[("seconds", (milliseconds / 1_000).max(1).to_string())],
        )
    }
}

fn usage_cache_hit(report: &serde_json::Value) -> String {
    if !report
        .get("cacheReported")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
    {
        return "—".to_string();
    }
    let input = usage_i64(report, "reportedInputTokens");
    if input <= 0 {
        return "—".to_string();
    }
    usage_compact(
        usage_i64(report, "cacheReadTokens") as f64 * 100. / input as f64,
        1,
    ) + "%"
}

fn usage_day_number(value: &str) -> Option<i64> {
    let mut parts = value.split('-');
    let year = parts.next()?.parse::<i64>().ok()?;
    let month = parts.next()?.parse::<u32>().ok()?;
    let day = parts.next()?.parse::<u32>().ok()?;
    if parts.next().is_some() {
        return None;
    }
    let days_in_month = usage_days_in_month(year, month)?;
    if !(1..=days_in_month).contains(&day) {
        return None;
    }
    let adjusted_year = year - i64::from(month <= 2);
    let era = adjusted_year.div_euclid(400);
    let year_of_era = adjusted_year - era * 400;
    let adjusted_month = month as i64 + 9 - 12 * i64::from(month > 2);
    let day_of_year = (153 * adjusted_month + 2) / 5 + day as i64 - 1;
    let day_of_era = year_of_era * 365 + year_of_era / 4 - year_of_era / 100 + day_of_year;
    Some(era * 146_097 + day_of_era - 719_468)
}

fn usage_days_in_month(year: i64, month: u32) -> Option<u32> {
    const DAYS: [u32; 12] = [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
    let days = *DAYS.get(month.checked_sub(1)? as usize)?;
    let leap = year % 4 == 0 && (year % 100 != 0 || year % 400 == 0);
    Some(days + u32::from(month == 2 && leap))
}

fn fill_usage_heatmap(
    report: &serde_json::Value,
    cells: &mut [UsageHeatCell],
    leading: usize,
    from: i64,
    to: i64,
) -> i64 {
    let mut peak = 0;
    for day in report
        .get("days")
        .and_then(serde_json::Value::as_array)
        .into_iter()
        .flatten()
    {
        let Some(date) = day.get("date").and_then(serde_json::Value::as_str) else {
            continue;
        };
        let Some(index) = usage_day_number(date) else {
            continue;
        };
        if !(from..=to).contains(&index) {
            continue;
        }
        let tokens = usage_i64(day, "tokens").max(0);
        let cell = &mut cells[leading + (index - from) as usize];
        cell.tokens = tokens;
        cell.date = Some(date.to_string());
        peak = peak.max(tokens);
    }
    peak
}

pub(in crate::surfaces) fn usage_heatmap(
    report: &serde_json::Value,
) -> (Vec<Vec<UsageHeatCell>>, i64) {
    let Some(from) = report
        .get("from")
        .and_then(serde_json::Value::as_str)
        .and_then(usage_day_number)
    else {
        return (Vec::new(), 0);
    };
    let Some(to) = report
        .get("to")
        .and_then(serde_json::Value::as_str)
        .and_then(usage_day_number)
    else {
        return (Vec::new(), 0);
    };
    if to < from || to - from > 370 {
        return (Vec::new(), 0);
    }
    let leading = (from + 4).rem_euclid(7) as usize;
    let days = (to - from + 1) as usize;
    let slots = (leading + days).div_ceil(7) * 7;
    let mut cells = vec![
        UsageHeatCell {
            in_range: false,
            tokens: 0,
            date: None,
        };
        slots
    ];
    for cell in cells.iter_mut().skip(leading).take(days) {
        cell.in_range = true;
    }
    let peak = fill_usage_heatmap(report, &mut cells, leading, from, to);
    (cells.chunks(7).map(|week| week.to_vec()).collect(), peak)
}

pub(in crate::surfaces) fn usage_activity_level(tokens: i64, peak: i64) -> usize {
    if tokens <= 0 || peak <= 0 {
        0
    } else if tokens * 100 >= peak * 66 {
        3
    } else if tokens * 100 >= peak * 33 {
        2
    } else {
        1
    }
}

fn usage_heat_color(level: usize, palette: ThemePalette) -> Rgba {
    match level {
        1 => rgb(0xb8dcff),
        2 => rgb(0x62adf8),
        3 => palette.accent,
        _ => palette.paper_muted,
    }
}

fn usage_fact(label: &'static str, value: String, palette: ThemePalette) -> gpui::Div {
    div()
        .min_w(px(104.))
        .flex_1()
        .flex()
        .flex_col()
        .gap(px(3.))
        .child(div().text_xs().text_color(palette.faint).child(label))
        .child(div().text_sm().text_color(palette.ink).child(value))
}

fn usage_loading_view(palette: ThemePalette, locale: Locale) -> gpui::AnyElement {
    div()
        .min_h(px(120.))
        .rounded(px(14.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .flex()
        .items_center()
        .justify_center()
        .gap_2()
        .child(
            div()
                .size(px(9.))
                .rounded_full()
                .bg(palette.accent)
                .with_animation(
                    "usage-loading",
                    Animation::new(Duration::from_millis(900)).repeat(),
                    |dot, progress| {
                        dot.opacity(0.25 + (progress * std::f32::consts::PI).sin().abs() * 0.75)
                    },
                ),
        )
        .child(
            div()
                .text_sm()
                .text_color(palette.muted)
                .child(locale.text("ui.loadingUsage")),
        )
        .into_any_element()
}

fn usage_model_identity(
    model: &serde_json::Value,
    state: &AppState,
    locale: Locale,
) -> (String, String, String) {
    let provider_id = model
        .get("provider")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let model_id = model
        .get("model")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let provider = state.catalogs.providers.iter().find(|provider| {
        provider.get("id").and_then(serde_json::Value::as_str) == Some(provider_id)
    });
    let title = if model_id.trim().is_empty() {
        locale.text("ui.unlabeledModel").to_string()
    } else {
        settings_route_model_name(&state.catalogs.providers, provider_id, model_id)
    };
    let provider_name = provider
        .map(|provider| provider_display_name(provider, provider_id))
        .unwrap_or_else(|| provider_id.to_string());
    let logo = provider
        .map(|provider| provider_logo_id(provider, provider_id))
        .unwrap_or_else(|| provider_id.to_string());
    (title, provider_name, logo)
}

fn usage_kind_label(kind: &str, locale: Locale) -> String {
    match kind {
        "main" => locale.text("ui.main").into(),
        "subagent" => locale.text("ui.subagent").into(),
        "compaction" => locale.text("ui.compaction").into(),
        "team" => locale.text("ui.team").into(),
        "review" => locale.text("ui.review").into(),
        _ => kind.to_string(),
    }
}

fn usage_breakdown_header(title: &'static str, palette: ThemePalette) -> gpui::Div {
    div()
        .h(px(44.))
        .px_4()
        .flex()
        .items_center()
        .text_sm()
        .font_weight(gpui::FontWeight::SEMIBOLD)
        .text_color(palette.ink)
        .child(title)
}

fn usage_model_row(
    model: &serde_json::Value,
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::Div {
    let (title, provider_name, logo) = usage_model_identity(model, state, locale);
    let model_id = model
        .get("model")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let requests = usage_i64(model, "requests");
    let meta = [
        provider_name,
        if model_id != title {
            model_id.to_string()
        } else {
            String::new()
        },
        locale.format(
            "ui.requestsRequests",
            &[("requests", (requests).to_string())],
        ),
    ]
    .into_iter()
    .filter(|value| !value.is_empty())
    .collect::<Vec<_>>()
    .join(" · ");
    let cache = if model
        .get("cacheReported")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
    {
        format_usage_count(usage_i64(model, "cacheReadTokens"), locale)
    } else {
        "—".to_string()
    };
    let token_detail = locale.format(
        "ui.inputArg0OutputArg1CacheArg2",
        &[
            (
                "arg0",
                (format_usage_count(usage_i64(model, "inputTokens"), locale)).to_string(),
            ),
            (
                "arg1",
                (format_usage_count(usage_i64(model, "outputTokens"), locale)).to_string(),
            ),
            ("arg2", (cache).to_string()),
        ],
    );
    div()
        .min_h(px(66.))
        .px_4()
        .py_2()
        .border_t_1()
        .border_color(palette.border)
        .flex()
        .items_center()
        .gap_3()
        .child(provider_logo(&logo, 18., palette.ink))
        .child(
            div()
                .min_w_0()
                .flex_1()
                .flex()
                .flex_col()
                .gap(px(2.))
                .child(
                    div()
                        .truncate()
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .text_color(palette.ink)
                        .child(title),
                )
                .child(
                    div()
                        .truncate()
                        .text_xs()
                        .text_color(palette.faint)
                        .child(meta),
                )
                .child(
                    div()
                        .truncate()
                        .text_size(px(10.))
                        .text_color(palette.muted)
                        .child(token_detail),
                ),
        )
        .child(
            div()
                .text_sm()
                .text_color(palette.ink_soft)
                .child(format_usage_count(usage_i64(model, "tokens"), locale)),
        )
}

fn usage_model_rows(state: &AppState, palette: ThemePalette, locale: Locale) -> gpui::AnyElement {
    let models = state
        .settings
        .usage
        .get("models")
        .and_then(serde_json::Value::as_array)
        .cloned()
        .unwrap_or_default();
    let rows = models
        .iter()
        .map(|model| usage_model_row(model, state, palette, locale))
        .collect::<Vec<_>>();
    div()
        .min_w_0()
        .flex_1()
        .border_l_1()
        .border_color(palette.border)
        .child(usage_breakdown_header(locale.text("ui.models2"), palette))
        .when(models.is_empty(), |section| {
            section.child(
                div()
                    .h(px(58.))
                    .px_4()
                    .flex()
                    .items_center()
                    .text_sm()
                    .text_color(palette.faint)
                    .child(locale.text("ui.noModelUsage")),
            )
        })
        .children(rows)
        .into_any_element()
}

fn usage_kind_rows(
    report: &serde_json::Value,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
    let kinds = report
        .get("kinds")
        .and_then(serde_json::Value::as_array)
        .cloned()
        .unwrap_or_default();
    let total = kinds
        .iter()
        .map(|row| usage_i64(row, "tokens"))
        .sum::<i64>();
    let rows = kinds
        .iter()
        .map(|row| {
            let tokens = usage_i64(row, "tokens");
            let fraction = if total > 0 {
                (tokens as f32 / total as f32).clamp(0.04, 1.)
            } else {
                0.
            };
            let requests = usage_i64(row, "requests");
            div()
                .min_h(px(58.))
                .px_4()
                .py_2()
                .border_t_1()
                .border_color(palette.border)
                .flex()
                .flex_col()
                .justify_center()
                .gap_2()
                .child(
                    div()
                        .flex()
                        .items_center()
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
                                        .child(usage_kind_label(
                                            row.get("kind")
                                                .and_then(serde_json::Value::as_str)
                                                .unwrap_or_default(),
                                            locale,
                                        )),
                                )
                                .child(div().text_xs().text_color(palette.faint).child(
                                    locale.format(
                                        "ui.requestsRequests",
                                        &[("requests", (requests).to_string())],
                                    ),
                                )),
                        )
                        .child(
                            div()
                                .text_sm()
                                .text_color(palette.ink_soft)
                                .child(format_usage_count(tokens, locale)),
                        ),
                )
                .child(
                    div()
                        .w_full()
                        .h(px(4.))
                        .rounded_full()
                        .bg(palette.paper_muted)
                        .overflow_hidden()
                        .child(
                            div()
                                .h_full()
                                .w(relative(fraction))
                                .rounded_full()
                                .bg(palette.ink_soft),
                        ),
                )
        })
        .collect::<Vec<_>>();
    div()
        .min_w_0()
        .flex_1()
        .child(usage_breakdown_header(
            locale.text("ui.requestKinds"),
            palette,
        ))
        .children(rows)
        .into_any_element()
}

fn usage_heat_tooltip(
    date: &str,
    tokens: i64,
    week_index: usize,
    day_index: usize,
    total_weeks: usize,
    palette: ThemePalette,
    locale: Locale,
) -> gpui::AnyElement {
    let middle = week_index >= 7 && week_index + 7 < total_weeks;
    div()
        .absolute()
        .top(px(day_index as f32 * 11. - 34.))
        .when(week_index < 7, |tooltip| tooltip.left_0())
        .when(week_index + 7 >= total_weeks, |tooltip| tooltip.right_0())
        .when(middle, |tooltip| {
            tooltip
                .left(relative((week_index as f32 + 0.5) / total_weeks as f32))
                .ml(px(-90.))
        })
        .h(px(28.))
        .px_2()
        .rounded(px(7.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .shadow(vec![
            BoxShadow::new(px(0.), px(5.), hsla(220. / 360., 0.12, 0.12, 0.14))
                .blur_radius(px(14.)),
        ])
        .flex()
        .items_center()
        .text_xs()
        .text_color(palette.muted)
        .child(format!(
            "{date} · {} Token",
            format_usage_count(tokens, locale)
        ))
        .into_any_element()
}

fn usage_heatmap_view(
    weeks: &[Vec<UsageHeatCell>],
    peak: i64,
    usage_hover: Option<&(String, i64)>,
    palette: ThemePalette,
    locale: Locale,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let hovered = usage_hover.and_then(|(hovered_date, tokens)| {
        weeks.iter().enumerate().find_map(|(week_index, week)| {
            week.iter().enumerate().find_map(|(day_index, cell)| {
                (cell.date.as_deref() == Some(hovered_date.as_str())).then(|| {
                    usage_heat_tooltip(
                        hovered_date,
                        *tokens,
                        week_index,
                        day_index,
                        weeks.len(),
                        palette,
                        locale,
                    )
                })
            })
        })
    });
    div()
        .relative()
        .w_full()
        .child(
            div()
                .w_full()
                .flex()
                .gap(px(3.))
                .children(weeks.iter().enumerate().map(|(week_index, week)| {
                    div()
                        .min_w_0()
                        .flex_1()
                        .flex()
                        .flex_col()
                        .gap(px(3.))
                        .children(week.iter().enumerate().map(|(day_index, cell)| {
                            let date = cell.date.clone();
                            let tokens = cell.tokens;
                            div()
                                .id(("usage-day", week_index * 7 + day_index))
                                .w_full()
                                .h(px(8.))
                                .rounded(px(2.))
                                .bg(if cell.in_range {
                                    usage_heat_color(
                                        usage_activity_level(cell.tokens, peak),
                                        palette,
                                    )
                                } else {
                                    rgba(0x00000000)
                                })
                                .when_some(date, |day, date| {
                                    let clear_key = date.clone();
                                    day.aria_label(format!(
                                        "{date}: {} Token",
                                        format_usage_exact(tokens)
                                    ))
                                    .cursor_pointer()
                                    .hover(|style| style.opacity(0.72))
                                    .on_hover(cx.listener(
                                        move |this, hovered: &bool, _, cx| {
                                            if *hovered {
                                                this.usage_hover = Some((date.clone(), tokens));
                                            } else if this
                                                .usage_hover
                                                .as_ref()
                                                .is_some_and(|(current, _)| current == &clear_key)
                                            {
                                                this.usage_hover = None;
                                            }
                                            cx.notify();
                                        },
                                    ))
                                })
                        }))
                })),
        )
        .when_some(hovered, |heatmap, tooltip| heatmap.child(tooltip))
        .into_any_element()
}

fn usage_model_share_view(
    report: &serde_json::Value,
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
) -> Option<gpui::AnyElement> {
    let models = report.get("models")?.as_array()?;
    let total = models
        .iter()
        .map(|model| usage_i64(model, "tokens"))
        .sum::<i64>();
    let colors = [
        palette.accent,
        rgb(0x55a9ff),
        rgb(0x8bc5ff),
        palette.border_strong,
    ];
    let shares = models
        .iter()
        .take(4)
        .enumerate()
        .map(|(index, model)| {
            let fraction = if total > 0 {
                usage_i64(model, "tokens") as f32 / total as f32
            } else {
                0.
            };
            (
                index,
                usage_model_identity(model, state, locale).0,
                fraction,
            )
        })
        .collect::<Vec<_>>();
    (!shares.is_empty()).then(|| {
        div()
            .mt_1()
            .flex()
            .flex_col()
            .gap_3()
            .child(
                div()
                    .text_xs()
                    .text_color(palette.faint)
                    .child(locale.text("ui.byModel")),
            )
            .child(
                div()
                    .w_full()
                    .h(px(6.))
                    .rounded_full()
                    .overflow_hidden()
                    .bg(palette.paper_muted)
                    .flex()
                    .children(shares.iter().map(|(index, _, fraction)| {
                        div().h_full().w(relative(*fraction)).bg(colors[*index])
                    })),
            )
            .child(div().flex().flex_wrap().gap_3().children(shares.iter().map(
                |(index, title, fraction)| {
                    div()
                        .flex()
                        .items_center()
                        .gap(px(6.))
                        .text_xs()
                        .text_color(palette.muted)
                        .child(div().size(px(6.)).rounded_full().bg(colors[*index]))
                        .child(div().max_w(px(150.)).truncate().child(title.clone()))
                        .child(format!("{:.0}%", fraction * 100.))
                },
            )))
            .into_any_element()
    })
}

fn usage_activity_view(
    report: &serde_json::Value,
    state: &AppState,
    usage_hover: Option<&(String, i64)>,
    palette: ThemePalette,
    locale: Locale,
    reduced_motion: bool,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let (weeks, peak) = usage_heatmap(report);
    let heatmap = usage_heatmap_view(&weeks, peak, usage_hover, palette, locale, cx);
    let content = div()
        .w_full()
        .p_4()
        .pt_3()
        .flex()
        .flex_col()
        .gap_3()
        .child(heatmap)
        .child(
            div()
                .flex()
                .items_center()
                .child(
                    div()
                        .flex_1()
                        .text_xs()
                        .text_color(palette.faint)
                        .child(locale.text("ui.past366Days")),
                )
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap(px(4.))
                        .text_xs()
                        .text_color(palette.faint)
                        .child(locale.text("ui.less"))
                        .children((0..4).map(|level| {
                            div()
                                .size(px(9.))
                                .rounded(px(2.))
                                .bg(usage_heat_color(level, palette))
                        }))
                        .child(locale.text("ui.more")),
                ),
        )
        .when_some(
            usage_model_share_view(report, state, palette, locale),
            |activity, share| activity.child(share),
        );
    let content = if reduced_motion {
        content.into_any_element()
    } else {
        content
            .with_animation(
                ("usage-activity", usage_i64(report, "totalTokens") as usize),
                Animation::new(USAGE_REVEAL_TRANSITION).with_easing(gpui::ease_out_quint()),
                |activity, progress| {
                    activity
                        .relative()
                        .top(px(5. * (1. - progress)))
                        .opacity(progress)
                },
            )
            .into_any_element()
    };
    div()
        .border_t_1()
        .border_color(palette.border)
        .child(
            div()
                .h(px(44.))
                .px_4()
                .flex()
                .items_center()
                .child(
                    div()
                        .flex_1()
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(locale.text("ui.tokenActivity")),
                )
                .child(
                    div()
                        .text_xs()
                        .text_color(palette.faint)
                        .child(locale.text("ui.daily")),
                ),
        )
        .child(content)
        .into_any_element()
}

fn usage_total_node(
    report: &serde_json::Value,
    palette: ThemePalette,
    locale: Locale,
    reduced_motion: bool,
) -> gpui::AnyElement {
    let total = usage_i64(report, "totalTokens");
    let total_node = div()
        .mt_2()
        .px_4()
        .text_size(px(34.))
        .line_height(px(39.))
        .font_weight(gpui::FontWeight::MEDIUM)
        .text_color(palette.ink);
    if reduced_motion {
        total_node
            .child(format_usage_count(total, locale))
            .into_any_element()
    } else {
        total_node
            .with_animation(
                ("usage-total", total as usize),
                Animation::new(USAGE_REVEAL_TRANSITION).with_easing(gpui::ease_out_quint()),
                move |node, progress| {
                    node.child(format_usage_count(
                        (total as f64 * progress as f64).round() as i64,
                        locale,
                    ))
                },
            )
            .into_any_element()
    }
}

fn usage_token_split(report: &serde_json::Value, locale: Locale) -> Vec<String> {
    let cache_read = if report
        .get("cacheReported")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
    {
        format_usage_count(usage_i64(report, "cacheReadTokens"), locale)
    } else {
        "—".into()
    };
    let cache_write = if report
        .get("cacheWriteReported")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
    {
        format_usage_count(usage_i64(report, "cacheWriteTokens"), locale)
    } else {
        "—".into()
    };
    [
        (
            "usage.input",
            format_usage_count(usage_i64(report, "inputTokens"), locale),
        ),
        (
            "usage.output",
            format_usage_count(usage_i64(report, "outputTokens"), locale),
        ),
        ("usage.cacheRead", cache_read),
        ("usage.cacheWrite", cache_write),
        ("usage.cacheHit", usage_cache_hit(report)),
    ]
    .into_iter()
    .map(|(key, value)| locale.format(key, &[("value", value)]))
    .collect()
}

fn usage_report_facts(
    report: &serde_json::Value,
    palette: ThemePalette,
    locale: Locale,
) -> Vec<gpui::Div> {
    vec![
        usage_fact(
            locale.text("ui.sessions"),
            format_usage_count(usage_i64(report, "sessions"), locale),
            palette,
        ),
        usage_fact(
            locale.text("ui.runs"),
            format_usage_count(usage_i64(report, "runs"), locale),
            palette,
        ),
        usage_fact(
            locale.text("ui.requests"),
            format_usage_count(usage_i64(report, "requests"), locale),
            palette,
        ),
        usage_fact(
            locale.text("ui.peakDay"),
            if usage_i64(report, "peakDayTokens") > 0 {
                format_usage_count(usage_i64(report, "peakDayTokens"), locale)
            } else {
                "—".into()
            },
            palette,
        ),
        usage_fact(
            locale.text("ui.currentStreak"),
            locale.format(
                "ui.arg0Days",
                &[("arg0", (usage_i64(report, "currentStreak")).to_string())],
            ),
            palette,
        ),
        usage_fact(
            locale.text("ui.longestStreak"),
            locale.format(
                "ui.arg0Days",
                &[("arg0", (usage_i64(report, "longestStreak")).to_string())],
            ),
            palette,
        ),
        usage_fact(
            locale.text("ui.longestRun"),
            format_usage_duration(usage_i64(report, "longestRunMs"), locale),
            palette,
        ),
    ]
}

fn usage_report_view(
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
    usage_hover: Option<&(String, i64)>,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let report = &state.settings.usage;
    let reduced_motion = state
        .settings
        .appearance
        .get("reducedMotion")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false);
    let from = report
        .get("from")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    let to = report
        .get("to")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default();
    div()
        .rounded(px(14.))
        .border_1()
        .border_color(palette.border)
        .bg(palette.paper)
        .overflow_hidden()
        .child(
            div()
                .px_4()
                .pt_4()
                .flex()
                .flex_col()
                .gap(px(3.))
                .child(
                    div()
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(locale.text("ui.usageLedger")),
                )
                .child(
                    div()
                        .text_xs()
                        .text_color(palette.muted)
                        .child(locale.format(
                            "ui.usageFromFromToTo",
                            &[("from", (from).to_string()), ("to", (to).to_string())],
                        )),
                ),
        )
        .child(usage_total_node(report, palette, locale, reduced_motion))
        .child(
            div()
                .px_4()
                .text_xs()
                .text_color(palette.muted)
                .child(locale.text("ui.totalTokens")),
        )
        .child(
            div()
                .mt_3()
                .px_4()
                .pb_4()
                .flex()
                .flex_wrap()
                .gap_3()
                .children(
                    usage_token_split(report, locale)
                        .into_iter()
                        .map(|value| div().text_xs().text_color(palette.muted).child(value)),
                ),
        )
        .child(
            div()
                .border_t_1()
                .border_color(palette.border)
                .p_4()
                .flex()
                .flex_wrap()
                .gap_3()
                .children(usage_report_facts(report, palette, locale)),
        )
        .child(usage_activity_view(
            report,
            state,
            usage_hover,
            palette,
            locale,
            reduced_motion,
            cx,
        ))
        .child(
            div()
                .border_t_1()
                .border_color(palette.border)
                .flex()
                .items_start()
                .child(usage_kind_rows(report, palette, locale))
                .child(usage_model_rows(state, palette, locale)),
        )
        .into_any_element()
}

pub(super) fn settings_usage_body(
    state: &AppState,
    palette: ThemePalette,
    locale: Locale,
    usage_hover: Option<&(String, i64)>,
    cx: &mut Context<AzemWindow>,
) -> gpui::AnyElement {
    let scope = state
        .settings
        .usage
        .get("scope")
        .and_then(serde_json::Value::as_str)
        .unwrap_or("project");
    let project_scope = scope != "all";
    let refresh_scope = if project_scope { "project" } else { "all" };
    let loading = state.settings.usage.is_null();
    let empty = state
        .settings
        .usage
        .get("empty")
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false);
    let report =
        if loading {
            usage_loading_view(palette, locale)
        } else if empty {
            div()
                .min_h(px(116.))
                .rounded(px(12.))
                .border_1()
                .border_color(palette.border)
                .bg(palette.paper)
                .px_4()
                .flex()
                .flex_col()
                .justify_center()
                .gap_2()
                .child(
                    div()
                        .text_sm()
                        .font_weight(gpui::FontWeight::SEMIBOLD)
                        .child(locale.text("ui.noUsageYetCompletedSessionsWillAppearHere")),
                )
                .child(div().text_sm().text_color(palette.muted).child(locale.text(
                    "ui.onlyCompletedModelRequestsCountUnknownCacheTelemetryRemainsUnreported",
                )))
                .into_any_element()
        } else {
            usage_report_view(state, palette, locale, usage_hover, cx)
        };
    div()
        .w_full()
        .flex()
        .flex_col()
        .gap_4()
        .child(
            div()
                .h(px(40.))
                .flex()
                .items_center()
                .child(
                    div()
                        .h(px(38.))
                        .p(px(3.))
                        .rounded(px(9.))
                        .bg(palette.paper_muted)
                        .flex()
                        .child(
                            div()
                                .id("usage-scope-project")
                                .role(Role::Button)
                                .aria_label(locale.text("ui.currentProject"))
                                .aria_selected(project_scope)
                                .tab_stop(state.connection.connected)
                                .h_full()
                                .px_3()
                                .rounded(px(7.))
                                .bg(if project_scope {
                                    palette.paper
                                } else {
                                    palette.paper_muted
                                })
                                .text_color(if project_scope {
                                    palette.ink
                                } else {
                                    palette.muted
                                })
                                .text_sm()
                                .flex()
                                .items_center()
                                .cursor_pointer()
                                .on_click(cx.listener(|this, _, _, cx| {
                                    let id = this
                                        .runtime
                                        .request(Method::UsageReport, json!({"scope": "project"}));
                                    this.pending_requests.insert(id, PendingRequest::Usage);
                                    cx.notify();
                                }))
                                .child(locale.text("ui.currentProject")),
                        )
                        .child(
                            div()
                                .id("usage-scope-all")
                                .role(Role::Button)
                                .aria_label(locale.text("ui.allProjects"))
                                .aria_selected(!project_scope)
                                .tab_stop(state.connection.connected)
                                .h_full()
                                .px_3()
                                .rounded(px(7.))
                                .bg(if project_scope {
                                    palette.paper_muted
                                } else {
                                    palette.paper
                                })
                                .text_sm()
                                .text_color(if project_scope {
                                    palette.muted
                                } else {
                                    palette.ink
                                })
                                .flex()
                                .items_center()
                                .cursor_pointer()
                                .on_click(cx.listener(|this, _, _, cx| {
                                    let id = this
                                        .runtime
                                        .request(Method::UsageReport, json!({"scope": "all"}));
                                    this.pending_requests.insert(id, PendingRequest::Usage);
                                    cx.notify();
                                }))
                                .child(locale.text("ui.allProjects")),
                        ),
                )
                .child(div().flex_1())
                .child(
                    div()
                        .id("refresh-usage")
                        .role(Role::Button)
                        .aria_label(locale.text("ui.refresh"))
                        .tab_stop(state.connection.connected)
                        .h(px(34.))
                        .px_3()
                        .rounded(px(8.))
                        .border_1()
                        .border_color(palette.border)
                        .text_sm()
                        .flex()
                        .items_center()
                        .gap_2()
                        .cursor_pointer()
                        .on_click(cx.listener(move |this, _, _, cx| {
                            let id = this
                                .runtime
                                .request(Method::UsageReport, json!({"scope": refresh_scope}));
                            this.pending_requests.insert(id, PendingRequest::Usage);
                            cx.notify();
                        }))
                        .child(icon("refresh-cw", 14., palette.muted))
                        .child(locale.text("ui.refresh")),
                ),
        )
        .child(report)
        .into_any_element()
}

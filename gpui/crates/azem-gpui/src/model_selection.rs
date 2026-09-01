use serde_json::Value;

const TIERS: [&str; 9] = [
    "default", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra",
];

pub struct ModelChoice<'a> {
    pub model: &'a Value,
    pub family_name: Option<String>,
    pub aliases: String,
    pub reasoning: String,
    pub selected: bool,
    pub variant_count: usize,
    pub no_zdr: bool,
}

#[derive(Default, Debug, PartialEq, Eq)]
pub struct ModelModes {
    pub levels: Vec<String>,
    pub reasoning: String,
    pub fast: bool,
    pub fast_available: bool,
}

fn string<'a>(value: &'a Value, key: &str) -> &'a str {
    value[key].as_str().unwrap_or_default()
}

fn strings(value: &Value, key: &str) -> Vec<String> {
    value[key]
        .as_array()
        .into_iter()
        .flatten()
        .filter_map(Value::as_str)
        .map(str::to_owned)
        .collect()
}

fn enabled_models(provider: &Value) -> impl Iterator<Item = &Value> {
    provider["models"]
        .as_array()
        .into_iter()
        .flatten()
        .filter(move |model| {
            provider["enabled"] != false
                && model["disabled"] != true
                && !string(model, "id").is_empty()
        })
}

fn provider<'a>(providers: &'a [Value], id: &str) -> Option<&'a Value> {
    providers
        .iter()
        .find(|provider| string(provider, "id") == id && provider["enabled"] != false)
}

fn sorted_levels(mut levels: Vec<String>) -> Vec<String> {
    levels.sort_by_key(|level| {
        TIERS
            .iter()
            .position(|tier| tier == level)
            .unwrap_or(usize::MAX)
    });
    levels.dedup();
    levels
}

fn reasoning_levels(provider: &Value, model: &Value) -> Vec<String> {
    sorted_levels(
        strings(model, "reasoningLevels")
            .into_iter()
            // ChatGPT Ultra is a mode, not a selectable reasoning depth.
            .filter(|level| string(provider, "id") != "chatgpt" || level != "ultra")
            .collect(),
    )
}

struct CursorVariant<'a> {
    model: &'a Value,
    family: String,
    tier: String,
    thinking: bool,
    fast: bool,
}

impl<'a> CursorVariant<'a> {
    fn parse(model: &'a Value) -> Self {
        let id = string(model, "id").to_ascii_lowercase();
        let mut parts: Vec<_> = id.split('-').filter(|part| !part.is_empty()).collect();
        let pop = |parts: &mut Vec<&str>, suffix| {
            if parts.last() == Some(&suffix) {
                parts.pop();
                true
            } else {
                false
            }
        };
        let fast = pop(&mut parts, "fast");
        let mut thinking = pop(&mut parts, "thinking");
        let tier = if parts.ends_with(&["extra", "high"]) {
            parts.truncate(parts.len() - 2);
            "xhigh"
        } else if parts.last().is_some_and(|part| TIERS[1..8].contains(part)) {
            parts.pop().unwrap()
        } else {
            "default"
        };
        thinking |= pop(&mut parts, "thinking");
        Self {
            model,
            family: parts.join("-"),
            tier: tier.to_owned(),
            thinking,
            fast,
        }
    }

    fn id(&self) -> &str {
        string(self.model, "id")
    }
}

struct CursorGroup<'a> {
    variants: Vec<CursorVariant<'a>>,
}

impl<'a> CursorGroup<'a> {
    fn find(&self, tier: &str, thinking: bool, fast: bool) -> Option<&CursorVariant<'a>> {
        self.variants.iter().find(|variant| {
            variant.tier == tier && variant.thinking == thinking && variant.fast == fast
        })
    }

    fn thinking(&self, fast: bool) -> bool {
        self.variants
            .iter()
            .any(|variant| variant.thinking && variant.fast == fast)
    }

    fn name(&self) -> String {
        self.variants
            .iter()
            .map(|variant| {
                let mut name = string(variant.model, "name").to_owned();
                while let Some(start) = name.to_ascii_lowercase().find("(no zdr)") {
                    name.replace_range(start..start + 8, "");
                }
                name = name.trim().to_owned();
                loop {
                    let lower = name.to_ascii_lowercase();
                    let suffix = [
                        " extra high",
                        " fast",
                        " thinking",
                        " high",
                        " medium",
                        " low",
                        " max",
                        " minimal",
                        " none",
                        " xhigh",
                    ]
                    .iter()
                    .find(|suffix| lower.ends_with(**suffix));
                    let Some(suffix) = suffix else { break };
                    name.truncate(name.len() - suffix.len());
                    name = name.trim_end().to_owned();
                }
                if name.is_empty() {
                    name = variant.family.replace('-', " ");
                }
                (variant.thinking || variant.fast, name)
            })
            .min_by_key(|(variant_name, name)| (*variant_name, name.len(), name.clone()))
            .map(|(_, name)| name)
            .unwrap_or_default()
    }
}

fn cursor_groups(provider: &Value) -> Vec<CursorGroup<'_>> {
    let mut groups: Vec<CursorGroup<'_>> = Vec::new();
    for model in enabled_models(provider) {
        let variant = CursorVariant::parse(model);
        if let Some(group) = groups
            .iter_mut()
            .find(|group| group.variants[0].family == variant.family)
        {
            group.variants.push(variant);
        } else {
            groups.push(CursorGroup {
                variants: vec![variant],
            });
        }
    }
    for group in &mut groups {
        group.variants.sort_by_key(|variant| {
            (
                TIERS
                    .iter()
                    .position(|tier| *tier == variant.tier)
                    .unwrap_or(usize::MAX),
                variant.thinking,
                variant.fast,
                variant.id().to_owned(),
            )
        });
    }
    groups
}

pub fn model_choices<'a>(
    provider: &'a Value,
    selected_model: &str,
    reasoning: &str,
) -> Vec<ModelChoice<'a>> {
    if string(provider, "id") != "cursor" {
        return enabled_models(provider)
            .map(|model| ModelChoice {
                model,
                family_name: None,
                aliases: strings(model, "aliases").join(" "),
                reasoning: {
                    let levels = reasoning_levels(provider, model);
                    let default = string(model, "defaultReasoning");
                    levels
                        .iter()
                        .find(|level| level.as_str() == default)
                        .or_else(|| levels.first())
                        .cloned()
                        .unwrap_or_default()
                },
                selected: string(model, "id") == selected_model,
                variant_count: 0,
                no_zdr: false,
            })
            .collect();
    }
    let groups = cursor_groups(provider);
    let fast = groups
        .iter()
        .flat_map(|group| &group.variants)
        .find(|variant| variant.id() == selected_model)
        .is_some_and(|variant| variant.fast);
    groups
        .iter()
        .map(|group| {
            let selected = group
                .variants
                .iter()
                .find(|variant| variant.id() == selected_model);
            let thinking = group.thinking(fast);
            let variant = selected
                .or_else(|| group.find(reasoning, thinking, fast))
                .or_else(|| group.find(reasoning, group.thinking(false), false))
                .or_else(|| group.find("default", thinking, fast))
                .or_else(|| group.find("medium", thinking, fast))
                .or_else(|| group.find("high", thinking, fast))
                .or_else(|| {
                    group
                        .variants
                        .iter()
                        .find(|variant| variant.thinking == thinking && variant.fast == fast)
                })
                .unwrap_or(&group.variants[0]);
            ModelChoice {
                model: variant.model,
                family_name: Some(group.name()),
                aliases: group
                    .variants
                    .iter()
                    .flat_map(|variant| {
                        let mut aliases = strings(variant.model, "aliases");
                        aliases.extend([
                            variant.id().to_owned(),
                            string(variant.model, "name").to_owned(),
                        ]);
                        aliases
                    })
                    .collect::<Vec<_>>()
                    .join(" "),
                reasoning: variant.tier.clone(),
                selected: selected.is_some(),
                variant_count: group.variants.len(),
                no_zdr: group.variants.iter().any(|variant| {
                    string(variant.model, "name")
                        .to_ascii_lowercase()
                        .contains("(no zdr)")
                }),
            }
        })
        .collect()
}

pub fn model_modes(
    providers: &[Value],
    provider_id: &str,
    model_id: &str,
    reasoning: &str,
    chatgpt_fast: bool,
) -> ModelModes {
    let Some(provider) = provider(providers, provider_id) else {
        return ModelModes::default();
    };
    if provider_id == "cursor" {
        for group in cursor_groups(provider) {
            if let Some(current) = group
                .variants
                .iter()
                .find(|variant| variant.id() == model_id)
            {
                let thinking = group.thinking(current.fast);
                return ModelModes {
                    levels: sorted_levels(
                        group
                            .variants
                            .iter()
                            .filter(|variant| {
                                variant.thinking == thinking && variant.fast == current.fast
                            })
                            .map(|variant| variant.tier.clone())
                            .collect(),
                    ),
                    reasoning: current.tier.clone(),
                    fast: current.fast,
                    fast_available: group.find(&current.tier, thinking, !current.fast).is_some(),
                };
            }
        }
        return ModelModes::default();
    }
    let Some(model) = enabled_models(provider).find(|model| string(model, "id") == model_id) else {
        return ModelModes::default();
    };
    let fast_available = provider_id == "chatgpt"
        && strings(model, "capabilities")
            .iter()
            .any(|capability| capability == "fast");
    ModelModes {
        levels: reasoning_levels(provider, model),
        reasoning: reasoning.to_owned(),
        fast: fast_available && chatgpt_fast,
        fast_available,
    }
}

/// Resolve only advertised, enabled IDs; a missing speed/tier combination never becomes a made-up ID.
pub fn cursor_selection(
    providers: &[Value],
    model_id: &str,
    reasoning: Option<&str>,
    fast: Option<bool>,
) -> Option<(String, String)> {
    let provider = provider(providers, "cursor")?;
    for group in cursor_groups(provider) {
        if let Some(current) = group
            .variants
            .iter()
            .find(|variant| variant.id() == model_id)
        {
            let variant = group.find(
                reasoning.unwrap_or(&current.tier),
                group.thinking(current.fast),
                fast.unwrap_or(current.fast),
            )?;
            return Some((variant.id().to_owned(), variant.tier.clone()));
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn cursor_depth_and_fast_select_exact_enabled_account_ids() {
        let providers = vec![json!({"id":"cursor","enabled":true,"models":[
            {"id":"gpt-5.6-sol-low","name":"GPT-5.6 Sol Low"},
            {"id":"gpt-5.6-sol-high","name":"GPT-5.6 Sol High"},
            {"id":"gpt-5.6-sol-low-fast","name":"GPT-5.6 Sol Low Fast"},
            {"id":"gpt-5.6-sol-high-fast","name":"GPT-5.6 Sol High Fast"},
            {"id":"gpt-5.6-sol-max-fast","disabled":true},
            {"id":"gpt-5.6-sol-mini-high","name":"GPT-5.6 Sol Mini High"}
        ]})];
        let modes = model_modes(&providers, "cursor", "gpt-5.6-sol-low-fast", "wrong", false);
        assert_eq!(modes.levels, ["low", "high"]);
        assert_eq!(modes.reasoning, "low");
        assert!(modes.fast && modes.fast_available);
        let high =
            cursor_selection(&providers, "gpt-5.6-sol-low-fast", Some("high"), None).unwrap();
        assert_eq!(high, ("gpt-5.6-sol-high-fast".into(), "high".into()));
        assert_eq!(
            cursor_selection(&providers, &high.0, None, Some(false))
                .unwrap()
                .0,
            "gpt-5.6-sol-high"
        );
        assert!(cursor_selection(&providers, &high.0, Some("max"), None).is_none());
        let choices = model_choices(&providers[0], &high.0, "high");
        assert_eq!(choices.len(), 2);
        assert_eq!(choices[0].variant_count, 4);
        assert_eq!(choices[0].family_name.as_deref(), Some("GPT-5.6 Sol"));
        assert!(choices[0].selected);
        assert!(choices[0].aliases.contains("gpt-5.6-sol-low-fast"));
        assert!(!choices[0].aliases.contains("max-fast"));
    }

    #[test]
    fn cursor_thinking_is_implicit_without_downgrading_or_inventing_fast_variants() {
        let providers = vec![json!({"id":"cursor","models":[
            {"id":"claude-fable-5-high","name":"Claude Fable 5 1M High (NO ZDR)"},
            {"id":"claude-fable-5-thinking-high","name":"Claude Fable 5 1M Thinking High (NO ZDR)"},
            {"id":"claude-fable-5-low-thinking","name":"Claude Fable 5 1M Low Thinking (NO ZDR)"},
            {"id":"claude-fable-5-thinking-extra-high","name":"Claude Fable 5 1M Thinking Extra High (NO ZDR)"},
            {"id":"claude-fable-5-high-fast","name":"Claude Fable 5 1M High Fast (NO ZDR)"}
        ]})];
        let choices = model_choices(&providers[0], "", "high");
        assert_eq!(choices.len(), 1);
        assert_eq!(choices[0].model["id"], "claude-fable-5-thinking-high");
        assert_eq!(choices[0].family_name.as_deref(), Some("Claude Fable 5 1M"));
        assert!(choices[0].no_zdr);
        let modes = model_modes(
            &providers,
            "cursor",
            "claude-fable-5-thinking-high",
            "high",
            true,
        );
        assert_eq!(modes.levels, ["low", "high", "xhigh"]);
        assert!(!modes.fast && !modes.fast_available);
        assert!(
            cursor_selection(&providers, "claude-fable-5-thinking-high", None, Some(true))
                .is_none()
        );
        assert_eq!(
            cursor_selection(&providers, "claude-fable-5-high", Some("xhigh"), None)
                .unwrap()
                .0,
            "claude-fable-5-thinking-extra-high"
        );
    }

    #[test]
    fn non_cursor_depths_and_fast_follow_capabilities_not_names() {
        let mut providers = vec![json!({"id":"chatgpt","models":[
            {"id":"gpt-5.6-sol", "capabilities":["fast"], "reasoningLevels":["ultra","high","low","max","medium","xhigh"], "defaultReasoning":"ultra"},
            {"id":"gpt-standard"}
        ]})];
        let modes = model_modes(&providers, "chatgpt", "gpt-5.6-sol", "high", true);
        assert_eq!(modes.levels, ["low", "medium", "high", "xhigh", "max"]);
        assert_eq!(modes.reasoning, "high");
        assert_eq!(model_choices(&providers[0], "", "")[0].reasoning, "low");
        providers[0]["models"][0]["defaultReasoning"] = json!("medium");
        assert_eq!(model_choices(&providers[0], "", "")[0].reasoning, "medium");
        assert!(modes.fast && modes.fast_available);
        assert!(!model_modes(&providers, "chatgpt", "gpt-standard", "", true).fast_available);
        providers[0]["models"][0]["disabled"] = json!(true);
        assert!(!model_modes(&providers, "chatgpt", "gpt-5.6-sol", "high", true).fast_available);
        providers[0]["id"] = json!("grok");
        providers[0]["models"][0]["disabled"] = json!(false);
        let other = model_modes(&providers, "grok", "gpt-5.6-sol", "high", true);
        assert_eq!(
            other.levels,
            ["low", "medium", "high", "xhigh", "max", "ultra"]
        );
        assert!(!other.fast_available);
        providers[0]["enabled"] = json!(false);
        assert!(model_choices(&providers[0], "", "high").is_empty());
    }
}

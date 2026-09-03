use anyhow::{Context as _, Result, ensure};
use serde::Deserialize;
use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    io::Read,
    path::Path,
    sync::OnceLock,
};

const ENGLISH: &str = include_str!("../../../assets/locales/en.json");
const MAX_PACK_BYTES: u64 = 1024 * 1024;

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Translation {
    pub id: String,
    pub name: String,
    messages: BTreeMap<String, String>,
    #[serde(default, rename = "compactNumbers")]
    compact_numbers: Vec<CompactUnit>,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CompactUnit {
    pub divisor: u64,
    pub suffix: String,
    pub digits: usize,
}

fn valid_id(id: &str) -> bool {
    let mut parts = id.split('-');
    let first = parts.next().unwrap_or_default();
    id.len() <= 64
        && (2..=8).contains(&first.len())
        && first.bytes().all(|c| c.is_ascii_alphabetic())
        && parts.all(|part| {
            (1..=8).contains(&part.len()) && part.bytes().all(|c| c.is_ascii_alphanumeric())
        })
}

fn placeholders(value: &str) -> BTreeSet<&str> {
    value
        .split('{')
        .skip(1)
        .filter_map(|part| part.split_once('}').map(|(name, _)| name))
        .collect()
}

fn parse_pack(data: &str, expected_id: &str, fallback: &Translation) -> Result<Translation> {
    let pack: Translation = serde_json::from_str(data)?;
    ensure!(
        valid_id(&pack.id) && pack.id == expected_id,
        "Locale ID must match its filename"
    );
    ensure!(
        !pack.name.trim().is_empty()
            && pack.name.len() <= 160
            && !pack.name.chars().any(char::is_control),
        "Invalid locale display name"
    );
    let mut previous = u64::MAX;
    for unit in &pack.compact_numbers {
        ensure!(
            unit.divisor > 1
                && unit.divisor < previous
                && unit.digits <= 6
                && unit.suffix.len() <= 32,
            "Invalid compact number format"
        );
        previous = unit.divisor;
    }
    if pack.id.eq_ignore_ascii_case("en") {
        ensure!(
            pack.messages.len() == fallback.messages.len(),
            "English fallback must be complete"
        );
    }
    for (key, value) in &pack.messages {
        let original = fallback
            .messages
            .get(key)
            .with_context(|| format!("Unknown translation key {key}"))?;
        ensure!(!value.trim().is_empty(), "Empty translation for {key}");
        ensure!(
            placeholders(value) == placeholders(original),
            "Placeholder mismatch for {key}"
        );
    }
    Ok(pack)
}

fn load_directory(
    directory: &Path,
    fallback: &Translation,
    packs: &mut BTreeMap<String, Translation>,
) -> Result<()> {
    if !directory.exists() {
        return Ok(());
    }
    let mut paths = fs::read_dir(directory)?.collect::<std::io::Result<Vec<_>>>()?;
    paths.sort_by_key(|entry| entry.file_name());
    for entry in paths {
        let path = entry.path();
        if !entry.file_type()?.is_file() || path.extension().is_none_or(|ext| ext != "json") {
            continue;
        }
        let result = (|| {
            let mut bytes = Vec::new();
            fs::File::open(&path)?
                .take(MAX_PACK_BYTES + 1)
                .read_to_end(&mut bytes)?;
            ensure!(
                bytes.len() as u64 <= MAX_PACK_BYTES,
                "Translation pack is too large"
            );
            let id = path
                .file_stem()
                .and_then(|value| value.to_str())
                .unwrap_or_default();
            parse_pack(std::str::from_utf8(&bytes)?, id, fallback)
        })();
        match result {
            Ok(pack) => {
                packs.insert(pack.id.to_ascii_lowercase(), pack);
            }
            Err(error) => {
                tracing::warn!(path = %path.display(), %error, "Ignoring invalid translation pack")
            }
        }
    }
    Ok(())
}

pub fn available() -> &'static [Translation] {
    static PACKS: OnceLock<Vec<Translation>> = OnceLock::new();
    PACKS.get_or_init(|| {
        let fallback: Translation =
            serde_json::from_str(ENGLISH).expect("bundled English translations must be valid");
        let mut packs = BTreeMap::new();
        if let Err(error) = load_directory(
            &crate::assets::Assets::discover().locale_directory(),
            &fallback,
            &mut packs,
        ) {
            tracing::warn!(%error, "Cannot load bundled translation packs");
        }
        let user = std::env::var_os("AZEM_HOME")
            .map(std::path::PathBuf::from)
            .or_else(|| dirs::home_dir().map(|path| path.join(".azem")));
        if let Some(user) = user
            && let Err(error) = load_directory(&user.join("locales"), &fallback, &mut packs)
        {
            tracing::warn!(%error, "Cannot load user translation packs");
        }
        packs.entry("en".into()).or_insert(fallback);
        packs.into_values().collect()
    })
}

#[derive(Clone, Copy, Debug)]
pub struct Locale(&'static Translation);

impl Locale {
    pub fn resolve(id: &str) -> Self {
        let mut candidate = id.trim();
        loop {
            if let Some(pack) = available()
                .iter()
                .find(|pack| pack.id.eq_ignore_ascii_case(candidate))
            {
                return Self(pack);
            }
            let Some((parent, _)) = candidate.rsplit_once('-') else {
                break;
            };
            candidate = parent;
        }
        Self(
            available()
                .iter()
                .find(|pack| pack.id.eq_ignore_ascii_case("en"))
                .expect("English fallback exists"),
        )
    }
    pub fn id(self) -> &'static str {
        &self.0.id
    }
    pub fn compact_units(self) -> &'static [CompactUnit] {
        if self.0.compact_numbers.is_empty() {
            &Self::resolve("en").0.compact_numbers
        } else {
            &self.0.compact_numbers
        }
    }
    pub fn text(self, key: &'static str) -> &'static str {
        self.0
            .messages
            .get(key)
            .or_else(|| Self::resolve("en").0.messages.get(key))
            .map(String::as_str)
            .unwrap_or(key)
    }
    // Translate known protocol values; preserve unknown values and external content.
    pub fn value<'a>(self, group: &str, value: &'a str) -> &'a str {
        let key = format!("{group}.{}", value.to_ascii_lowercase());
        self.0
            .messages
            .get(&key)
            .or_else(|| Self::resolve("en").0.messages.get(&key))
            .map(String::as_str)
            .unwrap_or(value)
    }
    pub fn format(self, key: &'static str, values: &[(&str, String)]) -> String {
        // Replace template tokens only; braces in supplied values are literal text.
        let mut template = self.text(key);
        let mut output = String::new();
        while let Some((before, tail)) = template.split_once('{') {
            output.push_str(before);
            let Some((name, rest)) = tail.split_once('}') else {
                output.push('{');
                output.push_str(tail);
                return output;
            };
            if let Some((_, value)) = values.iter().find(|(key, _)| *key == name) {
                output.push_str(value);
            } else {
                output.push('{');
                output.push_str(name);
                output.push('}');
            }
            template = rest;
        }
        output.push_str(template);
        output
    }
}

#[derive(Clone, Copy)]
pub struct Labels {
    pub application: &'static str,
    pub new_conversation: &'static str,
    pub conversation: &'static str,
    pub workspace: &'static str,
    pub search: &'static str,
    pub projects: &'static str,
    pub files: &'static str,
    pub changes: &'static str,
    pub pull_requests: &'static str,
    pub security: &'static str,
    pub terminal: &'static str,
    pub settings: &'static str,
    pub composer: &'static str,
    pub attach: &'static str,
    pub guide: &'static str,
    pub send: &'static str,
    pub queue: &'static str,
    pub stop: &'static str,
    pub prompt_title: &'static str,
    pub prompt_subtitle: &'static str,
    pub auto_review: &'static str,
    pub plan: &'static str,
}

pub fn labels(language: &str) -> Labels {
    let locale = Locale::resolve(language);
    Labels {
        application: locale.text("app.application"),
        new_conversation: locale.text("app.new_conversation"),
        conversation: locale.text("app.conversation"),
        workspace: locale.text("app.workspace"),
        search: locale.text("app.search"),
        projects: locale.text("app.projects"),
        files: locale.text("app.files"),
        changes: locale.text("app.changes"),
        pull_requests: locale.text("app.pull_requests"),
        security: locale.text("app.security"),
        terminal: locale.text("app.terminal"),
        settings: locale.text("app.settings"),
        composer: locale.text("app.composer"),
        attach: locale.text("app.attach"),
        guide: locale.text("app.guide"),
        send: locale.text("app.send"),
        queue: locale.text("app.queue"),
        stop: locale.text("app.stop"),
        prompt_title: locale.text("app.prompt_title"),
        prompt_subtitle: locale.text("app.prompt_subtitle"),
        auto_review: locale.text("app.auto_review"),
        plan: locale.text("app.plan"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn bundled_translations_have_identical_keys_and_placeholders() {
        let fallback: Translation = serde_json::from_str(ENGLISH).unwrap();
        let mut packs = BTreeMap::new();
        let directory = Path::new(env!("CARGO_MANIFEST_DIR")).join("../../assets/locales");
        // Unlike startup, tests must fail on every malformed bundled file.
        for entry in fs::read_dir(&directory).unwrap() {
            let path = entry.unwrap().path();
            if path.extension().is_none_or(|ext| ext != "json") {
                continue;
            }
            let id = path.file_stem().unwrap().to_str().unwrap();
            let pack = parse_pack(&fs::read_to_string(&path).unwrap(), id, &fallback)
                .unwrap_or_else(|error| panic!("{}: {error}", path.display()));
            assert!(
                packs.insert(id.to_ascii_lowercase(), pack).is_none(),
                "Duplicate language {id}"
            );
        }
        assert!(packs.contains_key("en"));
        assert!(packs.contains_key("zh-cn"));
        for pack in packs.values() {
            assert_eq!(
                pack.messages.keys().collect::<Vec<_>>(),
                fallback.messages.keys().collect::<Vec<_>>(),
                "{} is incomplete",
                pack.id
            );
        }
        assert_eq!(labels("en").new_conversation, "New conversation");
        assert_eq!(labels("zh-CN").new_conversation, "新建对话");
        assert_eq!(labels("unknown").settings, "Settings and extensions");
        assert_eq!(Locale::resolve("EN-us").id(), "en");
        assert_eq!(Locale::resolve("zh-CN-extra").id(), "zh-CN");
        let prefixes = fallback
            .messages
            .keys()
            .filter_map(|key| key.split_once('.').map(|(prefix, _)| prefix))
            .collect::<BTreeSet<_>>();
        let assert_source_keys = |source: &str| {
            for key in source.split('"') {
                if let Some((prefix, _)) = key.split_once('.')
                    && prefixes.contains(prefix)
                    && key
                        .bytes()
                        .all(|c| c.is_ascii_alphanumeric() || c == b'.' || c == b'_')
                {
                    assert!(
                        fallback.messages.contains_key(key),
                        "Missing translation key {key}"
                    );
                }
            }
        };
        for source in [crate::MAIN_SOURCE, crate::SURFACES_SOURCE] {
            assert_source_keys(source);
        }
        let localization_source = include_str!("localization.rs");
        assert_source_keys(localization_source.split("#[cfg(test)]").next().unwrap());
    }
    #[test]
    fn new_translation_files_are_discovered_without_a_language_enum() {
        let path = std::env::temp_dir().join(format!("azem-locales-{}", uuid::Uuid::new_v4()));
        fs::create_dir(&path).unwrap();
        fs::write(
            path.join("ja.json"),
            r#"{"id":"ja","name":"日本語","messages":{"app.settings":"設定"}}"#,
        )
        .unwrap();
        fs::write(path.join("broken.json"), "not JSON").unwrap();
        let fallback: Translation = serde_json::from_str(ENGLISH).unwrap();
        let mut packs = BTreeMap::new();
        load_directory(&path, &fallback, &mut packs).unwrap();
        assert_eq!(packs.len(), 1);
        let locale = Locale(Box::leak(Box::new(packs.remove("ja").unwrap())));
        assert_eq!(locale.text("app.settings"), "設定");
        assert_eq!(locale.text("app.new_conversation"), "New conversation");
        fs::remove_dir_all(path).unwrap();
    }
    #[test]
    fn invalid_translation_metadata_and_placeholders_are_rejected() {
        let fallback: Translation = serde_json::from_str(
            r#"{"id":"en","name":"English","messages":{"message":"Hello {name}"}}"#,
        )
        .unwrap();
        for data in [
            r#"{"id":"../ja","name":"日本語","messages":{}}"#,
            r#"{"id":"ja","name":"","messages":{}}"#,
            r#"{"id":"ja","name":"日本語","messages":{"message":"Hello {wrong}"}}"#,
            r#"{"id":"ja","name":"日本語","messages":{"typo":"Hello"}}"#,
        ] {
            assert!(parse_pack(data, "ja", &fallback).is_err());
        }
        let pack = parse_pack(
            r#"{"id":"ja","name":"日本語","messages":{"message":"{name} {name}"}}"#,
            "ja",
            &fallback,
        )
        .unwrap();
        let locale = Locale(Box::leak(Box::new(pack)));
        assert_eq!(
            locale.format("message", &[("name", "{other}".into())]),
            "{other} {other}"
        );
    }
}

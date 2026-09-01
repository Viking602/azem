use gpui::{App, Global, Rgba, Window, WindowAppearance, rgb};
use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Clone, Debug, PartialEq, Deserialize, Serialize)]
#[serde(default, rename_all = "camelCase")]
pub struct AppearancePreferences {
    pub theme: String,
    pub font: String,
    pub ui_font_size: f32,
    pub chat_font_size: f32,
    pub code_font_size: f32,
    pub reduced_motion: bool,
}

impl Default for AppearancePreferences {
    fn default() -> Self {
        Self {
            theme: "system".into(),
            font: "system".into(),
            ui_font_size: 13.,
            chat_font_size: 14.,
            code_font_size: 14.,
            reduced_motion: false,
        }
    }
}

impl Global for AppearancePreferences {}

impl AppearancePreferences {
    pub fn current(cx: &App) -> Self {
        cx.try_global::<Self>().cloned().unwrap_or_default()
    }

    pub fn changed(&self, key: &str, value: Value) -> anyhow::Result<Self> {
        anyhow::ensure!(
            [
                "theme",
                "font",
                "uiFontSize",
                "chatFontSize",
                "codeFontSize",
                "reducedMotion"
            ]
            .contains(&key),
            "Unknown appearance setting"
        );
        let mut data = serde_json::to_value(self)?;
        data[key] = value;
        let next: Self = serde_json::from_value(data)?;
        next.validate()?;
        Ok(next)
    }

    fn validate(&self) -> anyhow::Result<()> {
        anyhow::ensure!(
            ["light", "dark", "system"].contains(&self.theme.as_str()),
            "Invalid theme"
        );
        anyhow::ensure!(
            !self.font.trim().is_empty()
                && self.font.len() <= 128
                && !self.font.chars().any(char::is_control),
            "Invalid font family"
        );
        for (value, min, max) in [
            (self.ui_font_size, 11., 20.),
            (self.chat_font_size, 12., 20.),
            (self.code_font_size, 11., 18.),
        ] {
            anyhow::ensure!(
                value.is_finite() && value >= min && value <= max,
                "Invalid font size"
            );
        }
        Ok(())
    }

    pub fn load(path: &std::path::Path) -> anyhow::Result<Self> {
        if !path.exists() {
            return Ok(Self::default());
        }
        anyhow::ensure!(
            std::fs::metadata(path)?.len() <= 16 * 1024,
            "Appearance settings are too large"
        );
        let prefs: Self = serde_json::from_slice(&std::fs::read(path)?)?;
        prefs.validate()?;
        Ok(prefs)
    }

    pub fn save(&self, path: &std::path::Path) -> anyhow::Result<()> {
        self.validate()?;
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)?;
        }
        let temporary = path.with_extension(format!("{}.tmp", uuid::Uuid::new_v4()));
        std::fs::write(&temporary, serde_json::to_vec_pretty(self)?)?;
        std::fs::rename(temporary, path)?;
        Ok(())
    }
}

#[derive(Clone, Copy)]
pub struct ThemePalette {
    pub canvas: Rgba,
    pub sidebar: Rgba,
    pub paper: Rgba,
    pub paper_muted: Rgba,
    pub hover: Rgba,
    pub ink: Rgba,
    pub ink_soft: Rgba,
    pub muted: Rgba,
    pub faint: Rgba,
    pub border: Rgba,
    pub border_strong: Rgba,
    pub accent: Rgba,
    pub accent_soft: Rgba,
    pub positive: Rgba,
    pub warning: Rgba,
    pub danger: Rgba,
    pub button: Rgba,
    pub button_text: Rgba,
    pub chat_font_size: f32,
    pub code_font_size: f32,
}

impl ThemePalette {
    pub fn for_window(window: &Window, cx: &App) -> Self {
        let prefs = AppearancePreferences::current(cx);
        let mut palette = Self::for_preference(window.appearance(), &prefs.theme);
        palette.chat_font_size = prefs.chat_font_size;
        palette.code_font_size = prefs.code_font_size;
        palette
    }

    fn for_preference(appearance: WindowAppearance, theme: &str) -> Self {
        Self::for_appearance(match theme {
            "light" => WindowAppearance::Light,
            "dark" => WindowAppearance::Dark,
            _ => appearance,
        })
    }

    fn for_appearance(appearance: WindowAppearance) -> Self {
        match appearance {
            WindowAppearance::Dark | WindowAppearance::VibrantDark => Self {
                canvas: rgb(0x1c1d1f),
                sidebar: rgb(0x1f2022),
                paper: rgb(0x232427),
                paper_muted: rgb(0x2a2b2e),
                hover: rgb(0x313236),
                ink: rgb(0xf2f3f4),
                ink_soft: rgb(0xc7c9cd),
                muted: rgb(0xa5a8ad),
                faint: rgb(0x6c6f75),
                border: rgb(0x2e3033),
                border_strong: rgb(0x3a3c40),
                accent: rgb(0x3d9aff),
                accent_soft: rgb(0x26384b),
                positive: rgb(0x3dbb72),
                warning: rgb(0xf68f3c),
                danger: rgb(0xee5c61),
                button: rgb(0x3c4046),
                button_text: rgb(0xf2f3f4),
                chat_font_size: 14.,
                code_font_size: 14.,
            },
            WindowAppearance::Light | WindowAppearance::VibrantLight => Self {
                canvas: rgb(0xf1f2f3),
                sidebar: rgb(0xf7f8f9),
                paper: rgb(0xffffff),
                paper_muted: rgb(0xf4f5f6),
                hover: rgb(0xe7e9eb),
                ink: rgb(0x1f2124),
                ink_soft: rgb(0x45484d),
                muted: rgb(0x62656b),
                faint: rgb(0x9a9da3),
                border: rgb(0xecedef),
                border_strong: rgb(0xe0e2e5),
                accent: rgb(0x0285ff),
                accent_soft: rgb(0xe9f3ff),
                positive: rgb(0x189a4d),
                warning: rgb(0xef720c),
                danger: rgb(0xe3474c),
                button: rgb(0x1f2124),
                button_text: rgb(0xffffff),
                chat_font_size: 14.,
                code_font_size: 14.,
            },
        }
    }
}

#[cfg(test)]
mod tests {
    use gpui::WindowAppearance;

    use super::{AppearancePreferences, ThemePalette};

    #[test]
    fn system_appearances_select_distinct_palettes() {
        let light = ThemePalette::for_appearance(WindowAppearance::Light);
        let dark = ThemePalette::for_appearance(WindowAppearance::Dark);
        assert_ne!(light.paper, dark.paper);
        assert_ne!(light.ink, dark.ink);
        assert_ne!(light.accent, dark.accent);
    }

    #[test]
    fn explicit_theme_overrides_system_and_system_keeps_following() {
        let light = ThemePalette::for_appearance(WindowAppearance::Light);
        let dark = ThemePalette::for_appearance(WindowAppearance::Dark);
        assert_eq!(
            ThemePalette::for_preference(WindowAppearance::Light, "dark").paper,
            dark.paper
        );
        assert_eq!(
            ThemePalette::for_preference(WindowAppearance::Dark, "light").paper,
            light.paper
        );
        assert_eq!(
            ThemePalette::for_preference(WindowAppearance::Dark, "system").paper,
            dark.paper
        );
    }

    #[test]
    fn primary_buttons_use_dark_surfaces_in_both_themes() {
        for appearance in [WindowAppearance::Light, WindowAppearance::Dark] {
            let palette = ThemePalette::for_appearance(appearance);
            assert!(palette.button.r < 0.3 && palette.button.g < 0.3 && palette.button.b < 0.3);
            assert!(palette.button_text.r > 0.9);
        }
    }

    #[test]
    fn appearance_changes_validate_and_survive_restart() {
        let path =
            std::env::temp_dir().join(format!("azem-appearance-{}.json", uuid::Uuid::new_v4()));
        let original = AppearancePreferences::load(&path).unwrap();
        let mut changed = original.clone();
        for (key, value) in [
            ("theme", serde_json::json!("dark")),
            ("font", serde_json::json!("Songti SC")),
            ("uiFontSize", serde_json::json!(17)),
            ("chatFontSize", serde_json::json!(20)),
            ("codeFontSize", serde_json::json!(18)),
            ("reducedMotion", serde_json::json!(true)),
        ] {
            changed = changed.changed(key, value).unwrap();
        }
        changed.save(&path).unwrap();
        assert_ne!(changed, original);
        assert_eq!(AppearancePreferences::load(&path).unwrap(), changed);
        assert!(
            changed
                .changed("uiFontSize", serde_json::json!(300))
                .is_err()
        );
        assert!(
            changed
                .changed("theme", serde_json::json!("unknown"))
                .is_err()
        );
        assert!(changed.changed("font", serde_json::json!("\n")).is_err());
        original.save(&path).unwrap();
        assert_eq!(AppearancePreferences::load(&path).unwrap(), original);
        std::fs::remove_file(path).unwrap();
    }
}

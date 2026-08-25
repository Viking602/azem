use gpui::{Rgba, Window, WindowAppearance, rgb};

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
}

impl ThemePalette {
    pub fn for_window(window: &Window) -> Self {
        Self::for_appearance(window.appearance())
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
            },
        }
    }
}

#[cfg(test)]
mod tests {
    use gpui::WindowAppearance;

    use super::ThemePalette;

    #[test]
    fn system_appearances_select_distinct_palettes() {
        let light = ThemePalette::for_appearance(WindowAppearance::Light);
        let dark = ThemePalette::for_appearance(WindowAppearance::Dark);
        assert_ne!(light.paper, dark.paper);
        assert_ne!(light.ink, dark.ink);
        assert_ne!(light.accent, dark.accent);
    }
}

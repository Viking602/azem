use gpui::{Rgba, Window, WindowAppearance, rgb};

#[derive(Clone, Copy)]
pub struct ThemePalette {
    pub paper: Rgba,
    pub raised: Rgba,
    pub ink: Rgba,
    pub muted: Rgba,
    pub border: Rgba,
    pub selected: Rgba,
    pub unread: Rgba,
    pub positive: Rgba,
    pub warning: Rgba,
}

impl ThemePalette {
    pub fn for_window(window: &Window) -> Self {
        Self::for_appearance(window.appearance())
    }

    fn for_appearance(appearance: WindowAppearance) -> Self {
        match appearance {
            WindowAppearance::Dark | WindowAppearance::VibrantDark => Self {
                paper: rgb(0x171817),
                raised: rgb(0x202220),
                ink: rgb(0xf1f1ee),
                muted: rgb(0xa6aaa6),
                border: rgb(0x353735),
                selected: rgb(0x313431),
                unread: rgb(0x20342d),
                positive: rgb(0x65c69b),
                warning: rgb(0xe0aa6a),
            },
            WindowAppearance::Light | WindowAppearance::VibrantLight => Self {
                paper: rgb(0xf5f5f3),
                raised: rgb(0xffffff),
                ink: rgb(0x1f2124),
                muted: rgb(0x6f7278),
                border: rgb(0xdeddd7),
                selected: rgb(0xe5e4de),
                unread: rgb(0xebf2ef),
                positive: rgb(0x2f8f6b),
                warning: rgb(0x9a6b35),
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
        assert_ne!(light.selected, dark.selected);
    }
}

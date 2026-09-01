use std::{borrow::Cow, fs, path::PathBuf};

use gpui::{AssetSource, SharedString};

pub struct Assets {
    base: PathBuf,
}

impl Assets {
    pub fn locale_directory(&self) -> PathBuf {
        self.base.join("locales")
    }

    pub fn discover() -> Self {
        let executable_dir = std::env::current_exe()
            .ok()
            .and_then(|executable| executable.parent().map(PathBuf::from));
        let bundled = executable_dir
            .as_ref()
            .and_then(|directory| directory.parent())
            .map(|contents| contents.join("Resources"))
            .filter(|resources| resources.join("icons").is_dir())
            .or_else(|| executable_dir.filter(|directory| directory.join("icons").is_dir()));
        let development = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("../..")
            .join("assets");
        Self {
            base: bundled.unwrap_or(development),
        }
    }
}

impl AssetSource for Assets {
    fn load(&self, path: &str) -> anyhow::Result<Option<Cow<'static, [u8]>>> {
        match fs::read(self.base.join(path)) {
            Ok(bytes) => Ok(Some(Cow::Owned(bytes))),
            Err(error)
                if error.kind() == std::io::ErrorKind::NotFound && path.starts_with("logos/") =>
            {
                fs::read(self.base.join("icons/bot.svg"))
                    .map(|bytes| Some(Cow::Owned(bytes)))
                    .map_err(Into::into)
            }
            Err(error) => Err(error.into()),
        }
    }

    fn list(&self, path: &str) -> anyhow::Result<Vec<SharedString>> {
        fs::read_dir(self.base.join(path))?
            .map(|entry| entry.map(|entry| entry.file_name().to_string_lossy().into_owned().into()))
            .collect::<Result<Vec<_>, _>>()
            .map_err(Into::into)
    }
}

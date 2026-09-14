use std::sync::{Arc, OnceLock};

use gpui::{Image, ImageFormat};

// Keep image identities stable so scrolling reuses GPUI's decoded image cache.
pub(super) fn image(path: &str, directory: bool) -> Arc<Image> {
    static IMAGES: OnceLock<std::collections::HashMap<&'static str, Arc<Image>>> = OnceLock::new();
    let images = IMAGES.get_or_init(|| {
        [
            (
                "folder-base",
                include_bytes!("../../../../assets/file-icons/folder-base.svg").as_slice(),
            ),
            (
                "go",
                include_bytes!("../../../../assets/file-icons/go.svg").as_slice(),
            ),
            (
                "rust",
                include_bytes!("../../../../assets/file-icons/rust.svg").as_slice(),
            ),
            (
                "python",
                include_bytes!("../../../../assets/file-icons/python.svg").as_slice(),
            ),
            (
                "javascript",
                include_bytes!("../../../../assets/file-icons/javascript.svg").as_slice(),
            ),
            (
                "typescript",
                include_bytes!("../../../../assets/file-icons/typescript.svg").as_slice(),
            ),
            (
                "react",
                include_bytes!("../../../../assets/file-icons/react.svg").as_slice(),
            ),
            (
                "markdown",
                include_bytes!("../../../../assets/file-icons/markdown.svg").as_slice(),
            ),
            (
                "document",
                include_bytes!("../../../../assets/file-icons/document.svg").as_slice(),
            ),
            (
                "json",
                include_bytes!("../../../../assets/file-icons/json.svg").as_slice(),
            ),
            (
                "yaml",
                include_bytes!("../../../../assets/file-icons/yaml.svg").as_slice(),
            ),
            (
                "toml",
                include_bytes!("../../../../assets/file-icons/toml.svg").as_slice(),
            ),
            (
                "image",
                include_bytes!("../../../../assets/file-icons/image.svg").as_slice(),
            ),
            (
                "video",
                include_bytes!("../../../../assets/file-icons/video.svg").as_slice(),
            ),
            (
                "audio",
                include_bytes!("../../../../assets/file-icons/audio.svg").as_slice(),
            ),
            (
                "console",
                include_bytes!("../../../../assets/file-icons/console.svg").as_slice(),
            ),
            (
                "database",
                include_bytes!("../../../../assets/file-icons/database.svg").as_slice(),
            ),
            (
                "zip",
                include_bytes!("../../../../assets/file-icons/zip.svg").as_slice(),
            ),
            (
                "font",
                include_bytes!("../../../../assets/file-icons/font.svg").as_slice(),
            ),
            (
                "git",
                include_bytes!("../../../../assets/file-icons/git.svg").as_slice(),
            ),
            (
                "docker",
                include_bytes!("../../../../assets/file-icons/docker.svg").as_slice(),
            ),
            (
                "makefile",
                include_bytes!("../../../../assets/file-icons/makefile.svg").as_slice(),
            ),
            (
                "settings",
                include_bytes!("../../../../assets/file-icons/settings.svg").as_slice(),
            ),
            (
                "license",
                include_bytes!("../../../../assets/file-icons/license.svg").as_slice(),
            ),
            (
                "html",
                include_bytes!("../../../../assets/file-icons/html.svg").as_slice(),
            ),
            (
                "css",
                include_bytes!("../../../../assets/file-icons/css.svg").as_slice(),
            ),
            (
                "java",
                include_bytes!("../../../../assets/file-icons/java.svg").as_slice(),
            ),
            (
                "c",
                include_bytes!("../../../../assets/file-icons/c.svg").as_slice(),
            ),
            (
                "cpp",
                include_bytes!("../../../../assets/file-icons/cpp.svg").as_slice(),
            ),
            (
                "ruby",
                include_bytes!("../../../../assets/file-icons/ruby.svg").as_slice(),
            ),
            (
                "swift",
                include_bytes!("../../../../assets/file-icons/swift.svg").as_slice(),
            ),
            (
                "kotlin",
                include_bytes!("../../../../assets/file-icons/kotlin.svg").as_slice(),
            ),
            (
                "php",
                include_bytes!("../../../../assets/file-icons/php.svg").as_slice(),
            ),
            (
                "vue",
                include_bytes!("../../../../assets/file-icons/vue.svg").as_slice(),
            ),
            (
                "svelte",
                include_bytes!("../../../../assets/file-icons/svelte.svg").as_slice(),
            ),
        ]
        .into_iter()
        .map(|(name, bytes)| {
            (
                name,
                Arc::new(Image::from_bytes(ImageFormat::Svg, bytes.to_vec())),
            )
        })
        .collect()
    });
    images[icon_name(path, directory)].clone()
}

fn icon_name(path: &str, directory: bool) -> &'static str {
    if directory {
        return "folder-base";
    }
    let name = std::path::Path::new(path)
        .file_name()
        .and_then(|s| s.to_str())
        .unwrap_or_default()
        .to_ascii_lowercase();
    match name.as_str() {
        "go.mod" | "go.sum" => return "go",
        "cargo.toml" | "cargo.lock" => return "rust",
        "dockerfile" | "containerfile" => return "docker",
        "makefile" | "gnumakefile" | "justfile" => return "makefile",
        "license" | "licence" | "copying" => return "license",
        _ => {}
    }
    match name.rsplit('.').next().unwrap_or_default() {
        "go" => "go",
        "rs" => "rust",
        "py" | "pyi" => "python",
        "js" | "mjs" | "cjs" => "javascript",
        "ts" | "mts" | "cts" => "typescript",
        "jsx" | "tsx" => "react",
        "java" => "java",
        "kt" | "kts" => "kotlin",
        "swift" => "swift",
        "c" | "h" => "c",
        "cpp" | "hpp" | "cc" => "cpp",
        "rb" => "ruby",
        "php" => "php",
        "html" | "htm" => "html",
        "css" | "scss" | "sass" | "less" => "css",
        "vue" => "vue",
        "svelte" => "svelte",
        "json" | "jsonc" => "json",
        "yml" | "yaml" => "yaml",
        "toml" => "toml",
        "mp4" | "mov" | "webm" => "video",
        _ => match crate::text_input::file_icon(path) {
            "git-branch" => "git",
            "terminal" => "console",
            "box" => "settings",
            "settings" | "braces" => "settings",
            "notebook-pen" => "markdown",
            "image" => "image",
            "audio-lines" => "audio",
            "database" => "database",
            "archive" => "zip",
            "type" => "font",
            _ => "document",
        },
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn file_types_have_colored_bundled_icons_and_stable_images() {
        for (path, directory, expected) in [
            ("src", true, "folder-base"),
            ("main.GO", false, "go"),
            ("Cargo.toml", false, "rust"),
            ("app.tsx", false, "react"),
            ("README.md", false, "markdown"),
            ("photo.png", false, "image"),
            (".gitignore", false, "git"),
            ("unknown.xyz", false, "document"),
        ] {
            assert_eq!(icon_name(path, directory), expected);
            assert!(Arc::ptr_eq(
                &image(path, directory),
                &image(path, directory)
            ));
        }
    }
}

import { Icon, type IconifyIcon } from "@iconify/react/offline";
import iconBat from "@iconify-icons/vscode-icons/file-type-bat";
import iconBun from "@iconify-icons/vscode-icons/file-type-bun";
import iconC from "@iconify-icons/vscode-icons/file-type-c";
import iconCargo from "@iconify-icons/vscode-icons/file-type-cargo";
import iconConfig from "@iconify-icons/vscode-icons/file-type-config";
import iconCpp from "@iconify-icons/vscode-icons/file-type-cpp";
import iconCSharp from "@iconify-icons/vscode-icons/file-type-csharp";
import iconCss from "@iconify-icons/vscode-icons/file-type-css";
import iconDocker from "@iconify-icons/vscode-icons/file-type-docker";
import iconDotenv from "@iconify-icons/vscode-icons/file-type-dotenv";
import iconGit from "@iconify-icons/vscode-icons/file-type-git";
import iconGo from "@iconify-icons/vscode-icons/file-type-go";
import iconGoPackage from "@iconify-icons/vscode-icons/file-type-go-package";
import iconGraphql from "@iconify-icons/vscode-icons/file-type-graphql";
import iconHtml from "@iconify-icons/vscode-icons/file-type-html";
import iconImage from "@iconify-icons/vscode-icons/file-type-image";
import iconIni from "@iconify-icons/vscode-icons/file-type-ini";
import iconJava from "@iconify-icons/vscode-icons/file-type-java";
import iconJavaScript from "@iconify-icons/vscode-icons/file-type-js";
import iconJson from "@iconify-icons/vscode-icons/file-type-json";
import iconKotlin from "@iconify-icons/vscode-icons/file-type-kotlin";
import iconLess from "@iconify-icons/vscode-icons/file-type-less";
import iconLicense from "@iconify-icons/vscode-icons/file-type-license";
import iconLua from "@iconify-icons/vscode-icons/file-type-lua";
import iconMakefile from "@iconify-icons/vscode-icons/file-type-makefile";
import iconMarkdown from "@iconify-icons/vscode-icons/file-type-markdown";
import iconMdx from "@iconify-icons/vscode-icons/file-type-mdx";
import iconNpm from "@iconify-icons/vscode-icons/file-type-npm";
import iconPdf from "@iconify-icons/vscode-icons/file-type-pdf2";
import iconPhp from "@iconify-icons/vscode-icons/file-type-php";
import iconPnpm from "@iconify-icons/vscode-icons/file-type-pnpm";
import iconPowerShell from "@iconify-icons/vscode-icons/file-type-powershell";
import iconPrettier from "@iconify-icons/vscode-icons/file-type-prettier";
import iconProtobuf from "@iconify-icons/vscode-icons/file-type-protobuf";
import iconPython from "@iconify-icons/vscode-icons/file-type-python";
import iconReactJavaScript from "@iconify-icons/vscode-icons/file-type-reactjs";
import iconReactTypeScript from "@iconify-icons/vscode-icons/file-type-reactts";
import iconRuby from "@iconify-icons/vscode-icons/file-type-ruby";
import iconRust from "@iconify-icons/vscode-icons/file-type-rust";
import iconSass from "@iconify-icons/vscode-icons/file-type-sass";
import iconScss from "@iconify-icons/vscode-icons/file-type-scss";
import iconShell from "@iconify-icons/vscode-icons/file-type-shell";
import iconSql from "@iconify-icons/vscode-icons/file-type-sql";
import iconSqlite from "@iconify-icons/vscode-icons/file-type-sqlite";
import iconSvelte from "@iconify-icons/vscode-icons/file-type-svelte";
import iconSwift from "@iconify-icons/vscode-icons/file-type-swift";
import iconText from "@iconify-icons/vscode-icons/file-type-text";
import iconToml from "@iconify-icons/vscode-icons/file-type-toml";
import iconTsConfig from "@iconify-icons/vscode-icons/file-type-tsconfig";
import iconTypeScript from "@iconify-icons/vscode-icons/file-type-typescript";
import iconTypeScriptDefinition from "@iconify-icons/vscode-icons/file-type-typescriptdef";
import iconVite from "@iconify-icons/vscode-icons/file-type-vite";
import iconVitest from "@iconify-icons/vscode-icons/file-type-vitest";
import iconVue from "@iconify-icons/vscode-icons/file-type-vue";
import iconXml from "@iconify-icons/vscode-icons/file-type-xml";
import iconYaml from "@iconify-icons/vscode-icons/file-type-yaml";
import iconYarn from "@iconify-icons/vscode-icons/file-type-yarn";
import iconZip from "@iconify-icons/vscode-icons/file-type-zip";
import { File } from "lucide-react";

type FileIcon = { name: string; data: IconifyIcon };

const extensionIcons: Record<string, FileIcon> = {
  bat: fileIcon("bat", iconBat),
  c: fileIcon("c", iconC),
  cc: fileIcon("cpp", iconCpp),
  cjs: fileIcon("javascript", iconJavaScript),
  conf: fileIcon("config", iconConfig),
  cpp: fileIcon("cpp", iconCpp),
  cs: fileIcon("csharp", iconCSharp),
  css: fileIcon("css", iconCss),
  cts: fileIcon("typescript", iconTypeScript),
  gif: fileIcon("image", iconImage),
  go: fileIcon("go", iconGo),
  gql: fileIcon("graphql", iconGraphql),
  graphql: fileIcon("graphql", iconGraphql),
  h: fileIcon("c", iconC),
  hpp: fileIcon("cpp", iconCpp),
  html: fileIcon("html", iconHtml),
  ico: fileIcon("image", iconImage),
  icns: fileIcon("image", iconImage),
  ini: fileIcon("ini", iconIni),
  java: fileIcon("java", iconJava),
  jpeg: fileIcon("image", iconImage),
  jpg: fileIcon("image", iconImage),
  js: fileIcon("javascript", iconJavaScript),
  json: fileIcon("json", iconJson),
  jsx: fileIcon("react-javascript", iconReactJavaScript),
  kt: fileIcon("kotlin", iconKotlin),
  kts: fileIcon("kotlin", iconKotlin),
  less: fileIcon("less", iconLess),
  lua: fileIcon("lua", iconLua),
  md: fileIcon("markdown", iconMarkdown),
  mdx: fileIcon("mdx", iconMdx),
  mjs: fileIcon("javascript", iconJavaScript),
  mts: fileIcon("typescript", iconTypeScript),
  pdf: fileIcon("pdf", iconPdf),
  php: fileIcon("php", iconPhp),
  png: fileIcon("image", iconImage),
  proto: fileIcon("protobuf", iconProtobuf),
  ps1: fileIcon("powershell", iconPowerShell),
  py: fileIcon("python", iconPython),
  rb: fileIcon("ruby", iconRuby),
  rs: fileIcon("rust", iconRust),
  sass: fileIcon("sass", iconSass),
  scss: fileIcon("scss", iconScss),
  sh: fileIcon("shell", iconShell),
  sql: fileIcon("sql", iconSql),
  sqlite: fileIcon("sqlite", iconSqlite),
  svelte: fileIcon("svelte", iconSvelte),
  svg: fileIcon("image", iconImage),
  swift: fileIcon("swift", iconSwift),
  toml: fileIcon("toml", iconToml),
  ts: fileIcon("typescript", iconTypeScript),
  tsx: fileIcon("react-typescript", iconReactTypeScript),
  txt: fileIcon("text", iconText),
  vue: fileIcon("vue", iconVue),
  webp: fileIcon("image", iconImage),
  xml: fileIcon("xml", iconXml),
  yaml: fileIcon("yaml", iconYaml),
  yml: fileIcon("yaml", iconYaml),
  zip: fileIcon("zip", iconZip),
};

export default function FileTypeIcon({ path }: { path: string }) {
  const icon = resolveFileIcon(path);
  if (!icon) {
    return <span className="file-type-icon default" aria-hidden="true"><File size={15} strokeWidth={1.7} /></span>;
  }
  return (
    <span className="file-type-icon vscode-icon" data-file-icon={icon.name} aria-hidden="true">
      <Icon icon={icon.data} width={18} height={18} />
    </span>
  );
}

function resolveFileIcon(path: string): FileIcon | null {
  const name = fileBasename(path).toLowerCase();
  const extension = name.split(".").at(-1) ?? "";

  if (name === ".env" || name.startsWith(".env.")) return fileIcon("dotenv", iconDotenv);
  if ([".gitignore", ".gitattributes", ".gitmodules", ".gitkeep"].includes(name)) return fileIcon("git", iconGit);
  if (["license", "license.md", "license.txt", "copying"].includes(name)) return fileIcon("license", iconLicense);
  if (["dockerfile", "containerfile"].includes(name) || name.startsWith("docker-compose.")) return fileIcon("docker", iconDocker);
  if (["makefile", "gnumakefile"].includes(name)) return fileIcon("makefile", iconMakefile);
  if (["go.mod", "go.sum", "go.work", "go.work.sum"].includes(name)) return fileIcon("go-package", iconGoPackage);
  if (["cargo.toml", "cargo.lock"].includes(name)) return fileIcon("cargo", iconCargo);
  if (["package.json", "package-lock.json", ".npmrc"].includes(name)) return fileIcon("npm", iconNpm);
  if (["pnpm-lock.yaml", "pnpm-workspace.yaml", ".pnpmfile.cjs"].includes(name)) return fileIcon("pnpm", iconPnpm);
  if (["yarn.lock", ".yarnrc", ".yarnrc.yml"].includes(name)) return fileIcon("yarn", iconYarn);
  if (["bun.lock", "bun.lockb", "bunfig.toml"].includes(name)) return fileIcon("bun", iconBun);
  if (name === "tsconfig.json" || name.startsWith("tsconfig.")) return fileIcon("tsconfig", iconTsConfig);
  if (name === "vite.config.ts" || name === "vite.config.js" || name === "vite.config.mjs") return fileIcon("vite", iconVite);
  if (name === "vitest.config.ts" || name === "vitest.config.js" || name === "vitest.config.mjs") return fileIcon("vitest", iconVitest);
  if (name.startsWith("prettier.config.") || name.startsWith(".prettierrc")) return fileIcon("prettier", iconPrettier);
  if (name.endsWith(".d.ts")) return fileIcon("typescript-definition", iconTypeScriptDefinition);

  return extensionIcons[extension] ?? null;
}

function fileIcon(name: string, data: IconifyIcon): FileIcon {
  return { name, data };
}

export function fileBasename(path: string) {
  return path.split(/[\\/]/).filter(Boolean).at(-1) || "workspace";
}

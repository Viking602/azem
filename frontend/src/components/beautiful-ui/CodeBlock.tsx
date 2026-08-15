import { isValidElement, useEffect, useRef, useState, type ReactNode } from "react";
import { Copy } from "lucide-react";

const LANGUAGE_LABELS: Record<string, string> = {
  bash: "Shell",
  c: "C",
  cc: "C++",
  cpp: "C++",
  cs: "C#",
  css: "CSS",
  cxx: "C++",
  dart: "Dart",
  diff: "Diff",
  dockerfile: "Dockerfile",
  go: "Go",
  graphql: "GraphQL",
  h: "C",
  html: "HTML",
  java: "Java",
  javascript: "JavaScript",
  js: "JavaScript",
  json: "JSON",
  jsx: "JSX",
  kt: "Kotlin",
  kotlin: "Kotlin",
  lua: "Lua",
  makefile: "Makefile",
  markdown: "Markdown",
  md: "Markdown",
  php: "PHP",
  proto: "Protobuf",
  py: "Python",
  python: "Python",
  r: "R",
  rb: "Ruby",
  rs: "Rust",
  ruby: "Ruby",
  rust: "Rust",
  sh: "Shell",
  shell: "Shell",
  sql: "SQL",
  svelte: "Svelte",
  swift: "Swift",
  text: "Text",
  toml: "TOML",
  ts: "TypeScript",
  tsx: "TSX",
  txt: "Text",
  typescript: "TypeScript",
  vue: "Vue",
  xml: "XML",
  yaml: "YAML",
  yml: "YAML",
  zsh: "Shell",
};

export type CodeFenceInfo = { filename: string; language: string };

export function normalizeFenceToken(value: string) {
  return value.trim().replace(/^(?:title|filename|file)=/iu, "");
}

export function looksLikeFilename(value: string) {
  const token = normalizeFenceToken(value);
  if (!token || /\s/u.test(token)) return false;
  if (/[\\/]/u.test(token)) return true;
  if (/^(?:Makefile|Dockerfile|Gemfile|Procfile|Justfile|CMakeLists\.txt)$/iu.test(token)) return true;
  if (token.startsWith(".") && /^\.[\w.-]+$/u.test(token)) return true;
  return /\.[A-Za-z0-9]{1,16}$/u.test(token);
}

export function languageLabel(token: string) {
  const normalized = normalizeFenceToken(token);
  if (!normalized) return "";
  return LANGUAGE_LABELS[normalized.toLowerCase()] ?? normalized;
}

export function parseCodeFenceInfo(lang: string, meta = ""): CodeFenceInfo {
  const tokens = [lang, ...meta.split(/\s+/u)].map((token) => token.trim()).filter(Boolean);
  const filenameToken = tokens.find(looksLikeFilename);
  const languageToken = tokens.find((token) => token !== filenameToken);
  const filename = filenameToken ? normalizeFenceToken(filenameToken) : "";
  const inferred = filename ? languageLabel(filename.split(".").pop() ?? "") : "";
  return {
    filename,
    language: languageToken ? languageLabel(languageToken) : inferred,
  };
}

export function codeTextFromChildren(children: ReactNode): string {
  if (children == null || children === false) return "";
  if (typeof children === "string" || typeof children === "number") return String(children);
  if (Array.isArray(children)) return children.map(codeTextFromChildren).join("");
  if (isValidElement<{ children?: ReactNode }>(children)) return codeTextFromChildren(children.props.children);
  return "";
}

export function countCodeLines(text: string) {
  if (!text) return 1;
  const body = text.endsWith("\n") ? text.slice(0, -1) : text;
  return Math.max(1, body.split("\n").length);
}

export function CodeBlock({
  filename = "",
  language = "",
  copyLabel,
  copiedLabel,
  children,
}: {
  filename?: string;
  language?: string;
  copyLabel: string;
  copiedLabel: string;
  children?: ReactNode;
}) {
  const [copied, setCopied] = useState(false);
  const copiedTimer = useRef(0);
  useEffect(() => () => window.clearTimeout(copiedTimer.current), []);

  const text = codeTextFromChildren(children);
  const lineCount = countCodeLines(text);
  const label = [filename, language].filter(Boolean).join(" · ") || copyLabel;

  const copy = async () => {
    if (!navigator.clipboard?.writeText) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      window.clearTimeout(copiedTimer.current);
      copiedTimer.current = window.setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  };

  return <figure className="bui-code-block" aria-label={label}>
    <header className="bui-code-header">
      {filename ? <strong className="bui-code-filename">{filename}</strong> : language ? <strong className="bui-code-filename">{language}</strong> : null}
      {filename && language ? <span className="bui-code-lang">{language}</span> : null}
      <button type="button" className="bui-code-copy" onClick={() => void copy()}>
        <Copy size={14} strokeWidth={2} aria-hidden="true" />
        <span aria-live="polite">{copied ? copiedLabel : copyLabel}</span>
      </button>
    </header>
    <div className="bui-code-body">
      <div className="bui-code-gutter" aria-hidden="true">
        {Array.from({ length: lineCount }, (_, index) => <span key={index + 1}>{index + 1}</span>)}
      </div>
      <pre><code>{children}</code></pre>
    </div>
  </figure>;
}

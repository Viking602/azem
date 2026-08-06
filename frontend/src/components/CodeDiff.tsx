import { Copy } from "lucide-react";
import { translator, type Language } from "../i18n";
import type { FileChange } from "./fileChanges";

type DiffKind = "added" | "deleted" | "context";
type DiffRow = { kind: DiffKind; lineNumber: number; marker: "+" | "−" | " "; code: string };
type SyntaxToken = { content: string; kind?: "comment" | "string" | "number" | "keyword" | "literal" | "function" | "punctuation" };

const CODE_TOKEN = /(\/\/.*$|\/\*.*?\*\/|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`|\b(?:0x[\da-f]+|\d+(?:\.\d+)?)\b|\b(?:as|async|await|break|case|catch|class|const|continue|def|defer|do|else|enum|export|extends|fallthrough|final|finally|for|from|func|function|go|if|implements|import|in|interface|let|map|new|package|private|protected|public|range|return|select|static|struct|switch|throw|try|type|var|while|yield)\b|\b(?:false|null|nil|none|true|undefined)\b|\b[A-Za-z_$][\w$]*(?=\s*\()|[{}()[\].,;:+\-*/=<>!?&|]+)/giu;
const CODE_KEYWORDS = new Set("as async await break case catch class const continue def defer do else enum export extends fallthrough final finally for from func function go if implements import in interface let map new package private protected public range return select static struct switch throw try type var while yield".split(" "));

export default function CodeDiff({ changes, language, insetFromProcessRail = false }: {
  changes: FileChange[];
  language: Language;
  insetFromProcessRail?: boolean;
}) {
  return <div className={`code-diff-stack${insetFromProcessRail ? " process-rail-inset" : ""}`}>
    {changes.map((change, index) => <DiffFile key={`${change.path}-${change.firstChangedLine}-${index}`} change={change} language={language} />)}
  </div>;
}

function DiffFile({ change, language }: { change: FileChange; language: Language }) {
  const t = translator(language);
  const rows = diffRows(change);
  const filename = change.path.replace(/\\/gu, "/").split("/").at(-1) || change.path;
  const copy = async () => {
    await navigator.clipboard?.writeText(change.diff);
  };
  return <section className="code-diff" aria-label={`${t("editedFileDiff")} · ${change.path}`}>
    <header>
      <strong title={change.path}>{filename}</strong>
      <span className="diff-count plus">+{change.additions}</span>
      <span className="diff-count minus">−{change.deletions}</span>
      <button type="button" onClick={copy} aria-label={t("copyDiff")} title={t("copyDiff")}>
        <Copy size={14} />
      </button>
    </header>
    <div className="code-diff-scroll" tabIndex={0} aria-label={`${filename} ${t("editedFileDiff")}`}>
      <table>
        <caption className="sr-only">{change.path} · +{change.additions} −{change.deletions}</caption>
        <tbody>{rows.map((row, index) => <tr key={`${index}-${row.kind}-${row.lineNumber}`} className={row.kind}>
          <td className="diff-line-number" aria-hidden="true">{row.lineNumber}</td>
          <td className="diff-marker" aria-hidden="true">{row.marker}</td>
          <td className="diff-code"><code>{syntaxTokens(row.code).map((token, tokenIndex) => token.kind
            ? <span key={`${tokenIndex}-${token.content}`} className={`syntax-${token.kind}`}>{token.content}</span>
            : token.content)}</code></td>
        </tr>)}</tbody>
      </table>
    </div>
  </section>;
}

function diffRows(change: FileChange): DiffRow[] {
  let oldLine = change.firstChangedLine;
  let newLine = change.firstChangedLine;
  const rows: DiffRow[] = [];
  for (const line of change.diff.split("\n")) {
    if (line.startsWith("+") && !line.startsWith("+++")) {
      rows.push({ kind: "added", lineNumber: newLine, marker: "+", code: line.slice(1) });
      newLine += 1;
    } else if (line.startsWith("-") && !line.startsWith("---")) {
      rows.push({ kind: "deleted", lineNumber: oldLine, marker: "−", code: line.slice(1) });
      oldLine += 1;
    } else {
      rows.push({ kind: "context", lineNumber: newLine, marker: " ", code: line.startsWith(" ") ? line.slice(1) : line });
      oldLine += 1;
      newLine += 1;
    }
  }
  return rows;
}

export function syntaxTokens(code: string): SyntaxToken[] {
  const tokens: SyntaxToken[] = [];
  let offset = 0;
  for (const match of code.matchAll(CODE_TOKEN)) {
    const index = match.index ?? 0;
    if (index > offset) tokens.push({ content: code.slice(offset, index) });
    const content = match[0];
    let kind: SyntaxToken["kind"] = "punctuation";
    if (content.startsWith("//") || content.startsWith("/*")) kind = "comment";
    else if (/^["'`]/u.test(content)) kind = "string";
    else if (/^(?:0x[\da-f]+|\d)/iu.test(content)) kind = "number";
    else if (/^(?:false|null|nil|none|true|undefined)$/iu.test(content)) kind = "literal";
    else if (/^[A-Za-z_$]/u.test(content)) kind = CODE_KEYWORDS.has(content.toLowerCase()) ? "keyword" : "function";
    tokens.push({ content, kind });
    offset = index + content.length;
  }
  if (offset < code.length) tokens.push({ content: code.slice(offset) });
  return tokens;
}

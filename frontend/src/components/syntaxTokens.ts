export type SyntaxToken = {
  content: string;
  kind?: "comment" | "string" | "number" | "keyword" | "literal" | "function" | "punctuation";
};

const CODE_TOKEN = /(\/\/.*$|\/\*.*?\*\/|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`|\b(?:0x[\da-f]+|\d+(?:\.\d+)?)\b|\b(?:as|async|await|break|case|catch|class|const|continue|def|defer|do|else|enum|export|extends|fallthrough|final|finally|for|from|func|function|go|if|implements|import|in|interface|let|map|new|package|private|protected|public|range|return|select|static|struct|switch|throw|try|type|var|while|yield)\b|\b(?:false|null|nil|none|true|undefined)\b|\b[A-Za-z_$][\w$]*(?=\s*\()|[{}()[\].,;:+\-*/=<>!?&|]+)/giu;

const CODE_KEYWORDS = new Set("as async await break case catch class const continue def defer do else enum export extends fallthrough final finally for from func function go if implements import in interface let map new package private protected public range return select static struct switch throw try type var while yield".split(" "));

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

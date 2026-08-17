/**
 * ChatGPT-style incomplete-markdown completion for a live stream.
 * Keep the tree on the block types it will have when remaining closers
 * arrive, so the last tokens do not reflow from raw markers into headings,
 * emphasis, links, or code cards.
 */
export function stabilizeStreamingMarkdown(source: string): string {
  if (!source) return source;
  const held = holdBackIncompleteBlockStarter(source);
  if (hasOpenFence(held)) {
    return held.endsWith("\n") ? `${held}\`\`\`` : `${held}\n\`\`\``;
  }
  return closeInlineMarks(closeIncompleteLink(held));
}

function holdBackIncompleteBlockStarter(text: string) {
  return text.replace(/(^|\n)(?:#{1,6}|[-*+]|\d+[.)])[ \t]*$/u, "$1");
}

function hasOpenFence(text: string) {
  let open = false;
  for (const line of text.split("\n")) {
    if (/^ {0,3}(?:`{3,}|~{3,})/u.test(line)) open = !open;
  }
  return open;
}

function closeIncompleteLink(text: string) {
  const href = text.lastIndexOf("](");
  if (href >= 0 && !text.slice(href).includes(")")) return `${text})`;
  const label = text.lastIndexOf("[");
  if (label >= 0 && !text.slice(label).includes("]")) return `${text}]`;
  return text;
}

function closeInlineMarks(text: string) {
  let next = text;
  if (unescapedCount(next, "**") % 2 === 1) next += "**";
  if (unescapedCount(next, "~~") % 2 === 1) next += "~~";
  if (unescapedCount(next, "__") % 2 === 1) next += "__";
  if (singleMarkCount(next, "`") % 2 === 1) next += "`";
  if (singleMarkCount(next, "*") % 2 === 1) next += "*";
  return next;
}

function unescapedCount(text: string, token: string) {
  let count = 0;
  for (let index = 0; index < text.length; index += 1) {
    if (text[index] === "\\") {
      index += 1;
      continue;
    }
    if (text.startsWith(token, index)) {
      count += 1;
      index += token.length - 1;
    }
  }
  return count;
}

function singleMarkCount(text: string, mark: string) {
  let count = 0;
  for (let index = 0; index < text.length; index += 1) {
    if (text[index] === "\\") {
      index += 1;
      continue;
    }
    if (text[index] !== mark) continue;
    if (text[index - 1] === mark || text[index + 1] === mark) continue;
    if (mark === "*" && (index === 0 || text[index - 1] === "\n") && text[index + 1] === " ") continue;
    count += 1;
  }
  return count;
}

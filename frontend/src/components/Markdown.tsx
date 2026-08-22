import { Children, cloneElement, isValidElement, memo, useMemo, useRef, type ComponentProps, type ReactElement, type ReactNode } from "react";
import ReactMarkdown, { type ExtraProps } from "react-markdown";
import remarkGfm from "remark-gfm";
import { CodeBlock, parseCodeFenceInfo } from "./beautiful-ui/CodeBlock";
import { syntaxTokens } from "./CodeDiff";
import { translator } from "../i18n";
import { useRuntimeStore } from "../store";

// Module-level stable plugin list: memoization below skips re-rendering when
// only children/components change, so the plugin array must never be recreated.
const GFM_PLUGINS: ComponentProps<typeof ReactMarkdown>["remarkPlugins"] = [remarkGfm, attachCodeFenceInfo];

export type StreamingRevealRange = { id: number; start: number; end: number; bornAt?: number };

export const STREAM_REVEAL_MS = 260;
export const MAX_LIVE_REVEAL_RANGES = 8;

export function sameRevealRanges(
  left: readonly StreamingRevealRange[],
  right: readonly StreamingRevealRange[],
) {
  return left.length === right.length && left.every((range, index) => {
    const other = right[index];
    return other !== undefined && range.id === other.id && range.start === other.start && range.end === other.end;
  });
}

/** Newest unsettled increments may still be sharpening. Expired and older siblings stay plain text. */
export function liveRevealRanges(
  ranges: readonly StreamingRevealRange[],
  now = Date.now(),
): StreamingRevealRange[] {
  if (!ranges.length) return [];
  return [...ranges]
    .sort((left, right) => left.id - right.id)
    .slice(-MAX_LIVE_REVEAL_RANGES)
    .filter((range) => now - (range.bornAt ?? now) < STREAM_REVEAL_MS)
    .sort((left, right) => left.start - right.start);
}

function revealResumeStyle(range: StreamingRevealRange, now: number): { animationDelay: string } | undefined {
  if (range.bornAt === undefined) return undefined;
  const elapsed = now - range.bornAt;
  if (elapsed <= 0) return undefined;
  return { animationDelay: `-${elapsed}ms` };
}

type MarkdownSyntaxNode = {
  type: string;
  value?: string;
  lang?: string | null;
  meta?: string | null;
  children?: MarkdownSyntaxNode[];
  position?: { start?: { offset?: number }; end?: { offset?: number } };
  data?: { hName?: string; hProperties?: Record<string, unknown> };
};

function wrapStreamingRanges(parent: MarkdownSyntaxNode, ranges: readonly StreamingRevealRange[], now: number) {
  if (!parent.children) return;
  parent.children = parent.children.flatMap((child) => {
    if (child.children) {
      wrapStreamingRanges(child, ranges, now);
      return [child];
    }
    const start = child.position?.start?.offset;
    const end = child.position?.end?.offset;
    if (child.type !== "text" || !child.value || start === undefined || end === undefined) return [child];
    const relevant = ranges.filter((range) => range.start < end && range.end > start);
    if (!relevant.length) return [child];

    const result: MarkdownSyntaxNode[] = [];
    let cursor = 0;
    for (const range of relevant) {
      const from = Math.max(cursor, Math.min(child.value.length, range.start - start));
      const to = Math.max(from, Math.min(child.value.length, range.end - start));
      if (from > cursor) result.push({ ...child, value: child.value.slice(cursor, from) });
      if (to > from) result.push({
        ...child,
        value: child.value.slice(from, to),
        data: {
          ...child.data,
          hName: "span",
          hProperties: {
            ...child.data?.hProperties,
            className: ["streaming-text-reveal"],
            "data-stream-reveal": String(range.id),
            style: revealResumeStyle(range, now),
          },
        },
      });
      cursor = to;
    }
    if (cursor < child.value.length) result.push({ ...child, value: child.value.slice(cursor) });
    return result.length ? result : [child];
  });
}

function streamingRevealPlugin(ranges: readonly StreamingRevealRange[]) {
  const now = Date.now();
  const live = liveRevealRanges(ranges, now);
  return () => (tree: MarkdownSyntaxNode) => wrapStreamingRanges(tree, live, now);
}


function visitMarkdown(node: MarkdownSyntaxNode, visit: (node: MarkdownSyntaxNode) => void) {
  visit(node);
  node.children?.forEach((child) => visitMarkdown(child, visit));
}

function attachCodeFenceInfo() {
  return (tree: MarkdownSyntaxNode) => {
    visitMarkdown(tree, (node) => {
      if (node.type !== "code") return;
      node.data = {
        ...node.data,
        hProperties: {
          ...node.data?.hProperties,
          "data-code-lang": node.lang ?? "",
          "data-code-meta": node.meta ?? "",
        },
      };
    });
  };
}

function renderSyntaxTokens(text: string, keyPrefix: string) {
  return syntaxTokens(text).map((token, index) => token.kind
    ? <span key={`${keyPrefix}-${index}-${token.kind}`} className={`syntax-${token.kind}`}>{token.content}</span>
    : token.content);
}

function highlightCodeChildren(children: ReactNode, keyPrefix = "code"): ReactNode {
  if (children == null || children === false) return children;
  if (typeof children === "string" || typeof children === "number") return renderSyntaxTokens(String(children), keyPrefix);
  if (Array.isArray(children)) {
    return children.map((child, index) => highlightCodeChildren(child, `${keyPrefix}-${index}`));
  }
  if (isValidElement<{ className?: string; children?: ReactNode; "data-stream-reveal"?: string }>(children)) {
    const reveal = children.props["data-stream-reveal"];
    const nextPrefix = reveal != null ? `reveal-${reveal}` : keyPrefix;
    if (children.props.children == null) return children;
    return cloneElement(children, { children: highlightCodeChildren(children.props.children, nextPrefix) });
  }
  return children;
}

function fenceClassLanguage(className?: string) {
  const match = className?.match(/language-([^\s]+)/u);
  return match?.[1] ?? "";
}

function extractFencedCode(children: ReactNode): { className: string; children: ReactNode } {
  const code = Children.toArray(children).find((child): child is ReactElement<{ className?: string; children?: ReactNode }> => (
    isValidElement<{ className?: string; children?: ReactNode }>(child) && child.type === "code"
  ));
  if (!code) return { className: "", children };
  return { className: code.props.className ?? "", children: code.props.children };
}

type MarkdownPreProps = ComponentProps<"pre"> & ExtraProps & {
  "data-code-lang"?: string;
  "data-code-meta"?: string;
};

function MarkdownPre({ children, ...props }: MarkdownPreProps) {
  const language = useRuntimeStore((state) => state.snapshot?.language === "en" ? "en" : "zh-CN");
  const t = translator(language);
  const fenced = extractFencedCode(children);
  const lang = String(props["data-code-lang"] || fenceClassLanguage(fenced.className));
  const meta = String(props["data-code-meta"] || "");
  const info = parseCodeFenceInfo(lang, meta);
  return <CodeBlock filename={info.filename} language={info.language} copyLabel={t("copyCode")} copiedLabel={t("copiedCode")}>
    {highlightCodeChildren(fenced.children)}
  </CodeBlock>;
}

const MARKDOWN_COMPONENTS: ComponentProps<typeof ReactMarkdown>["components"] = {
  pre: MarkdownPre,
};

function MarkdownBase(props: ComponentProps<typeof ReactMarkdown>) {
  const components = props.components
    ? { ...MARKDOWN_COMPONENTS, ...props.components }
    : MARKDOWN_COMPONENTS;
  return <ReactMarkdown remarkPlugins={GFM_PLUGINS} {...props} components={components} />;
}

/**
 * Memoized GFM markdown renderer. Re-parsing is skipped when the source text
 * and the components map are unchanged, so completed blocks (or PR descriptions
 * and comments) are not re-rendered on unrelated parent updates.
 */
export const Markdown = memo(MarkdownBase, (previous, next) =>
  previous.children === next.children
  && previous.components === next.components,
);

/**
 * Renders the current Markdown structure immediately while marking the
 * newest unsettled increments for a short visual reveal. Expired ranges stay
 * ordinary text in the same Markdown tree. In-flight spans resume via a
 * negative animation-delay so reparse cannot replay enter motion. Memoized
 * so a just-settled live tree is not remounted or re-parsed.
 */
function StreamingMarkdownView({ children, ranges }: { children: string; ranges: readonly StreamingRevealRange[] }) {
  const stableRanges = useRef(ranges);
  if (!sameRevealRanges(stableRanges.current, ranges)) stableRanges.current = ranges;
  const plugins = useMemo<ComponentProps<typeof ReactMarkdown>["remarkPlugins"]>(
    () => [remarkGfm, attachCodeFenceInfo, streamingRevealPlugin(stableRanges.current)],
    [stableRanges.current],
  );
  return <ReactMarkdown remarkPlugins={plugins} components={MARKDOWN_COMPONENTS}>{children}</ReactMarkdown>;
}

export const StreamingMarkdown = memo(StreamingMarkdownView, (previous, next) =>
  previous.children === next.children && sameRevealRanges(previous.ranges, next.ranges),
);

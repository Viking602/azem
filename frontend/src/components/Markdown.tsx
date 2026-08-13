import { memo, useMemo, type ComponentProps } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

// Module-level stable plugin list: memoization below skips re-rendering when
// only children/components change, so the plugin array must never be recreated.
const GFM_PLUGINS: ComponentProps<typeof ReactMarkdown>["remarkPlugins"] = [remarkGfm];

export type StreamingRevealRange = { id: number; start: number; end: number };

type MarkdownSyntaxNode = {
  type: string;
  value?: string;
  children?: MarkdownSyntaxNode[];
  position?: { start?: { offset?: number }; end?: { offset?: number } };
  data?: { hName?: string; hProperties?: Record<string, unknown> };
};

function wrapStreamingRanges(parent: MarkdownSyntaxNode, ranges: readonly StreamingRevealRange[]) {
  if (!parent.children) return;
  parent.children = parent.children.flatMap((child) => {
    if (child.children) {
      wrapStreamingRanges(child, ranges);
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
  return () => (tree: MarkdownSyntaxNode) => wrapStreamingRanges(tree, ranges);
}

function MarkdownBase(props: ComponentProps<typeof ReactMarkdown>) {
  return <ReactMarkdown remarkPlugins={GFM_PLUGINS} {...props} />;
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
 * Renders the current Markdown structure immediately while marking only the
 * latest provider ranges for a short visual reveal. Earlier ranges stay in the
 * same Markdown tree, so streaming never falls back to raw or pre-wrapped text.
 */
export function StreamingMarkdown({ children, ranges }: { children: string; ranges: readonly StreamingRevealRange[] }) {
  const plugins = useMemo<ComponentProps<typeof ReactMarkdown>["remarkPlugins"]>(
    () => [remarkGfm, streamingRevealPlugin(ranges)],
    [ranges],
  );
  return <ReactMarkdown remarkPlugins={plugins}>{children}</ReactMarkdown>;
}

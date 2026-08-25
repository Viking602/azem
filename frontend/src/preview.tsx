import { useEffect } from "react";
import { createRoot } from "react-dom/client";
import { TimelineFeed } from "./components/Timeline";
import type { Block } from "./types";
import "./styles.css";
import "./prototype.css";
import "./components/assistant-ui/elements.css";

function Preview() {
  useEffect(() => {
    document.querySelector<HTMLButtonElement>(".process-step .reasoning-summary")?.click();
  }, []);
  return <div style={{ padding: 28, maxWidth: 760 }}>
    <TimelineFeed blocks={settled} language="zh-CN" />
  </div>;
}

const settled: Block[] = [
  { id: "u", kind: "user", content: "分析当前改动", state: "completed" },
  {
    id: "c1", kind: "commentary", runId: "run", state: "completed",
    content: "当前改动很大，我先按主题核对终端、上下文压缩和 subagent 这三条主线。",
    textPhase: "commentary",
  },
  {
    id: "read", kind: "tool", runId: "run", title: "coding.read_file", state: "completed",
    content: JSON.stringify({ path: "CHANGELOG.md" }),
    data: { elapsedMs: "1800" },
  },
  {
    id: "list", kind: "tool", runId: "run", title: "coding.list_files", state: "completed",
    content: JSON.stringify({ path: "." }),
    data: { elapsedMs: "900" },
  },
  {
    id: "search", kind: "tool", runId: "run", title: "coding.search", state: "completed",
    content: JSON.stringify({ query: "idle_timeout" }),
    data: { elapsedMs: "2400" },
  },
  {
    id: "th", kind: "thinking", runId: "run", state: "completed", data: { elapsedMs: "18500" },
    content: "The user wants me to analyze the package structure, context rebuild evidence, and the frontend store.",
  },
  {
    id: "diff", kind: "diff", runId: "run", state: "completed", title: "frontend/src/thread.tsx",
    content: "@@ -1,3 +1,4 @@\n export function Composer() {\n-  const [draft, setDraft] = useState(\"\");\n+  const draft = useDraft(threadId);\n+  useEffect(() => hydrate(draft), [threadId]);\n }",
  },
  {
    id: "a", kind: "assistant", runId: "run", state: "completed",
    content: "```churn.ts\nexport async function churnBatch() {\n  const flavor = await getFlavor(\"pistachio\");\n  const base = await dairy.fetch({ flavor });\n  await freezer.store(base, { temp: \"-14C\" });\n  return base.gallons;\n}\n```",
  },
];

createRoot(document.getElementById("root")!).render(<Preview />);

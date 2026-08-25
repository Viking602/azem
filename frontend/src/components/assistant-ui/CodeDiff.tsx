import { translator, type Language } from "../../i18n";
import { CodeDiff as AssistantCodeDiff, type DiffLine } from "../elements/code-diff";
import type { FileChange } from "../fileChanges";

export function assistantDiffLines(diff: string): DiffLine[] {
  if (!diff) return [];
  const lines: DiffLine[] = [];
  for (const line of diff.split("\n")) {
    if (line.startsWith("+++") || line.startsWith("---")) continue;
    if (line.startsWith("+")) {
      lines.push({ kind: "added", text: line.slice(1) });
    } else if (line.startsWith("-")) {
      lines.push({ kind: "removed", text: line.slice(1) });
    } else {
      lines.push({ kind: "context", text: line.startsWith(" ") ? line.slice(1) : line });
    }
  }
  return lines;
}

export default function CodeDiff({ changes, language, insetFromProcessRail = false }: {
  changes: FileChange[];
  language: Language;
  insetFromProcessRail?: boolean;
}) {
  const t = translator(language);
  return <div className={`code-diff-stack${insetFromProcessRail ? " process-rail-inset" : ""}`} data-slot="code-diff-stack">
    {changes.map((change, index) => <AssistantCodeDiff
      key={`${change.path}-${change.firstChangedLine}-${index}`}
      filename={change.path}
      additions={change.additions}
      deletions={change.deletions}
      lines={assistantDiffLines(change.diff)}
      cycle={change.diff.length + change.firstChangedLine + index}
      className="azem-code-diff max-w-none"
      aria-label={`${t("editedFileDiff")} · ${change.path}`}
    />)}
  </div>;
}

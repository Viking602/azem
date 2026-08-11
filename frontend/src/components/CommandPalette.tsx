import { useEffect, useMemo, useRef, useState } from "react";
import { Command, FileCode2, FileDiff, Plus, Search, Sparkles } from "lucide-react";
import { execute } from "../bridge";
import { translator } from "../i18n";
import { useRuntimeStore } from "../store";

export default function CommandPalette() {
  const dialog = useRef<HTMLDialogElement>(null);
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const sessions = useRuntimeStore((state) => state.sessions);
  const setView = useRuntimeStore((state) => state.setView);
  const setSettingsOpen = useRuntimeStore((state) => state.setSettingsOpen);
  const setCommandOpen = useRuntimeStore((state) => state.setCommandOpen);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const t = translator(snapshot.language);
  const close = () => setCommandOpen(false);
  const primaryCommands = useMemo(() => [
    { id: "new", label: t("newSession"), hint: "⌘N", icon: Plus, run: () => execute({ kind: "new_session" }).then(() => setView("thread")) },
    { id: "files", label: snapshot.language === "zh-CN" ? "查看项目文件" : "View project files", hint: "⌘2", icon: FileCode2, run: () => Promise.resolve(setView("files")) },
    { id: "changes", label: snapshot.language === "zh-CN" ? "查看代码改动" : "Review code changes", hint: "⌘3", icon: FileDiff, run: () => Promise.resolve(setView("changes")) },
    { id: "motion", label: snapshot.language === "zh-CN" ? "打开动效规范" : "Open motion settings", hint: "⌘M", icon: Sparkles, run: () => setSettingsOpen(true) },
  ], [setSettingsOpen, setView, snapshot.language, t]);
  const sessionCommands = useMemo(() => sessions.map((session) => ({ id: session.id, label: session.title || t("newSession"), hint: session.modelId, icon: Command, run: () => execute({ kind: "resume_session", target: session.id }).then(() => setView("thread")) })), [sessions, setView, t]);
  const commands = query.trim() ? [...primaryCommands, ...sessionCommands] : primaryCommands;
  const filtered = commands.filter((command) => command.label.toLowerCase().includes(query.toLowerCase()));

  useEffect(() => { dialog.current?.showModal(); }, []);
  useEffect(() => setActive(0), [query]);
  const choose = (index: number) => {
    const item = filtered[index];
    if (!item) return;
    close();
    void item.run();
  };
  return <dialog ref={dialog} className="command-dialog" onCancel={(event) => { event.preventDefault(); close(); }} onClick={(event) => { if (event.target === dialog.current) close(); }}>
    <div className="command-frame" onKeyDown={(event) => {
      if (event.key === "ArrowDown") { event.preventDefault(); setActive((value) => Math.min(filtered.length - 1, value + 1)); }
      if (event.key === "ArrowUp") { event.preventDefault(); setActive((value) => Math.max(0, value - 1)); }
      if (event.key === "Enter") { event.preventDefault(); choose(active); }
    }}><header><Search size={17} /><input autoFocus value={query} onChange={(event) => setQuery(event.target.value)} placeholder={snapshot.language === "zh-CN" ? "搜索命令、任务或文件…" : "Search commands, tasks, or files…"} /><kbd>esc</kbd></header><div className="command-list"><small>{snapshot.language === "zh-CN" ? "建议" : "Suggested"}</small>{filtered.map((item, index) => <button key={item.id} className={index === active ? "active" : ""} onMouseEnter={() => setActive(index)} onClick={() => choose(index)}><item.icon size={15} /><span>{item.label}</span><kbd>{item.hint}</kbd></button>)}{!filtered.length && <p>没有匹配的命令。</p>}</div><footer><span>↵ {snapshot.language === "zh-CN" ? "选择" : "Select"}</span><span>↑↓ {snapshot.language === "zh-CN" ? "导航" : "Navigate"}</span><span>esc {snapshot.language === "zh-CN" ? "关闭" : "Close"}</span></footer></div>
  </dialog>;
}

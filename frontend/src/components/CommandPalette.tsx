import { useEffect, useMemo, useRef, useState } from "react";
import { Bot, Command, Plus, Search, Settings, X } from "lucide-react";
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
  const commands = useMemo(() => [
    { id: "new", label: t("newSession"), hint: "⌘N", icon: Plus, run: () => execute({ kind: "new_session" }).then(() => setView("thread")) },
    { id: "agents", label: t("agents"), hint: "", icon: Bot, run: () => setView("agents") },
    { id: "settings", label: t("settings"), hint: "⌘,", icon: Settings, run: () => setSettingsOpen(true) },
    ...sessions.map((session) => ({ id: session.id, label: session.title || t("newSession"), hint: session.modelId, icon: Command, run: () => execute({ kind: "resume_session", target: session.id }).then(() => setView("thread")) })),
  ], [sessions, setSettingsOpen, setView, t]);
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
    }}><header><Search size={17} /><input autoFocus value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("command")} /><button className="icon-button" onClick={close}><X size={15} /></button></header><div className="command-list">{filtered.map((item, index) => <button key={item.id} className={index === active ? "active" : ""} onMouseEnter={() => setActive(index)} onClick={() => choose(index)}><item.icon size={15} /><span>{item.label}</span><kbd>{item.hint}</kbd></button>)}{!filtered.length && <p>没有匹配的命令。</p>}</div><footer>{t("keyboardTip")}</footer></div>
  </dialog>;
}

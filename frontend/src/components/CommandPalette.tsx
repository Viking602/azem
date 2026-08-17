import { useEffect, useMemo, useRef, useState, type ComponentType } from "react";
import {
  Bot, FileCode2, FileDiff, MessageSquareText, Plus, Search, Settings2, Sparkles, SquareTerminal, UserRound,
} from "lucide-react";
import { execute, openProjectSession, resumeSession, searchSessions } from "../bridge";
import { translator } from "../i18n";
import { filterSettings, settingsSearchEntries } from "../settingsSearch";
import { useRuntimeStore } from "../store";
import { useTerminalStore } from "../terminalStore";
import type { SessionSearchResult } from "../types";

const SEARCH_DEBOUNCE_MS = 160;
const SEARCH_RESULT_LIMIT = 24;

type PaletteGroup = "commands" | "settings" | "sessions";

type PaletteItem = {
  id: string;
  group: PaletteGroup;
  label: string;
  description?: string;
  meta?: string;
  icon: ComponentType<{ size?: number }>;
  run: () => Promise<void> | void;
};

export default function CommandPalette() {
  const dialog = useRef<HTMLDialogElement>(null);
  const requestGeneration = useRef(0);
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const modelRoutes = useRuntimeStore((state) => state.modelRoutes);
  const modelProviders = useRuntimeStore((state) => state.modelProviders);
  const mcpServers = useRuntimeStore((state) => state.mcpServers);
  const skills = useRuntimeStore((state) => state.skills);
  const plugins = useRuntimeStore((state) => state.plugins);
  const setView = useRuntimeStore((state) => state.setView);
  const setSettingsOpen = useRuntimeStore((state) => state.setSettingsOpen);
  const setCommandOpen = useRuntimeStore((state) => state.setCommandOpen);
  const setSessionSearchTarget = useRuntimeStore((state) => state.setSessionSearchTarget);
  const applyEvents = useRuntimeStore((state) => state.applyEvents);
  const setError = useRuntimeStore((state) => state.setError);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const [sessionResults, setSessionResults] = useState<SessionSearchResult[]>([]);
  const [searching, setSearching] = useState(false);
  const [searchError, setSearchError] = useState("");
  const t = translator(snapshot.language);
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const close = () => setCommandOpen(false);

  const primaryCommands = useMemo<PaletteItem[]>(() => [
    { id: "new", group: "commands", label: t("newSession"), meta: "⌘N", icon: Plus, run: () => execute({ kind: "new_session" }).then(() => setView("thread")) },
    { id: "files", group: "commands", label: snapshot.language === "zh-CN" ? "查看项目文件" : "View project files", meta: "⌘2", icon: FileCode2, run: () => setView("files") },
    { id: "changes", group: "commands", label: snapshot.language === "zh-CN" ? "查看代码改动" : "Review code changes", meta: "⌘3", icon: FileDiff, run: () => setView("changes") },
    { id: "motion", group: "commands", label: snapshot.language === "zh-CN" ? "打开动效设置" : "Open motion settings", meta: "⌘,", icon: Sparkles, run: () => setSettingsOpen(true, { section: "appearance", id: "appearance:motion" }) },
    { id: "terminal", group: "commands", label: t("toggleTerminal"), meta: "⌘`", icon: SquareTerminal, run: () => useTerminalStore.getState().toggle() },
  ], [setSettingsOpen, setView, snapshot.language, t]);

  const commandItems = useMemo(() => {
    if (!normalizedQuery) return primaryCommands;
    return primaryCommands.filter((item) => `${item.label} ${item.description || ""}`.toLocaleLowerCase().includes(normalizedQuery));
  }, [normalizedQuery, primaryCommands]);
  const settingItems = useMemo<PaletteItem[]>(() => {
    if (!normalizedQuery) return [];
    return filterSettings(settingsSearchEntries(snapshot.language, modelRoutes, modelProviders, mcpServers, skills, plugins), query).slice(0, 12).map((entry) => ({
      id: `setting:${entry.id}`,
      group: "settings",
      label: entry.title,
      description: entry.description,
      meta: snapshot.language === "zh-CN" ? "设置" : "Settings",
      icon: Settings2,
      run: () => setSettingsOpen(true, { section: entry.section, id: entry.id }),
    }));
  }, [mcpServers, modelProviders, modelRoutes, normalizedQuery, plugins, query, setSettingsOpen, skills, snapshot.language]);
  const sessionItems = useMemo<PaletteItem[]>(() => sessionResults.map((result, index) => ({
    id: `session:${result.sessionId}:${result.kind}:${result.sequence || 0}:${index}`,
    group: "sessions",
    label: result.title || t("newSession"),
    description: result.preview || (snapshot.language === "zh-CN" ? "会话标题匹配" : "Conversation title match"),
    meta: sessionMeta(result, snapshot.workspace, snapshot.language),
    icon: result.kind === "user" ? UserRound : result.kind === "assistant" ? Bot : MessageSquareText,
    run: async () => {
      const sequence = result.kind === "title" ? undefined : result.sequence;
      if (result.workspace && result.workspace !== snapshot.workspace) {
        await openProjectSession(result.workspace, result.sessionId, sequence);
        return;
      }
      setSessionSearchTarget({ sessionId: result.sessionId, sequence });
      setView("thread");
      // Always reload the durable projection before focusing the match. The desktop
      // bootstrap can already identify the target session without having projected
      // its transcript yet, and a same-session shortcut would then leave the user on
      // an empty surface instead of navigating to the indexed message.
      const projection = await resumeSession(result.sessionId);
      if (projection) applyEvents([projection]);
    },
  })), [applyEvents, sessionResults, setSessionSearchTarget, setView, snapshot.language, snapshot.workspace, t]);

  const groups = useMemo(() => ([
    { id: "commands" as const, label: snapshot.language === "zh-CN" ? "操作" : "Actions", items: commandItems },
    { id: "settings" as const, label: snapshot.language === "zh-CN" ? "设置" : "Settings", items: settingItems },
    { id: "sessions" as const, label: snapshot.language === "zh-CN" ? "会话" : "Conversations", items: sessionItems },
  ]).filter((group) => group.items.length > 0), [commandItems, sessionItems, settingItems, snapshot.language]);
  const items = groups.flatMap((group) => group.items);

  useEffect(() => { dialog.current?.showModal(); }, []);
  useEffect(() => setActive(0), [query]);
  useEffect(() => setActive((value) => Math.max(0, Math.min(items.length - 1, value))), [items.length]);
  useEffect(() => {
    const value = query.trim();
    const generation = ++requestGeneration.current;
    if (!value) {
      setSessionResults([]);
      setSearching(false);
      setSearchError("");
      return;
    }
    setSearching(true);
    setSearchError("");
    let disposed = false;
    const timer = window.setTimeout(() => {
      void searchSessions(value, SEARCH_RESULT_LIMIT).then((results) => {
        if (disposed || generation !== requestGeneration.current) return;
        setSessionResults(results);
        setSearching(false);
      }).catch((cause) => {
        if (disposed || generation !== requestGeneration.current) return;
        setSessionResults([]);
        setSearching(false);
        setSearchError(cause instanceof Error ? cause.message : String(cause));
      });
    }, SEARCH_DEBOUNCE_MS);
    return () => {
      disposed = true;
      window.clearTimeout(timer);
    };
  }, [query]);

  const choose = (index: number) => {
    const item = items[index];
    if (!item) return;
    close();
    void Promise.resolve(item.run()).catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)));
  };

  let itemIndex = 0;
  return <dialog ref={dialog} className="command-dialog" aria-label={snapshot.language === "zh-CN" ? "全局搜索" : "Global search"} onCancel={(event) => { event.preventDefault(); close(); }} onClick={(event) => { if (event.target === dialog.current) close(); }}>
    <div className="command-frame" onKeyDown={(event) => {
      if (event.key === "ArrowDown") { event.preventDefault(); setActive((value) => Math.min(items.length - 1, value + 1)); }
      if (event.key === "ArrowUp") { event.preventDefault(); setActive((value) => Math.max(0, value - 1)); }
      if (event.key === "Enter") { event.preventDefault(); choose(active); }
    }}>
      <header><Search size={17} /><input autoFocus maxLength={200} value={query} onChange={(event) => setQuery(event.target.value)} placeholder={snapshot.language === "zh-CN" ? "搜索设置、会话标题或对话内容…" : "Search settings, conversation titles, or messages…"} /><kbd>esc</kbd></header>
      <div className="command-list" aria-busy={searching}>
        {groups.map((group) => <section className="command-group" key={group.id}><small>{group.label}</small>{group.items.map((item) => {
          const index = itemIndex++;
          return <button key={item.id} className={index === active ? "active" : ""} onMouseEnter={() => setActive(index)} onClick={() => choose(index)}>
            <item.icon size={15} />
            <span className="command-result-copy"><strong>{item.label}</strong>{item.description ? <small>{item.description}</small> : null}</span>
            {item.meta ? <kbd>{item.meta}</kbd> : null}
          </button>;
        })}</section>)}
        {searching && <p className="command-search-state">{snapshot.language === "zh-CN" ? "正在搜索会话…" : "Searching conversations…"}</p>}
        {searchError && <p className="command-search-error" role="alert">{searchError}</p>}
        {!items.length && !searching && !searchError && <p>{snapshot.language === "zh-CN" ? "没有匹配的设置或会话。" : "No matching settings or conversations."}</p>}
      </div>
      <footer><span>↵ {snapshot.language === "zh-CN" ? "打开" : "Open"}</span><span>↑↓ {snapshot.language === "zh-CN" ? "导航" : "Navigate"}</span><span>esc {snapshot.language === "zh-CN" ? "关闭" : "Close"}</span></footer>
    </div>
  </dialog>;
}

function sessionMeta(result: SessionSearchResult, currentWorkspace: string, language: "zh-CN" | "en") {
  const project = basename(result.workspace || currentWorkspace);
  const source = result.kind === "user"
    ? (language === "zh-CN" ? "用户消息" : "User message")
    : result.kind === "assistant"
      ? (language === "zh-CN" ? "回答" : "Answer")
      : (language === "zh-CN" ? "标题" : "Title");
  return project ? `${project} · ${source}` : source;
}

function basename(path: string) {
  const clean = path.replace(/[\\/]+$/u, "");
  return clean.split(/[\\/]/u).pop() || clean;
}

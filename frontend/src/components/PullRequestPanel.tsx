import { useEffect, useMemo, useRef, useState, type ComponentProps, type FormEvent, type MouseEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import {
  AlertCircle, Check, CheckCircle2, ChevronDown, ChevronRight, CircleDot, Clock3,
  ExternalLink, FileCode2, GitBranch, Github, GitCommitHorizontal, GitMerge, GitPullRequest,
  LoaderCircle, MessageCircle, Pencil, RefreshCw, RotateCcw, Send, ShieldCheck, UserRound,
  UserRoundPlus, X, XCircle,
} from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import GitHubAvatar from "./GitHubAvatar";
import { execute, openExternalURL } from "../bridge";
import { translator } from "../i18n";
import {
  refreshPullRequestDashboard, refreshPullRequestDetail, runPullRequestMutation, togglePullRequestMonitor,
} from "../pullRequests";
import { useRuntimeStore } from "../store";
import type {
  PullRequest, PullRequestActivity, PullRequestActor, PullRequestCheck, PullRequestMonitorState,
  PullRequestMutationRequest,
} from "../types";
type DialogState = "edit" | "reviewer" | "review" | null;
type Confirmation = { title: string; description: string; request: PullRequestMutationRequest };

export default function PullRequestPanel() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const number = useRuntimeStore((state) => state.selectedPullRequestNumber);
  const pullRequest = useRuntimeStore((state) => state.pullRequestDetail);
  const monitor = useRuntimeStore((state) => number ? state.pullRequestMonitors.get(number) : undefined);
  const loading = useRuntimeStore((state) => state.pullRequestLoading);
  const mutating = useRuntimeStore((state) => state.pullRequestMutating);
  const error = useRuntimeStore((state) => state.pullRequestError);
  const selectPullRequest = useRuntimeStore((state) => state.selectPullRequest);
  const setView = useRuntimeStore((state) => state.setView);
  const [dialog, setDialog] = useState<DialogState>(null);
  const [confirmation, setConfirmation] = useState<Confirmation | null>(null);
  const t = translator(snapshot.language);

  useEffect(() => {
    if (number && pullRequest?.number !== number) void refreshPullRequestDetail(number);
  }, [number, pullRequest?.number]);

  if (!number) return null;
  const close = () => selectPullRequest(null);
  const openRepairSession = async () => {
    if (!monitor?.sessionId) return;
    try {
      await execute({ kind: "resume_session", target: monitor.sessionId, sessionId: monitor.sessionId });
      setView("thread");
      close();
    } catch (cause) {
      useRuntimeStore.getState().setPullRequestError(cause instanceof Error ? cause.message : String(cause));
    }
  };

  return <aside className="pull-request-panel" aria-label={t("pullRequestDetails")}>
    <header className="pull-request-panel-header titlebar-region">
      <div className="pull-request-tab"><GitPullRequest size={15} /><strong>PR #{number}</strong></div>
      <button className="icon-button" type="button" disabled={loading} aria-label={t("refresh")} title={t("refresh")} onClick={() => void refreshPullRequestDetail(number)}><RefreshCw className={loading ? "spin" : ""} size={15} /></button>
      <button className="icon-button" type="button" aria-label={t("closePullRequestPanel")} title={t("closePullRequestPanel")} onClick={close}><X size={16} /></button>
    </header>

    {error && <div className="pull-request-panel-error" role="alert"><AlertCircle size={15} /><span>{error}</span></div>}
    {loading && !pullRequest && <div className="pull-request-panel-loading"><LoaderCircle className="spin" size={21} /><span>{t("loadingPullRequests")}</span></div>}
    {pullRequest?.number === number && <div className="pull-request-panel-scroll">
      <section className="pull-request-hero">
        <div className="pull-request-title-line"><h1>{pullRequest.title}</h1><button className="icon-button" type="button" aria-label={t("editPullRequest")} title={t("editPullRequest")} onClick={() => setDialog("edit")}><Pencil size={15} /></button><button className="icon-button" type="button" aria-label={t("openGitHub")} title={t("openGitHub")} onClick={() => void openExternalURL(pullRequest.url)}><Github size={17} /></button></div>
        <Metadata pullRequest={pullRequest} openReviewers={() => setDialog("reviewer")} confirm={setConfirmation} />
        <div className="pull-request-primary-actions">
          <button type="button" className={monitor?.enabled ? "monitor-button active" : "monitor-button"} disabled={mutating || pullRequest.state !== "OPEN"} onClick={() => void togglePullRequestMonitor(number, !monitor?.enabled)}>
            {monitor?.status === "repairing" ? <LoaderCircle className="spin" size={16} /> : <RotateCcw size={16} />}
            {monitor?.enabled ? t("stopMonitoring") : t("monitorAndFixPR")}
          </button>
          <MergeMenu pullRequest={pullRequest} disabled={mutating} confirm={setConfirmation} />
        </div>
        <MonitorBanner monitor={monitor} openSession={() => void openRepairSession()} />
      </section>

      <DescriptionSection pullRequest={pullRequest} edit={() => setDialog("edit")} />
      <ChecksSection checks={pullRequest.checksDetail} />
      <FilesSection pullRequest={pullRequest} />
      <ActivitySection activity={pullRequest.activity} />
      <CommentsSection pullRequest={pullRequest} review={() => setDialog("review")} />
    </div>}

    {dialog === "edit" && pullRequest && <EditDialog pullRequest={pullRequest} close={() => setDialog(null)} />}
    {dialog === "reviewer" && pullRequest && <ReviewerDialog pullRequest={pullRequest} close={() => setDialog(null)} />}
    {dialog === "review" && pullRequest && <ReviewDialog pullRequest={pullRequest} close={() => setDialog(null)} />}
    {confirmation && <ConfirmDialog confirmation={confirmation} close={() => setConfirmation(null)} />}
  </aside>;
}

function Metadata({ pullRequest, openReviewers, confirm }: { pullRequest: PullRequest; openReviewers: () => void; confirm: (value: Confirmation) => void }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const t = translator(language);
  const reviewerLabel = pullRequest.reviewRequests.length > 0
    ? pullRequest.reviewRequests.map((actor) => actor.login).join(", ")
    : t("requestReview");
  return <dl className="pull-request-metadata">
    <div><dt><GitBranch size={15} />{t("branch")}</dt><dd><span>{pullRequest.headRefName}</span><ChevronRight size={14} /><span>{pullRequest.baseRefName}</span><b>+{pullRequest.additions.toLocaleString()}</b><em>-{pullRequest.deletions.toLocaleString()}</em></dd></div>
    <div><dt><UserRound size={15} />{t("requestedReviewers")}</dt><dd><button type="button" onClick={openReviewers}>{reviewerLabel}<ChevronDown size={13} /></button></dd></div>
    <div><dt><MessageCircle size={15} />{t("comments")}</dt><dd>{pullRequest.comments.length || t("noComments")}</dd></div>
    <div><dt><Clock3 size={15} />{t("checks")}</dt><dd><CheckLabel pullRequest={pullRequest} /></dd></div>
    <div><dt><GitPullRequest size={15} />{t("status")}</dt><dd><StatusMenu pullRequest={pullRequest} confirm={confirm} /></dd></div>
  </dl>;
}

function CheckLabel({ pullRequest }: { pullRequest: PullRequest }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const t = translator(language);
  if (pullRequest.checks.total === 0) return <span className="quiet">{t("noChecks")}</span>;
  if (pullRequest.checks.failing > 0) return <span className="failing">{pullRequest.checks.failing} {t("checkFailing")}</span>;
  if (pullRequest.checks.pending > 0) return <span className="pending">{pullRequest.checks.pending} {t("checkPending")}</span>;
  return <span className="passing">{pullRequest.checks.passing} {t("checkPassing")}</span>;
}

function StatusMenu({ pullRequest, confirm }: { pullRequest: PullRequest; confirm: (value: Confirmation) => void }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const mutating = useRuntimeStore((state) => state.pullRequestMutating);
  const t = translator(language);
  const details = useRef<HTMLDetailsElement>(null);
  const run = async (request: PullRequestMutationRequest) => {
    if (details.current) details.current.open = false;
    await runPullRequestMutation(request);
  };
  if (pullRequest.state === "MERGED") return <span>{t("mergedStatus")}</span>;
  return <details className="pull-request-menu" ref={details}>
    <summary>{pullRequest.state === "CLOSED" ? t("closedStatus") : pullRequest.draft ? t("draftStatus") : t("readyForReview")}<ChevronDown size={13} /></summary>
    <div role="menu">
      {pullRequest.state === "CLOSED" ? <button type="button" role="menuitem" disabled={mutating} onClick={() => void run({ number: pullRequest.number, kind: "reopen" })}>{t("reopenPullRequest")}</button> : <>
        <button type="button" role="menuitem" disabled={mutating} onClick={() => void run({ number: pullRequest.number, kind: pullRequest.draft ? "ready" : "draft" })}>{pullRequest.draft ? t("readyForReview") : t("convertToDraft")}</button>
        <button type="button" role="menuitem" className="danger-text" disabled={mutating} onClick={() => { if (details.current) details.current.open = false; confirm({ title: t("closePullRequest"), description: `#${pullRequest.number} · ${pullRequest.title}`, request: { number: pullRequest.number, kind: "close" } }); }}>{t("closePullRequest")}</button>
      </>}
    </div>
  </details>;
}

function MergeMenu({ pullRequest, disabled, confirm }: { pullRequest: PullRequest; disabled: boolean; confirm: (value: Confirmation) => void }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const t = translator(language);
  const details = useRef<HTMLDetailsElement>(null);
  const canMerge = pullRequest.state === "OPEN" && !pullRequest.draft && Boolean(pullRequest.headRefOid);
  const choose = (kind: "merge" | "enable_auto_merge", method: "merge" | "squash" | "rebase") => {
    if (details.current) details.current.open = false;
    confirm({
      title: kind === "merge" ? t("confirmMerge") : t("enableAutoMerge"),
      description: `${mergeMethodLabel(method, language)} · ${pullRequest.headRefName} → ${pullRequest.baseRefName}`,
      request: { number: pullRequest.number, kind, mergeMethod: method, expectedHeadOid: pullRequest.headRefOid },
    });
  };
  if (pullRequest.state === "MERGED") return <button type="button" className="merge-button" disabled><GitMerge size={16} />{t("mergedStatus")}</button>;
  return <details className="merge-menu" ref={details}>
    <summary className="merge-button" aria-disabled={!canMerge || disabled}><GitMerge size={16} />{pullRequest.autoMergeEnabled ? t("disableAutoMerge") : t("merge")}<ChevronDown size={14} /></summary>
    <div role="menu">
      {pullRequest.autoMergeEnabled ? <button type="button" role="menuitem" disabled={disabled} onClick={() => { if (details.current) details.current.open = false; confirm({ title: t("disableAutoMerge"), description: pullRequest.title, request: { number: pullRequest.number, kind: "disable_auto_merge" } }); }}>{t("disableAutoMerge")}</button> : <>
        <span>{t("merge")}</span>
        {pullRequest.allowedMergeMethods.map((method) => <button type="button" role="menuitem" disabled={!canMerge || disabled} key={`merge-${method}`} onClick={() => choose("merge", method)}>{mergeMethodLabel(method, language)}</button>)}
        <span>{t("enableAutoMerge")}</span>
        {pullRequest.allowedMergeMethods.map((method) => <button type="button" role="menuitem" disabled={!canMerge || disabled} key={`auto-${method}`} onClick={() => choose("enable_auto_merge", method)}>{mergeMethodLabel(method, language)}</button>)}
      </>}
    </div>
  </details>;
}

function MonitorBanner({ monitor, openSession }: { monitor?: PullRequestMonitorState; openSession: () => void }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const t = translator(language);
  if (!monitor?.enabled) return null;
  const label = monitor.status === "watching" ? t("monitorWatching")
    : monitor.status === "pending" ? t("monitorPending")
      : monitor.status === "repairing" ? t("monitorRepairing")
        : monitor.status === "completed" ? t("monitorCompleted") : t("monitorError");
  return <div className={`pull-request-monitor-state ${monitor.status}`} role="status">
    {monitor.status === "repairing" ? <LoaderCircle className="spin" size={15} /> : monitor.status === "error" ? <AlertCircle size={15} /> : <ShieldCheck size={15} />}
    <span><strong>{label}</strong>{monitor.message && <small>{monitor.message}</small>}{monitor.conflict && <small>Merge conflict</small>}{monitor.failingChecks?.length ? <small>{monitor.failingChecks.join(", ")}</small> : null}</span>
    {monitor.sessionId && <button type="button" onClick={openSession}>{t("openRepairSession")}</button>}
  </div>;
}

function DescriptionSection({ pullRequest, edit }: { pullRequest: PullRequest; edit: () => void }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const t = translator(language);
  return <details className="pull-request-section-fold" open>
    <summary><span>{t("description")}</span><ChevronDown size={15} /></summary>
    <div className="pull-request-section-actions"><button className="icon-button" type="button" aria-label={t("editPullRequest")} onClick={edit}><Pencil size={14} /></button></div>
    <div className="pull-request-markdown">
      {pullRequest.body ? <ReactMarkdown remarkPlugins={[remarkGfm]} components={{ a: (props) => <SafeMarkdownAnchor {...props} baseURL={pullRequest.url} />, img: InertMarkdownImage }}>{pullRequest.body}</ReactMarkdown> : <p>—</p>}
    </div>
  </details>;
}

type MarkdownAnchorProps = ComponentProps<"a"> & { baseURL: string };
function SafeMarkdownAnchor({ baseURL, href, children, title }: MarkdownAnchorProps) {
  const destination = resolveExternalURL(href, baseURL);
  const handleClick = (event: MouseEvent<HTMLAnchorElement>) => {
    event.preventDefault();
    if (destination) void openExternalURL(destination);
  };
  return <a href={destination ?? undefined} title={title} aria-disabled={destination ? undefined : true} onClick={handleClick} onAuxClick={(event) => event.preventDefault()}>{children}</a>;
}

function InertMarkdownImage({ alt, title }: ComponentProps<"img">) {
  const label = alt?.trim() || title?.trim() || "Image";
  return <span className="pull-request-markdown-image-omitted" role="img" aria-label={label}>[{label}]</span>;
}

function resolveExternalURL(href: string | undefined, baseURL: string): string | null {
  if (!href) return null;
  try { decodeURI(href); } catch { return null; }
  try {
    const url = new URL(href, baseURL);
    return url.protocol === "http:" || url.protocol === "https:" ? url.toString() : null;
  } catch {
    return null;
  }
}

function ChecksSection({ checks }: { checks: PullRequestCheck[] }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const t = translator(language);
  return <details className="pull-request-section-fold" open>
    <summary><span>{t("checks")}</span><ChevronDown size={15} /></summary>
    <div className="pull-request-check-list">
      {checks.length === 0 ? <div className="pull-request-section-empty"><Clock3 size={16} />{t("noChecks")}</div> : checks.map((check) => <CheckRow key={`${check.workflow}-${check.name}`} check={check} />)}
    </div>
  </details>;
}

function CheckRow({ check }: { check: PullRequestCheck }) {
  const Icon = check.category === "passing" ? CheckCircle2 : check.category === "failing" ? XCircle : Clock3;
  return <button type="button" className={`pull-request-check ${check.category}`} disabled={!check.url} onClick={() => check.url && void openExternalURL(check.url)}>
    <Icon size={15} /><span><strong>{check.name}</strong>{check.workflow && <small>{check.workflow}</small>}</span><em>{check.conclusion || check.status}</em>{check.url && <ExternalLink size={13} />}
  </button>;
}

function FilesSection({ pullRequest }: { pullRequest: PullRequest }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const t = translator(language);
  return <details className="pull-request-section-fold">
    <summary><span>{pullRequest.changedFiles} {t("changedFiles")}</span><ChevronDown size={15} /></summary>
    <div className="pull-request-file-list">{pullRequest.files.map((file) => <div key={file.path}><FileCode2 size={14} /><span>{file.path}</span><b>+{file.additions}</b><em>-{file.deletions}</em></div>)}</div>
  </details>;
}

function ActivitySection({ activity }: { activity: PullRequestActivity[] }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const t = translator(language);
  return <details className="pull-request-section-fold" open>
    <summary><span>{t("activityLog")} · {activity.length}</span><ChevronDown size={15} /></summary>
    <div className="pull-request-activity-list">{activity.map((item, index) => <ActivityRow key={`${item.kind}-${item.at}-${item.oid || index}`} item={item} />)}</div>
  </details>;
}

function ActivityRow({ item }: { item: PullRequestActivity }) {
  const Icon = item.kind === "commit" ? GitCommitHorizontal : item.kind === "review" ? UserRound : item.kind === "comment" ? MessageCircle : item.kind === "merged" ? GitMerge : GitPullRequest;
  return <article className="pull-request-activity-row"><Icon size={14} /><Actor actor={item.actor} /><div><strong>{item.title}</strong>{item.state && <small>{item.state.replaceAll("_", " ")}</small>}{item.body && item.kind !== "comment" && <p>{item.body}</p>}</div><time dateTime={item.at}>{dateLabel(item.at)}</time></article>;
}

function CommentsSection({ pullRequest, review }: { pullRequest: PullRequest; review: () => void }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const mutating = useRuntimeStore((state) => state.pullRequestMutating);
  const t = translator(language);
  const [body, setBody] = useState("");
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!body.trim()) return;
    if (await runPullRequestMutation({ number: pullRequest.number, kind: "comment", body })) setBody("");
  };
  return <section className="pull-request-comments" aria-labelledby="pull-request-comments-heading">
    <header><h2 id="pull-request-comments-heading">{t("comments")} · {pullRequest.comments.length}</h2><button type="button" onClick={review}><ShieldCheck size={14} />{t("review")}</button></header>
    <div className="pull-request-comment-list">{pullRequest.comments.length === 0 ? <div className="pull-request-section-empty"><MessageCircle size={16} />{t("noComments")}</div> : pullRequest.comments.map((comment) => <article key={comment.id || `${comment.author.login}-${comment.createdAt}`}><header><Actor actor={comment.author} /><time dateTime={comment.createdAt}>{dateLabel(comment.createdAt)}</time></header><div className="pull-request-markdown compact"><ReactMarkdown remarkPlugins={[remarkGfm]} components={{ a: (props) => <SafeMarkdownAnchor {...props} baseURL={pullRequest.url} />, img: InertMarkdownImage }}>{comment.body}</ReactMarkdown></div></article>)}</div>
    {pullRequest.state === "OPEN" && <form className="pull-request-comment-composer" onSubmit={(event) => void submit(event)}><label className="sr-only" htmlFor="pull-request-comment">{t("commentPlaceholder")}</label><textarea id="pull-request-comment" value={body} onChange={(event) => setBody(event.target.value)} placeholder={t("commentPlaceholder")} /><button type="submit" aria-label={t("postComment")} title={t("postComment")} disabled={mutating || !body.trim()}><Send size={15} /></button></form>}
  </section>;
}

function Actor({ actor }: { actor: PullRequestActor }) {
  const label = actor.name || actor.login || "GitHub";
  return <span className="pull-request-actor"><GitHubAvatar actor={actor} /><b>{actor.login || label}</b></span>;
}

function EditDialog({ pullRequest, close }: { pullRequest: PullRequest; close: () => void }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const mutating = useRuntimeStore((state) => state.pullRequestMutating);
  const t = translator(language);
  const [title, setTitle] = useState(pullRequest.title);
  const [body, setBody] = useState(pullRequest.body);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (await runPullRequestMutation({ number: pullRequest.number, kind: "edit", title, body })) close();
  };
  return <Modal title={t("editPullRequest")} close={close}><form className="pull-request-form" onSubmit={(event) => void submit(event)}><label>{t("prTitle")}<input autoFocus value={title} onChange={(event) => setTitle(event.target.value)} required /></label><label>{t("prBody")}<textarea rows={13} value={body} onChange={(event) => setBody(event.target.value)} /></label><footer><button type="button" onClick={close}>{t("cancel")}</button><button className="primary" type="submit" disabled={mutating || !title.trim()}>{t("save")}</button></footer></form></Modal>;
}

function ReviewerDialog({ pullRequest, close }: { pullRequest: PullRequest; close: () => void }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const mutating = useRuntimeStore((state) => state.pullRequestMutating);
  const t = translator(language);
  const [login, setLogin] = useState("");
  const add = async (event: FormEvent) => {
    event.preventDefault();
    if (await runPullRequestMutation({ number: pullRequest.number, kind: "add_reviewer", login })) setLogin("");
  };
  return <Modal title={t("requestReview")} close={close}><div className="pull-request-reviewers-dialog">
    <form onSubmit={(event) => void add(event)}><label htmlFor="pull-request-reviewer">{t("reviewerPlaceholder")}</label><div><input id="pull-request-reviewer" autoFocus value={login} onChange={(event) => setLogin(event.target.value)} placeholder="octocat" /><button className="primary" type="submit" disabled={mutating || !login.trim()}><UserRoundPlus size={14} />{t("requestReview")}</button></div></form>
    <ul>{pullRequest.reviewRequests.map((actor) => <li key={actor.login}><Actor actor={actor} /><button type="button" disabled={mutating} aria-label={`${t("removeReviewer")} ${actor.login}`} onClick={() => void runPullRequestMutation({ number: pullRequest.number, kind: "remove_reviewer", login: actor.login })}><X size={14} /></button></li>)}</ul>
    <footer><button type="button" onClick={close}>{t("cancel")}</button></footer>
  </div></Modal>;
}

function ReviewDialog({ pullRequest, close }: { pullRequest: PullRequest; close: () => void }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const mutating = useRuntimeStore((state) => state.pullRequestMutating);
  const t = translator(language);
  const [kind, setKind] = useState<"approve" | "comment" | "request_changes">("comment");
  const [body, setBody] = useState("");
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (await runPullRequestMutation({
      number: pullRequest.number,
      kind: "review",
      reviewKind: kind,
      body,
      expectedHeadOid: pullRequest.headRefOid,
    })) close();
  };
  return <Modal title={t("review")} close={close}><form className="pull-request-form" onSubmit={(event) => void submit(event)}>
    <fieldset className="pull-request-review-kinds"><legend>{t("review")}</legend>{(["comment", "approve", "request_changes"] as const).map((value) => <label key={value}><input type="radio" name="review-kind" checked={kind === value} onChange={() => setKind(value)} /><span>{value === "approve" ? t("approvePR") : value === "request_changes" ? t("requestChanges") : t("reviewComment")}</span></label>)}</fieldset>
    <label>{t("reviewBodyPlaceholder")}<textarea autoFocus rows={7} value={body} onChange={(event) => setBody(event.target.value)} /></label>
    <footer><button type="button" onClick={close}>{t("cancel")}</button><button className="primary" type="submit" disabled={mutating || (kind !== "approve" && !body.trim())}>{t("review")}</button></footer>
  </form></Modal>;
}

function ConfirmDialog({ confirmation, close }: { confirmation: Confirmation; close: () => void }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const mutating = useRuntimeStore((state) => state.pullRequestMutating);
  const t = translator(language);
  const confirm = async () => {
    if (await runPullRequestMutation(confirmation.request)) close();
  };
  const mergeAction = confirmation.request.kind === "merge";
  return <Modal title={confirmation.title} close={close}><div className="pull-request-confirm"><span>{mergeAction ? <GitMerge size={21} /> : <AlertCircle size={21} />}</span><p>{confirmation.description}</p>{mergeAction && <small>{t("mergeWarning")}</small>}<footer><button type="button" onClick={close}>{t("cancel")}</button><button className="primary" type="button" disabled={mutating} onClick={() => void confirm()}>{confirmation.title}</button></footer></div></Modal>;
}

function Modal({ title, close, children }: { title: string; close: () => void; children: ReactNode }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const t = translator(language);
  const panel = useRef<HTMLDivElement>(null);
  const titleID = useMemo(() => `pull-request-dialog-${crypto.randomUUID()}`, []);
  const priorFocus = useRef<HTMLElement | null>(document.activeElement instanceof HTMLElement ? document.activeElement : null);
  useEffect(() => {
    const first = panel.current?.querySelector<HTMLElement>("[autofocus]")
      ?? panel.current?.querySelector<HTMLElement>("input, textarea")
      ?? panel.current?.querySelector<HTMLElement>("button, [tabindex]:not([tabindex='-1'])");
    first?.focus();
    return () => priorFocus.current?.focus();
  }, []);
  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "Escape") {
      event.stopPropagation();
      event.preventDefault();
      close();
      return;
    }
    if (event.key !== "Tab" || !panel.current) return;
    const focusable = Array.from(panel.current.querySelectorAll<HTMLElement>("button:not(:disabled), input:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex='-1'])"));
    if (focusable.length === 0) return;
    const first = focusable[0];
    const last = focusable.at(-1)!;
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  };
  return createPortal(<div className="pull-request-dialog-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) close(); }}>
    <div ref={panel} className="pull-request-dialog" role="dialog" aria-modal="true" aria-labelledby={titleID} onKeyDown={onKeyDown}>
      <header><h2 id={titleID}>{title}</h2><button className="icon-button" type="button" aria-label={t("closeDialog")} onClick={close}><X size={16} /></button></header>{children}
    </div>
  </div>, document.body);
}

function mergeMethodLabel(method: "merge" | "squash" | "rebase", language: "en" | "zh-CN") {
  const t = translator(language);
  return method === "squash" ? t("squashMerge") : method === "rebase" ? t("rebaseMerge") : t("mergeCommit");
}

function dateLabel(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return "";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date);
}

import { useEffect } from "react";
import {
  AlertCircle, CheckCircle2, Clock3, GitBranch, Github, GitPullRequest, RefreshCw, XCircle,
} from "lucide-react";
import { openExternalURL } from "../bridge";
import { translator, type MessageKey } from "../i18n";
import { openPullRequest, refreshPullRequestDashboard } from "../pullRequests";
import { useRuntimeStore } from "../store";
import type { PullRequestSummary } from "../types";

export default function PullRequestsPage() {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const dashboard = useRuntimeStore((state) => state.pullRequestDashboard);
  const loading = useRuntimeStore((state) => state.pullRequestLoading);
  const error = useRuntimeStore((state) => state.pullRequestError);
  const t = translator(snapshot.language);

  useEffect(() => {
    if (!dashboard && !loading) void refreshPullRequestDashboard();
  }, [dashboard, loading]);

  const refreshButton = (
    <button className="subtle-button" type="button" disabled={loading} onClick={() => void refreshPullRequestDashboard()}>
      <RefreshCw className={loading ? "spin" : ""} size={14} />{t("refresh")}
    </button>
  );

  return <section className="secondary-page pull-requests-page" aria-labelledby="pull-requests-heading">
    <header>
      <div><span>GitHub</span><h1 id="pull-requests-heading">{t("pullRequests")}</h1><p>{dashboard?.repository?.nameWithOwner ?? snapshot.workspace}</p></div>
      {refreshButton}
    </header>
    <div className="page-content pull-request-page-content" aria-busy={loading}>
      {error && <div className="pull-request-inline-error" role="alert"><AlertCircle size={15} /><span>{error}</span></div>}
      {dashboard && !dashboard.capability.available ? <Unavailable code={dashboard.capability.code} detail={dashboard.capability.message} retry={refreshButton} /> : null}
      {dashboard?.capability.available ? <>
        <PullRequestSection title={t("currentPullRequest")} hint={dashboard.currentBranch} items={dashboard.current ? [dashboard.current] : []} empty={t("noCurrentPullRequest")} />
        {dashboard.needsReview.length > 0 && <PullRequestSection title={t("needsMyReview")} items={dashboard.needsReview} />}
        {dashboard.createdByViewer.length > 0 && <PullRequestSection title={t("createdByMe")} items={dashboard.createdByViewer} />}
        <PullRequestSection title={t("allOpenPullRequests")} items={dashboard.open} empty={t("noPullRequests")} />
      </> : null}
      {!dashboard && loading && <div className="pull-request-loading"><RefreshCw className="spin" size={20} /><span>{t("loadingPullRequests")}</span></div>}
    </div>
  </section>;
}

function PullRequestSection({ title, hint, items, empty }: { title: string; hint?: string; items: PullRequestSummary[]; empty?: string }) {
  const sectionID = `pr-section-${title.replace(/\s+/g, "-").toLowerCase()}`;
  return <section className="pull-request-section" aria-labelledby={sectionID}>
    <header><h2 id={sectionID}>{title}</h2>{hint && <span><GitBranch size={12} />{hint}</span>}</header>
    {items.length > 0 ? <div className="pull-request-list">{items.map((pullRequest) => <PullRequestRow key={pullRequest.number} pullRequest={pullRequest} />)}</div> : empty ? <div className="pull-request-empty-row"><GitPullRequest size={15} />{empty}</div> : null}
  </section>;
}

function PullRequestRow({ pullRequest }: { pullRequest: PullRequestSummary }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const t = translator(language);
  const status = pullRequest.state === "MERGED" ? t("mergedStatus") : pullRequest.state === "CLOSED" ? t("closedStatus") : pullRequest.draft ? t("draftStatus") : t("openStatus");
  return <article className="pull-request-row">
    <button type="button" className="pull-request-row-main" onClick={() => void openPullRequest(pullRequest.number)}>
      <span className={`pull-request-state-dot ${pullRequest.state.toLowerCase()} ${pullRequest.draft ? "draft" : ""}`} aria-hidden="true" />
      <span className="pull-request-row-copy"><strong>{pullRequest.title}</strong><small>#{pullRequest.number} · {pullRequest.author.login} · {status}</small></span>
      <span className="pull-request-row-branch"><GitBranch size={13} />{pullRequest.headRefName}<i>→</i>{pullRequest.baseRefName}</span>
      <CheckSummary pullRequest={pullRequest} />
      <span className="pull-request-diff"><b>+{pullRequest.additions.toLocaleString()}</b><em>-{pullRequest.deletions.toLocaleString()}</em></span>
    </button>
    <button type="button" className="icon-button pull-request-open-github" aria-label={t("openGitHub")} title={t("openGitHub")} onClick={() => void openExternalURL(pullRequest.url)}><Github size={15} /></button>
  </article>;
}

function CheckSummary({ pullRequest }: { pullRequest: PullRequestSummary }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const t = translator(language);
  if (pullRequest.checks.total === 0) return <span className="pull-request-check-summary quiet"><Clock3 size={13} />{t("noChecks")}</span>;
  if (pullRequest.checks.failing > 0) return <span className="pull-request-check-summary failing"><XCircle size={13} />{pullRequest.checks.failing} {t("checkFailing")}</span>;
  if (pullRequest.checks.pending > 0) return <span className="pull-request-check-summary pending"><Clock3 size={13} />{pullRequest.checks.pending} {t("checkPending")}</span>;
  return <span className="pull-request-check-summary passing"><CheckCircle2 size={13} />{pullRequest.checks.passing} {t("checkPassing")}</span>;
}

function Unavailable({ code, detail, retry }: { code?: string; detail?: string; retry: React.ReactNode }) {
  const language = useRuntimeStore((state) => state.snapshot?.language ?? "zh-CN");
  const t = translator(language);
  const key: MessageKey = code === "not_installed" ? "prNotInstalled"
    : code === "unauthenticated" ? "prUnauthenticated"
      : code === "no_repository" ? "prNoRepository"
        : code === "offline" ? "prOffline" : "prUnavailable";
  return <div className="pull-request-unavailable">
    <span><Github size={26} /></span><h2>{t("prUnavailable")}</h2><p>{t(key)}</p>{detail && <code>{detail}</code>}{retry}
  </div>;
}

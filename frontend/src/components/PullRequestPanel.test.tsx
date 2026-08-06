import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { getPullRequestDashboard, mutatePullRequest, openExternalURL } from "../bridge";
import { useRuntimeStore } from "../store";
import type { PullRequest, PullRequestActor, PullRequestDashboard, Snapshot } from "../types";
import PullRequestPanel from "./PullRequestPanel";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock("../bridge", () => ({
  execute: vi.fn(() => Promise.resolve()),
  openExternalURL: vi.fn(() => Promise.resolve()),
  getPullRequestDashboard: vi.fn(() => Promise.resolve()),
  getPullRequestDetail: vi.fn(() => Promise.resolve()),
  mutatePullRequest: vi.fn(() => Promise.resolve()),
  setPullRequestMonitor: vi.fn(() => Promise.resolve()),
}));

const author: PullRequestActor = { login: "reviewer", name: "Reviewer" };
const snapshot = { language: "en" } as Snapshot;
const basePullRequest: PullRequest = {
  number: 42,
  title: "Markdown links",
  state: "OPEN",
  draft: false,
  author,
  headRefName: "feature/links",
  headRefOid: "abcdef1234567890",
  baseRefName: "main",
  additions: 1,
  deletions: 1,
  changedFiles: 1,
  url: "https://github.com/example/azem/pull/42",
  createdAt: "2026-08-01T00:00:00Z",
  updatedAt: "2026-08-01T00:00:00Z",
  body: "[body](https://example.com/body)",
  maintainerCanModify: false,
  autoMergeEnabled: false,
  reviewRequests: [],
  reviews: [],
  comments: [{
    id: "comment-1",
    author,
    body: "[comment](/docs)",
    createdAt: "2026-08-01T00:00:00Z",
  }],
  commits: [],
  files: [],
  checks: { total: 0, pending: 0, passing: 0, failing: 0, neutral: 0, skipped: 0 },
  checksDetail: [],
  activity: [],
  allowedMergeMethods: ["merge"],
};

async function renderPanel(pullRequest: PullRequest): Promise<{ container: HTMLDivElement; root: Root }> {
  const dashboard: PullRequestDashboard = {
    capability: { available: true },
    repository: {
      nameWithOwner: "example/azem",
      url: "https://github.com/example/azem",
      defaultBranch: "main",
      viewerPermission: "WRITE",
      viewerLogin: "reviewer",
      allowedMergeMethods: ["merge"],
    },
    createdByViewer: [],
    needsReview: [],
    open: [],
    refreshedAt: "2026-08-01T00:00:00Z",
  };
  vi.mocked(mutatePullRequest).mockResolvedValue({
    pullRequest,
    monitor: { number: pullRequest.number, enabled: false, status: "disabled" },
  });
  vi.mocked(getPullRequestDashboard).mockResolvedValue(dashboard);
  useRuntimeStore.setState({
    snapshot,
    pullRequestDashboard: dashboard,
    selectedPullRequestNumber: pullRequest.number,
    pullRequestDetail: pullRequest,
    pullRequestMonitors: new Map(),
    pullRequestLoading: false,
    pullRequestMutating: false,
    pullRequestError: "",
  });
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () => root.render(createElement(PullRequestPanel)));
  return { container, root };
}

function dispatchClick(link: HTMLAnchorElement): boolean {
  return link.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
}

afterEach(() => {
  vi.clearAllMocks();
  useRuntimeStore.setState({
    snapshot: null,
    pullRequestDashboard: null,
    selectedPullRequestNumber: null,
    pullRequestDetail: null,
    pullRequestMonitors: new Map(),
    pullRequestLoading: false,
    pullRequestMutating: false,
    pullRequestError: "",
  });
});

describe("PullRequestPanel Markdown links", () => {
  it("opens body and relative comment links externally without WebView navigation", async () => {
    const { container, root } = await renderPanel(basePullRequest);

    const bodyLink = Array.from(container.querySelectorAll<HTMLAnchorElement>(".pull-request-markdown a"))
      .find((link) => link.textContent === "body")!;
    const commentLink = Array.from(container.querySelectorAll<HTMLAnchorElement>(".pull-request-markdown a"))
      .find((link) => link.textContent === "comment")!;

    expect(bodyLink.getAttribute("href")).toBe("https://example.com/body");
    expect(commentLink.getAttribute("href")).toBe("https://github.com/docs");
    let bodyNavigation = true;
    let commentNavigation = true;
    await act(async () => {
      bodyNavigation = dispatchClick(bodyLink);
      commentNavigation = dispatchClick(commentLink);
    });

    expect(bodyNavigation).toBe(false);
    expect(commentNavigation).toBe(false);
    expect(openExternalURL).toHaveBeenNthCalledWith(1, "https://example.com/body");
    expect(openExternalURL).toHaveBeenNthCalledWith(2, "https://github.com/docs");

    await act(async () => root.unmount());
    container.remove();
  });

  it("does not open unsafe or malformed comment links", async () => {
    const pullRequest: PullRequest = {
      ...basePullRequest,
      comments: [{
        ...basePullRequest.comments[0]!,
        body: "[javascript](javascript:alert(1)) [data](data:text/html,blocked) [malformed](%zz)",
      }],
    };
    const { container, root } = await renderPanel(pullRequest);

    const unsafeLinks = Array.from(container.querySelectorAll<HTMLAnchorElement>(".pull-request-comment-list a"));
    expect(unsafeLinks).toHaveLength(3);
    expect(unsafeLinks.every((link) => link.getAttribute("href") === null)).toBe(true);
    expect(unsafeLinks.every((link) => link.getAttribute("aria-disabled") === "true")).toBe(true);
    await act(async () => {
      unsafeLinks.forEach((link) => expect(dispatchClick(link)).toBe(false));
    });
    expect(openExternalURL).not.toHaveBeenCalled();

    await act(async () => root.unmount());
    container.remove();
  });

  it("renders remote Markdown images inertly without network-capable elements", async () => {
    const pullRequest: PullRequest = {
      ...basePullRequest,
      body: "![body tracker](http://127.0.0.1:8080/body)",
      comments: [{
        ...basePullRequest.comments[0]!,
        body: "![comment tracker](https://attacker.example/track)",
      }],
    };
    const { container, root } = await renderPanel(pullRequest);

    expect(container.querySelectorAll(".pull-request-markdown img")).toHaveLength(0);
    expect(container.querySelectorAll(".pull-request-markdown-image-omitted")).toHaveLength(2);
    expect(container.innerHTML).not.toContain("127.0.0.1");
    expect(container.innerHTML).not.toContain("attacker.example");

    await act(async () => root.unmount());
    container.remove();
  });
});

describe("PullRequestPanel review safety", () => {
  it("binds a review to the displayed repository and head commit", async () => {
    const { container, root } = await renderPanel(basePullRequest);
    const reviewButton = container.querySelector<HTMLButtonElement>(".pull-request-comments > header button")!;
    await act(async () => reviewButton.click());

    const dialog = document.querySelector<HTMLDivElement>(".pull-request-dialog")!;
    const approve = Array.from(dialog.querySelectorAll<HTMLLabelElement>(".pull-request-review-kinds label"))
      .find((label) => label.textContent?.includes("Approve"))!;
    await act(async () => approve.querySelector<HTMLInputElement>("input")!.click());
    await act(async () => dialog.querySelector<HTMLFormElement>("form")!.requestSubmit());

    expect(mutatePullRequest).toHaveBeenCalledWith({
      number: 42,
      kind: "review",
      reviewKind: "approve",
      body: "",
      expectedHeadOid: "abcdef1234567890",
      expectedRepository: "example/azem",
    });

    await act(async () => root.unmount());
    container.remove();
  });
});

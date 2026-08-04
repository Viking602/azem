import {
  getPullRequestDashboard, getPullRequestDetail, mutatePullRequest, setPullRequestMonitor,
} from "./bridge";
import { useRuntimeStore } from "./store";
import type { PullRequestMutationRequest } from "./types";

let dashboardRequest = 0;
let detailRequest = 0;

export async function refreshPullRequestDashboard(): Promise<void> {
  const request = ++dashboardRequest;
  const store = useRuntimeStore.getState();
  store.setPullRequestLoading(true);
  try {
    const dashboard = await getPullRequestDashboard();
    if (request !== dashboardRequest) return;
    useRuntimeStore.getState().setPullRequestDashboard(dashboard);
  } catch (cause) {
    if (request !== dashboardRequest) return;
    useRuntimeStore.getState().setPullRequestError(cause instanceof Error ? cause.message : String(cause));
  }
}

export async function openPullRequest(number: number): Promise<void> {
  useRuntimeStore.getState().selectPullRequest(number);
  await refreshPullRequestDetail(number);
}

export async function refreshPullRequestDetail(number?: number): Promise<void> {
  const selected = number ?? useRuntimeStore.getState().selectedPullRequestNumber;
  if (!selected) return;
  const request = ++detailRequest;
  useRuntimeStore.getState().setPullRequestLoading(true);
  try {
    const detail = await getPullRequestDetail(selected);
    const state = useRuntimeStore.getState();
    if (request !== detailRequest || state.selectedPullRequestNumber !== selected) return;
    state.setPullRequestDetail(detail);
  } catch (cause) {
    if (request !== detailRequest) return;
    useRuntimeStore.getState().setPullRequestError(cause instanceof Error ? cause.message : String(cause));
  }
}

export async function runPullRequestMutation(request: PullRequestMutationRequest): Promise<boolean> {
  const store = useRuntimeStore.getState();
  store.setPullRequestError("");
  store.setPullRequestMutating(true);
  try {
    const detail = await mutatePullRequest(request);
    const state = useRuntimeStore.getState();
    state.setPullRequestDetail(detail);
    await refreshPullRequestDashboard();
    return true;
  } catch (cause) {
    useRuntimeStore.getState().setPullRequestError(cause instanceof Error ? cause.message : String(cause));
    return false;
  }
}

export async function togglePullRequestMonitor(number: number, enabled: boolean): Promise<boolean> {
  const store = useRuntimeStore.getState();
  store.setPullRequestError("");
  store.setPullRequestMutating(true);
  try {
    const monitor = await setPullRequestMonitor(number, enabled);
    const state = useRuntimeStore.getState();
    state.updatePullRequestMonitor(monitor);
    state.setPullRequestMutating(false);
    return true;
  } catch (cause) {
    useRuntimeStore.getState().setPullRequestError(cause instanceof Error ? cause.message : String(cause));
    return false;
  }
}

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { execute } from "../../bridge";
import { useRuntimeStore } from "../../store";
import type { SecurityProjection, Snapshot } from "../../types";
import SecurityPage from "./SecurityPage";

vi.mock("../../bridge", () => ({ execute: vi.fn(() => Promise.resolve()) }));

const snapshot: Snapshot = {
  workspace: "/tmp/azem", sessionId: "session-1", provider: "chatgpt", model: "gpt-test", reasoning: "high",
  agentMode: "single", language: "en", approvalMode: "prompt", queueMode: "queue", subagentConcurrency: 2,
  chatgptFastMode: false, sequence: 0,
};

const projection: SecurityProjection = {
  scan: {
    id: "scan_1", projectId: "/tmp/azem", mode: "standard", status: "complete", phase: "reporting", completeness: "complete",
    target: { kind: "repository", repository: "/tmp/azem", targetId: "target_1", displayName: "azem", snapshotDigest: "digest", includePaths: ["."], excludePaths: [] },
    route: { provider: "chatgpt", model: "gpt-test", reasoning: "high" }, outputDirectory: "/tmp/results",
    createdAt: "2026-08-23T00:00:00Z", updatedAt: "2026-08-23T00:01:00Z",
  },
  progress: { scanId: "scan_1", phase: "reporting", filesCompleted: 4, filesTotal: 4, workersPlanned: 1, workersDone: 1, updatedAt: "2026-08-23T00:01:00Z" },
  findings: [{
    findingId: "csf_1", occurrenceId: "occ_1", ruleId: "test.issue", title: "Unsafe test path", summary: "Synthetic vulnerability.",
    severity: { level: "high" }, confidence: { level: "high", rationale: "Source trace" }, taxonomy: { category: "test", cwe: ["CWE-20"] },
    locations: [{ path: "main.go", startLine: 7, role: "sink" }], remediation: "Validate input.",
  }],
};

describe("SecurityPage", () => {
  let container: HTMLDivElement;
  let root: Root;

  afterEach(async () => {
    await act(async () => root?.unmount());
    container?.remove();
    vi.clearAllMocks();
    vi.restoreAllMocks();
  });

  it("renders scan evidence and starts a host-owned scan", async () => {
    useRuntimeStore.setState({ snapshot, securityScans: [projection.scan], securityScansLoaded: true, securityProjection: projection, securityFindings: projection.findings ?? [], selectedSecurityFinding: null, securityPatch: null });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root.render(<SecurityPage />));
    expect(container.textContent).toContain("Unsafe test path");
    expect(container.textContent).toContain("4/4");
    const standard = [...container.querySelectorAll("button")].find((button) => button.textContent?.includes("Standard scan"));
    await act(async () => standard?.click());
    expect(execute).toHaveBeenCalledWith(expect.objectContaining({ kind: "start_security_scan", payload: expect.objectContaining({ mode: "standard" }) }));
  });

  it("shows blocked recovery and the exported artifact path", async () => {
    const blocked: SecurityProjection = {
      ...projection,
      scan: { ...projection.scan, status: "blocked", blockingReason: "Interrupted during shutdown", completeness: "partial" },
      findings: [],
    };
    useRuntimeStore.setState({
      snapshot,
      securityScans: [blocked.scan],
      securityScansLoaded: true,
      securityProjection: blocked,
      securityProjections: { [blocked.scan.id]: blocked },
      securityFindings: [],
      securityFindingsByScan: { [blocked.scan.id]: [] },
      selectedSecurityFinding: null,
      securityPatch: null,
      securityExportPath: "/tmp/results/results.sarif",
      error: "",
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root.render(<SecurityPage />));
    expect(container.textContent).toContain("Interrupted during shutdown");
    expect(container.textContent).toContain("/tmp/results/results.sarif");
    const resume = [...container.querySelectorAll("button")].find((button) => button.textContent?.includes("Resume scan"));
    await act(async () => resume?.click());
    expect(execute).toHaveBeenCalledWith(expect.objectContaining({ kind: "resume_security_scan", target: "scan_1" }));
  });

  it("triages a selected finding through the typed host action", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const finding = projection.findings![0];
    useRuntimeStore.setState({
      snapshot,
      securityScans: [projection.scan],
      securityScansLoaded: true,
      securityProjection: projection,
      securityProjections: { [projection.scan.id]: projection },
      securityFindings: [finding],
      securityFindingsByScan: { [projection.scan.id]: [finding] },
      selectedSecurityFinding: finding,
      securityPatch: null,
      securityExportPath: "",
      error: "",
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root.render(<SecurityPage />));
    const close = [...container.querySelectorAll("button")].find((button) => button.textContent === "False positive");
    await act(async () => close?.click());
    expect(execute).toHaveBeenCalledWith(expect.objectContaining({
      kind: "set_security_finding_triage",
      payload: expect.objectContaining({ occurrenceId: "occ_1", status: "closed", closeReason: "false_positive" }),
    }));
  });
});

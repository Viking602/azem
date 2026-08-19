import { lazy, Suspense, type ReactNode } from "react";
import { useRuntimeStore } from "../store";
import type { View } from "../types";
import ThreadSurface from "./ThreadSurface";

const CommandPalette = lazy(() => import("./CommandPalette"));
const Pages = lazy(() => import("./Pages"));
const SubagentsDrawer = lazy(() => import("./SubagentsPage"));
const SettingsDialog = lazy(() => import("./SettingsDialog"));
const PullRequestPanel = lazy(() => import("./PullRequestPanel"));
const Inspector = lazy(() => import("./Inspector"));
const AgentSideChat = lazy(() => import("./AgentSideChat"));
const TerminalPanel = lazy(() => import("./TerminalPanel"));

export function AppWorkspace({
  view,
  fallback,
  terminalOpen,
  terminalMounted,
  showInspector,
  showAgentDrawer,
  showAgentDetailDrawer,
  showPullRequest,
}: {
  view: View;
  fallback: ReactNode;
  terminalOpen: boolean;
  terminalMounted: boolean;
  showInspector: boolean;
  showAgentDrawer: boolean;
  showAgentDetailDrawer: boolean;
  showPullRequest: boolean;
}) {
  return <>
    <main className="workspace-main" data-terminal={terminalOpen ? "open" : "closed"}>
      <div className="workspace-primary">
        {view === "thread" || view === "agents" ? (
          <ThreadSurface />
        ) : (
          <Suspense fallback={fallback}>
            <Pages view={view} />
          </Suspense>
        )}
        {showInspector && <Suspense fallback={null}><Inspector /></Suspense>}
        {showAgentDrawer && <Suspense fallback={null}>
          <div className="subagents-drawer-layer" onClick={(event) => {
            if (event.target === event.currentTarget) useRuntimeStore.getState().setView("thread");
          }}>
            <SubagentsDrawer />
          </div>
        </Suspense>}
        {showAgentDetailDrawer && (
          <Suspense fallback={null}>
            <div className="subagent-detail-drawer-layer" onClick={(event) => {
              if (event.target === event.currentTarget) useRuntimeStore.getState().selectAgent("");
            }}>
              <AgentSideChat />
            </div>
          </Suspense>
        )}
      </div>
      {terminalMounted && <Suspense fallback={null}><TerminalPanel /></Suspense>}
    </main>
    {showPullRequest && <Suspense fallback={null}><PullRequestPanel /></Suspense>}
  </>;
}

export function AppOverlays({ settingsOpen, commandOpen }: { settingsOpen: boolean; commandOpen: boolean }) {
  return <>
    {settingsOpen && <Suspense fallback={null}><SettingsDialog /></Suspense>}
    {commandOpen && <Suspense fallback={null}><CommandPalette /></Suspense>}
  </>;
}

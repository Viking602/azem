import { createInterface } from "node:readline";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const workerArg = process.argv.at(-1);
if (workerArg === "__omp_worker_daemon_broker") {
  const brokerPath = process.env.AZEM_CODING_AGENT_LAUNCH_BROKER;
  if (!brokerPath) throw new Error("AZEM_CODING_AGENT_LAUNCH_BROKER is required");
  const { startDaemonBrokerFromEnvironment } = await import(pathToFileURL(brokerPath).href);
  await startDaemonBrokerFromEnvironment();
  process.exit(0);
}
if (workerArg === "__omp_worker_terminal_output") {
  const terminalWorkerPath = process.env.AZEM_CODING_AGENT_TERMINAL_WORKER;
  if (!terminalWorkerPath) throw new Error("AZEM_CODING_AGENT_TERMINAL_WORKER is required");
  await import(pathToFileURL(terminalWorkerPath).href);
  await new Promise(() => {});
}

const modulePath = process.env.AZEM_CODING_AGENT_LSP;
const clientPath = process.env.AZEM_CODING_AGENT_LSP_CLIENT;
const debugPath = process.env.AZEM_CODING_AGENT_DEBUG;
const dapPath = process.env.AZEM_CODING_AGENT_DAP;
const evalPath = process.env.AZEM_CODING_AGENT_EVAL;
const evalJsPath = process.env.AZEM_CODING_AGENT_EVAL_JS;
const evalPyPath = process.env.AZEM_CODING_AGENT_EVAL_PY;
const browserPath = process.env.AZEM_CODING_AGENT_BROWSER;
const browserTabsPath = process.env.AZEM_CODING_AGENT_BROWSER_TABS;
const computerPath = process.env.AZEM_CODING_AGENT_COMPUTER;
const webSearchPath = process.env.AZEM_CODING_AGENT_WEB_SEARCH;
const githubPath = process.env.AZEM_CODING_AGENT_GITHUB;
const sshPath = process.env.AZEM_CODING_AGENT_SSH;
const internalUrlPath = process.env.AZEM_CODING_AGENT_INTERNAL_URL;
const sshConnectionsPath = process.env.AZEM_CODING_AGENT_SSH_CONNECTIONS;
const hubPath = process.env.AZEM_CODING_AGENT_HUB;
const launchClientPath = process.env.AZEM_CODING_AGENT_LAUNCH_CLIENT;
const imageGenPath = process.env.AZEM_CODING_AGENT_IMAGE_GEN;
const ttsPath = process.env.AZEM_CODING_AGENT_TTS;
if (!modulePath || !clientPath || !debugPath || !dapPath || !evalPath || !evalJsPath || !evalPyPath || !browserPath || !browserTabsPath || !computerPath || !webSearchPath || !githubPath || !sshPath || !internalUrlPath || !sshConnectionsPath || !hubPath || !launchClientPath || !imageGenPath || !ttsPath) {
  throw new Error("OMP runtime module paths are required");
}
const [
  { LspTool }, { shutdownAll }, { DebugTool }, { dapSessionManager },
  { EvalTool }, { disposeAllVmContexts }, { disposeAllKernelSessions },
  { BrowserTool }, { releaseAllTabs }, { ComputerTool }, { runSearchQuery }, { GithubTool },
  { SshProtocolHandler }, { parseInternalUrl }, { closeAllConnections }, { HubTool }, { closeDaemonClients },
  { imageGenTool }, { ttsTool },
] = await Promise.all([
  import(pathToFileURL(modulePath).href),
  import(pathToFileURL(clientPath).href),
  import(pathToFileURL(debugPath).href),
  import(pathToFileURL(dapPath).href),
  import(pathToFileURL(evalPath).href),
  import(pathToFileURL(evalJsPath).href),
  import(pathToFileURL(evalPyPath).href),
  import(pathToFileURL(browserPath).href),
  import(pathToFileURL(browserTabsPath).href),
  import(pathToFileURL(computerPath).href),
  import(pathToFileURL(webSearchPath).href),
  import(pathToFileURL(githubPath).href),
  import(pathToFileURL(sshPath).href),
  import(pathToFileURL(internalUrlPath).href),
  import(pathToFileURL(sshConnectionsPath).href),
  import(pathToFileURL(hubPath).href),
  import(pathToFileURL(launchClientPath).href),
  import(pathToFileURL(imageGenPath).href),
  import(pathToFileURL(ttsPath).href),
]);
const tools = new Map();
const debugTools = new Map();
const evalTools = new Map();
const browserTools = new Map();
const computerTools = new Map();
const githubTools = new Map();
const hubTools = new Map();
let artifactSequence = 0;
const inflight = new Map();
const sshHandler = new SshProtocolHandler();

function toolFor(cwd, readOnly) {
  const key = `${cwd}\0${readOnly ? "ro" : "rw"}`;
  let tool = tools.get(key);
  if (tool) return tool;
  const settings = {
    get(name) {
      if (name === "tools.maxTimeout") return 300;
      return undefined;
    },
  };
  tool = new LspTool({ cwd, lspReadOnly: readOnly, enableLsp: true, settings });
  tools.set(key, tool);
  return tool;
}

function debugToolFor(cwd) {
  let tool = debugTools.get(cwd);
  if (tool) return tool;
  const settings = { get: (name) => name === "tools.maxTimeout" ? 300 : name === "debug.enabled" };
  tool = new DebugTool({ cwd, settings });
  debugTools.set(cwd, tool);
  return tool;
}

function evalToolFor(message) {
  const key = `${message.cwd}\0${message.sessionId ?? "workspace"}`;
  let tool = evalTools.get(key);
  if (tool) return tool;
  const settings = {
    get(name) {
      if (name === "tools.maxTimeout") return 3600;
      if (name === "eval.py" || name === "eval.js") return true;
      if (name === "eval.rb" || name === "eval.jl") return true;
      if (name === "eval.autoBackground.enabled") return false;
      return undefined;
    },
  };
  const session = {
    cwd: message.cwd,
    settings,
    toolRegistry: new Map(),
    getSessionSpawns: () => false,
    getSessionId: () => message.sessionId ?? "workspace",
    getEvalSessionId: () => key,
    getEvalKernelOwnerId: () => key,
    getArtifactsDir: () => message.artifactsDir ?? null,
    getEvalBridgeToolNames: () => [],
    getUsageStatistics: () => ({ output: 0 }),
  };
  tool = new EvalTool(session);
  evalTools.set(key, tool);
  return tool;
}

function browserToolFor(message) {
  const key = `${message.cwd}\0${message.sessionId ?? "workspace"}`;
  let tool = browserTools.get(key);
  if (tool) return tool;
  const settings = {
    get(name) {
      if (name === "tools.maxTimeout") return 300;
      if (name === "browser.headless") return true;
      if (name === "browser.relay" || name === "browser.cmux") return false;
      return undefined;
    },
  };
  tool = new BrowserTool({
    cwd: message.cwd,
    settings,
    getSessionId: () => message.sessionId ?? "workspace",
  });
  browserTools.set(key, tool);
  return tool;
}

function computerToolFor(message) {
  const key = `${message.cwd}\0${message.sessionId ?? "workspace"}`;
  let tool = computerTools.get(key);
  if (tool) return tool;
  const settings = {
    get(name) {
      if (name === "tools.maxTimeout") return 300;
      if (name === "computer.maxWidth") return 3840;
      if (name === "computer.maxHeight") return 2400;
      if (name === "computer.display") return "all";
      return undefined;
    },
  };
  tool = new ComputerTool({
    cwd: message.cwd,
    settings,
    getSessionId: () => message.sessionId ?? "workspace",
    getEvalSessionId: () => key,
    getEvalKernelOwnerId: () => key,
  });
  computerTools.set(key, tool);
  return tool;
}

function githubToolFor(message) {
  const key = `${message.cwd}\0${message.sessionId ?? "workspace"}`;
  let tool = githubTools.get(key);
  if (tool) return tool;
  const settings = { get: (name) => name === "images.autoResize" };
  const allocateOutputArtifact = async (kind) => {
    if (!message.artifactsDir) return undefined;
    mkdirSync(message.artifactsDir, { recursive: true });
    const id = `github-${++artifactSequence}`;
    return { id, path: join(message.artifactsDir, `${id}-${String(kind)}.txt`) };
  };
  tool = new GithubTool({
    cwd: message.cwd,
    settings,
    getSessionId: () => message.sessionId ?? "workspace",
    allocateOutputArtifact,
  });
  githubTools.set(key, tool);
  return tool;
}

function hubToolFor(message) {
  const key = `${message.cwd}\0${message.sessionId ?? "workspace"}`;
  let tool = hubTools.get(key);
  if (tool) return tool;
  const settings = {
    get(name) {
      if (name === "launch.enabled") return true;
      if (name === "tools.maxTimeout") return 3600;
      if (name === "async.pollWaitDuration") return "30s";
      return undefined;
    },
  };
  tool = new HubTool({
    cwd: message.cwd,
    settings,
    getSessionId: () => message.sessionId ?? "workspace",
    getAgentId: () => message.agentId ?? message.params.__agentId,
  });
  hubTools.set(key, tool);
  return tool;
}

function renderContent(content) {
  if (!Array.isArray(content)) return "";
  return content.map((part) => part?.type === "text" ? String(part.text ?? "") : `[${String(part?.type ?? "content")}]`).join("\n");
}

async function execute(message) {
  const controller = new AbortController();
  inflight.set(message.id, controller);
  try {
    let result;
    if (message.tool === "web_search") {
      result = await runSearchQuery(message.params, { sessionId: message.sessionId, signal: controller.signal });
    } else if (message.tool === "generate_image" || message.tool === "tts") {
      const custom = message.tool === "generate_image" ? imageGenTool : ttsTool;
      const noCredentialRegistry = {
        authStorage: { hasNonEnvCredential: () => false },
        getApiKeyForProvider: async () => null,
        getAll: () => [],
        getProviderBaseUrl: () => undefined,
        getProviderHeaders: () => undefined,
        find: () => undefined,
      };
      const customContext = {
        sessionManager: {
          getSessionId: () => message.sessionId ?? "workspace",
          getCwd: () => message.cwd,
        },
        modelRegistry: message.tool === "tts" ? noCredentialRegistry : undefined,
        model: undefined,
        fetch,
      };
      result = await custom.execute(String(message.id), message.params, undefined, customContext, controller.signal);
    } else if (message.tool === "ssh") {
      const internalUrl = parseInternalUrl(message.params.uri);
      if (message.params.action === "write") {
        const content = String(message.params.content ?? "");
        await sshHandler.write(internalUrl, content, { cwd: message.cwd, signal: controller.signal });
        result = { content: [{ type: "text", text: `Wrote ${Buffer.byteLength(content)} bytes to ${message.params.uri}` }], details: { uri: message.params.uri } };
      } else {
        const remote = await sshHandler.resolve(internalUrl, { cwd: message.cwd, signal: controller.signal });
        result = { content: [{ type: "text", text: remote.content }], details: { resource: remote } };
      }
    } else {
      const tool = message.tool === "debug"
        ? debugToolFor(message.cwd)
        : message.tool === "eval"
          ? evalToolFor(message)
          : message.tool === "browser"
            ? browserToolFor(message)
            : message.tool === "computer"
              ? computerToolFor(message)
              : message.tool === "github"
                ? githubToolFor(message)
                : message.tool === "hub"
                  ? hubToolFor(message)
                  : toolFor(message.cwd, Boolean(message.readOnly));
      result = await tool.execute(String(message.id), message.params, controller.signal);
    }
    process.stdout.write(`${JSON.stringify({
      id: message.id,
      ok: true,
      isError: Boolean(result.isError),
      content: renderContent(result.content),
      parts: result.content ?? [],
      details: result.details ?? null,
    })}\n`);
  } catch (error) {
    process.stdout.write(`${JSON.stringify({ id: message.id, ok: false, error: error instanceof Error ? error.message : String(error) })}\n`);
  } finally {
    inflight.delete(message.id);
  }
}

const input = createInterface({ input: process.stdin, crlfDelay: Infinity });
input.on("line", (line) => {
  let message;
  try {
    message = JSON.parse(line);
  } catch (error) {
    process.stdout.write(`${JSON.stringify({ id: null, ok: false, error: `invalid request: ${String(error)}` })}\n`);
    return;
  }
  if (message.cancel !== undefined) {
    inflight.get(message.cancel)?.abort();
    return;
  }
  void execute(message);
});
async function shutdown() {
  for (const controller of inflight.values()) controller.abort();
  try {
    await dapSessionManager.terminate(undefined, 3000);
  } catch {
    // Best-effort termination continues through LSP shutdown.
  }
  await shutdownAll();
  await Promise.allSettled([releaseAllTabs({ kill: true, timeoutMs: 3000 })]);
  await Promise.allSettled([...computerTools.values()].map((tool) => tool.close()));
  await Promise.allSettled([disposeAllVmContexts(), disposeAllKernelSessions()]);
  await Promise.allSettled([closeAllConnections()]);
  await Promise.allSettled([closeDaemonClients()]);
}

input.on("close", async () => {
  await shutdown();
  process.exit(0);
});
for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, async () => {
    await shutdown();
    process.exit(0);
  });
}

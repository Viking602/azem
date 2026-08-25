import { pathToFileURL } from "node:url";
import readline from "node:readline";

const tools = new Map();
const controllers = new Map();
let cwd = process.cwd();
const extensionCommands = new Map();
const extensionProviders = new Map();
const extensionAgents = new Map();
const extensionHandlers = new Map();
const extensionToolDefinitions = new Map();
const extensionWriteFallbacks = [];
const extensionDeleteFallbacks = [];
let writeTail = Promise.resolve();

function writeMessage(value) {
  const line = JSON.stringify(value) + "\n";
  writeTail = writeTail.then(() => new Promise((resolve, reject) => {
    process.stdout.write(line, error => error ? reject(error) : resolve());
  })).catch(error => process.stderr.write(`custom-tool bridge write: ${error}\n`));
}

function schemaNode(schema, optional = false) {
  const node = {
    __schema: schema,
    __optional: optional,
    optional() { return schemaNode(schema, true); },
    describe(description) { return schemaNode({ ...schema, description }, optional); },
    default(value) { return schemaNode({ ...schema, default: value }, true); },
    nullable() { return schemaNode({ anyOf: [schema, { type: "null" }] }, optional); },
    array() { return schemaNode({ type: "array", items: schema }, optional); },
  };
  return node;
}

function unwrapSchema(value) {
  if (!value) return {};
  if (value.__schema) return value.__schema;
  if (typeof value.toJsonSchema === "function") return value.toJsonSchema();
  if (typeof value.toJSONSchema === "function") return value.toJSONSchema();
  if (typeof value === "object") return value;
  return {};
}

const builder = {
  string: () => schemaNode({ type: "string" }),
  number: () => schemaNode({ type: "number" }),
  integer: () => schemaNode({ type: "integer" }),
  boolean: () => schemaNode({ type: "boolean" }),
  any: () => schemaNode({}),
  unknown: () => schemaNode({}),
  literal: value => schemaNode({ const: value }),
  enum: values => schemaNode({ type: "string", enum: [...values] }),
  array: item => schemaNode({ type: "array", items: unwrapSchema(item) }),
  union: values => schemaNode({ anyOf: values.map(unwrapSchema) }),
  object: shape => {
    const properties = {};
    const required = [];
    for (const [name, value] of Object.entries(shape ?? {})) {
      properties[name] = unwrapSchema(value);
      if (!value?.__optional) required.push(name);
    }
    return schemaNode({ type: "object", properties, required, additionalProperties: false });
  },
  record: value => schemaNode({ type: "object", additionalProperties: unwrapSchema(value) }),
};

const typebox = {
  String: options => ({ type: "string", ...(options ?? {}) }),
  Number: options => ({ type: "number", ...(options ?? {}) }),
  Integer: options => ({ type: "integer", ...(options ?? {}) }),
  Boolean: options => ({ type: "boolean", ...(options ?? {}) }),
  Literal: value => ({ const: value }),
  Array: (item, options) => ({ type: "array", items: unwrapSchema(item), ...(options ?? {}) }),
  Union: values => ({ anyOf: values.map(unwrapSchema) }),
  Optional: value => ({ ...unwrapSchema(value), __optional: true }),
  Object: (shape, options) => {
    const properties = {};
    const required = [];
    for (const [name, value] of Object.entries(shape ?? {})) {
      const optional = value?.__optional === true;
      properties[name] = { ...unwrapSchema(value) };
      delete properties[name].__optional;
      if (!optional) required.push(name);
    }
    return { type: "object", properties, required, additionalProperties: false, ...(options ?? {}) };
  },
};

function arktype(grammar) {
  if (typeof grammar === "object") return builder.object(grammar);
  const token = String(grammar).trim();
  if (token === "string") return builder.string();
  if (token === "number") return builder.number();
  if (token === "boolean") return builder.boolean();
  return builder.any();
}

async function exec(command, args = [], options = {}) {
  const controller = new AbortController();
  const externalSignal = options.signal;
  const abort = () => controller.abort();
  externalSignal?.addEventListener?.("abort", abort, { once: true });
  try {
    const child = Bun.spawn([String(command), ...args.map(String)], {
      cwd: options.cwd || cwd,
      env: options.env ? { ...process.env, ...options.env } : process.env,
      stdin: options.stdin == null ? "ignore" : new Blob([String(options.stdin)]),
      stdout: "pipe",
      stderr: "pipe",
      signal: controller.signal,
    });
    const [stdout, stderr, code] = await Promise.all([
      new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited,
    ]);
    return { stdout: stdout.slice(0, 4 << 20), stderr: stderr.slice(0, 4 << 20), code, killed: controller.signal.aborted };
  } finally {
    externalSignal?.removeEventListener?.("abort", abort);
  }
}

function hostAPI() {
  return {
    cwd,
    exec,
    hasUI: false,
    ui: new Proxy({}, { get: () => async () => undefined }),
    logger: { debug() {}, info() {}, warn() {}, error() {} },
    zod: builder,
    arktype,
    typebox,
    pi: {},
    pushPendingAction() { throw new Error("Pending action store unavailable for custom tools in this runtime."); },
  };
}

function definitionOf(tool, modulePath) {
  if (!tool || typeof tool !== "object" || typeof tool.name !== "string" || typeof tool.execute !== "function") {
    throw new Error(`${modulePath}: custom tool must define name and execute`);
  }
  const parameters = unwrapSchema(tool.parameters ?? tool.inputSchema ?? { type: "object", properties: {}, additionalProperties: false });
  const approval = typeof tool.approval === "string" ? tool.approval : tool.approval?.tier;
  return {
    name: tool.name,
    label: tool.label || tool.name,
    description: tool.description || `${tool.name} custom tool`,
    parameters,
    strict: tool.strict === true,
    hidden: tool.hidden === true,
    approval: approval || "exec",
    concurrency: tool.concurrency || "parallel",
    modulePath,
  };
}

function extensionAPI(modulePath) {
  return {
    cwd,
    zod: builder,
    arktype,
    typebox,
    pi: {},
    registerTool(tool) {
      const definition = definitionOf(tool, modulePath);
      if (tools.has(definition.name)) throw new Error(`duplicate custom tool ${definition.name}`);
      tools.set(definition.name, tool);
      extensionToolDefinitions.set(definition.name, definition);
    },
    registerCommand(name, options) {
      if (typeof name !== "string" || typeof options?.handler !== "function") throw new Error("extension command requires name and handler");
      if (extensionCommands.has(name)) throw new Error(`duplicate extension command ${name}`);
      extensionCommands.set(name, { ...options, modulePath });
    },
    registerProvider(name, config) {
      if (typeof name !== "string" || !config || typeof config !== "object") throw new Error("extension provider requires name and config");
      extensionProviders.set(name, { ...config, modulePath });
    },
    unregisterProvider(name) { extensionProviders.delete(name); },
    registerAgent(name, config) {
      if (typeof name !== "string" || !config || typeof config !== "object") throw new Error("extension agent requires name and config");
      extensionAgents.set(name, { ...config, modulePath });
    },
    on(event, handler) {
      if (typeof handler !== "function") throw new Error("extension event handler must be a function");
      const handlers = extensionHandlers.get(event) ?? [];
      handlers.push(handler);
      extensionHandlers.set(event, handlers);
    },
    registerFileWriteFallback(handler) {
      if (typeof handler !== "function") throw new Error("file write fallback must be a function");
      extensionWriteFallbacks.push({ handler, modulePath });
    },
    registerFileDeleteFallback(handler) {
      if (typeof handler !== "function") throw new Error("file delete fallback must be a function");
      extensionDeleteFallbacks.push({ handler, modulePath });
    },
    exec,
    logger: { debug() {}, info() {}, warn() {}, error() {} },
    events: { emit() {}, on() { return () => {}; } },
  };
}

function serializable(value) {
  return JSON.parse(JSON.stringify(value));
}

async function initialize(request) {
  cwd = request.cwd || cwd;
  tools.clear();
  extensionCommands.clear();
  extensionProviders.clear();
  extensionAgents.clear();
  extensionHandlers.clear();
  extensionToolDefinitions.clear();
  extensionWriteFallbacks.length = 0;
  extensionDeleteFallbacks.length = 0;
  const definitions = [];
  const diagnostics = [];
  for (const modulePath of request.modules ?? []) {
    try {
      const imported = await import(pathToFileURL(modulePath).href + `?azem=${Date.now()}-${Math.random()}`);
      const factory = imported.default ?? imported.createTool ?? imported.factory;
      if (typeof factory !== "function") throw new Error(`${modulePath}: default export must be a custom tool factory`);
      const produced = await factory(hostAPI());
      const staged = [];
      for (const tool of Array.isArray(produced) ? produced : [produced]) {
        const definition = definitionOf(tool, modulePath);
        if (tools.has(definition.name) || staged.some(item => item.definition.name === definition.name)) {
          throw new Error(`duplicate custom tool ${definition.name}`);
        }
        staged.push({ definition, tool });
      }
      for (const item of staged) {
        tools.set(item.definition.name, item.tool);
        definitions.push(item.definition);
      }
    } catch (error) {
      diagnostics.push(`${modulePath}: ${error?.message || String(error)}`);
    }
  }
  for (const modulePath of request.extensions ?? []) {
    const toolNames = new Set(tools.keys());
    const commandNames = new Set(extensionCommands.keys());
    const providerNames = new Set(extensionProviders.keys());
    const agentNames = new Set(extensionAgents.keys());
    const writeFallbackCount = extensionWriteFallbacks.length;
    const deleteFallbackCount = extensionDeleteFallbacks.length;
    try {
      const imported = await import(pathToFileURL(modulePath).href + `?azemext=${Date.now()}-${Math.random()}`);
      const factory = imported.default ?? imported.extension;
      if (typeof factory !== "function") throw new Error(`${modulePath}: default export must be an extension factory`);
      await factory(extensionAPI(modulePath));
    } catch (error) {
      for (const name of tools.keys()) if (!toolNames.has(name)) tools.delete(name);
      for (const name of extensionCommands.keys()) if (!commandNames.has(name)) extensionCommands.delete(name);
      for (const name of extensionProviders.keys()) if (!providerNames.has(name)) extensionProviders.delete(name);
      for (const name of extensionAgents.keys()) if (!agentNames.has(name)) extensionAgents.delete(name);
      for (const name of extensionToolDefinitions.keys()) if (!toolNames.has(name)) extensionToolDefinitions.delete(name);
      extensionWriteFallbacks.length = writeFallbackCount;
      extensionDeleteFallbacks.length = deleteFallbackCount;
      diagnostics.push(`${modulePath}: ${error?.message || String(error)}`);
    }
  }
  definitions.push(...extensionToolDefinitions.values());
  const themePaths = [];
  for (const handler of extensionHandlers.get("resources_discover") ?? []) {
    try {
      const result = await handler({ cwd });
      if (Array.isArray(result?.themePaths)) themePaths.push(...result.themePaths.map(String));
    } catch (error) {
      diagnostics.push(`resources_discover: ${error?.message || String(error)}`);
    }
  }
  const commands = [...extensionCommands].map(([name, value]) => ({ name, description: value.description ?? name, modulePath: value.modulePath }));
  const providers = [...extensionProviders].map(([name, value]) => ({ name, config: serializable(value), modulePath: value.modulePath }));
  const agents = [...extensionAgents].map(([name, value]) => ({ name, config: serializable(value), modulePath: value.modulePath }));
  return { definitions, diagnostics, commands, providers, agents, themePaths, writeFallbacks: extensionWriteFallbacks.length, deleteFallbacks: extensionDeleteFallbacks.length };
}

function textFromContent(content) {
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  return content.map(part => {
    if (typeof part === "string") return part;
    if (part?.type === "text") return String(part.text ?? "");
    if (part?.type === "image") return `[image ${part.mimeType ?? part.mediaType ?? "application/octet-stream"}]`;
    return JSON.stringify(part);
  }).filter(Boolean).join("\n");
}

function normalizeResult(value) {
  if (value == null) return { content: "", parts: [] };
  if (typeof value === "string") return { content: value, parts: [{ kind: "text", text: value }] };
  const rawParts = Array.isArray(value.content) ? value.content : [];
  const parts = rawParts.map(part => {
    if (typeof part === "string") return { kind: "text", text: part };
    if (part?.type === "text") return { kind: "text", text: String(part.text ?? "") };
    if (part?.type === "image") return { kind: "image", data: part.data ?? "", mediaType: part.mimeType ?? part.mediaType ?? "application/octet-stream" };
    return { kind: "provider_data", providerData: part };
  });
  return { content: textFromContent(value.content ?? value), parts, details: value.details ?? value.structuredContent ?? null, isError: value.isError === true };
}

async function execute(request) {
  const tool = tools.get(request.tool);
  if (!tool) throw new Error(`unknown custom tool ${request.tool}`);
  const controller = new AbortController();
  controllers.set(request.id, controller);
  const onUpdate = update => writeMessage({ id: request.id, type: "update", update: normalizeResult(update) });
  const context = { sessionManager: null, modelRegistry: null, model: request.model ?? null, isIdle: () => false, hasQueuedMessages: () => false, abort: () => controller.abort(), settings: {} };
  try {
    const result = await tool.execute(request.callId ?? request.id, request.args ?? {}, onUpdate, context, controller.signal);
    return normalizeResult(result);
  } finally {
    controllers.delete(request.id);
  }
}

async function executeExtensionCommand(request) {
  const command = extensionCommands.get(request.command);
  if (!command) throw new Error(`unknown extension command ${request.command}`);
  const messages = [];
  const context = {
    cwd,
    ui: new Proxy({}, { get: () => async () => undefined }),
    sendMessage(value) {
      if (typeof value === "string") messages.push(value);
      else if (value?.content) messages.push(textFromContent(value.content));
    },
    sendUserMessage(value) { messages.push(String(value)); },
    exec,
  };
  const returned = await command.handler(request.args ?? "", context);
  if (typeof returned === "string") return { prompt: returned };
  if (returned?.prompt) return { prompt: String(returned.prompt) };
  if (messages.length > 0) return { prompt: messages.join("\n\n") };
  return { output: returned == null ? "" : JSON.stringify(returned) };
}

async function executeFileFallback(request, handlers, deleting) {
  const context = {
    cwd,
    sessionId: request.sessionId ?? "",
    exec,
    ui: new Proxy({}, { get: () => async () => undefined }),
    logger: { debug() {}, info() {}, warn() {}, error() {} },
  };
  const payload = deleting
    ? { dst: String(request.dst ?? ""), cause: request.cause ?? "", confirmedFile: request.confirmedFile === true, sessionId: context.sessionId }
    : { dst: String(request.dst ?? ""), content: String(request.content ?? ""), cause: request.cause ?? "", sessionId: context.sessionId };
  for (const { handler, modulePath } of handlers) {
    try {
      if (await handler(payload, context) === true) return { handled: true };
    } catch (error) {
      process.stderr.write(`${modulePath}: file ${deleting ? "delete" : "write"} fallback failed: ${error?.message || String(error)}\n`);
    }
  }
  return { handled: false };
}

async function handle(request) {
  if (request.op === "cancel") {
    controllers.get(request.id)?.abort();
    return;
  }
  try {
    let result;
    if (request.op === "init") result = await initialize(request);
    else if (request.op === "execute") result = await execute(request);
    else if (request.op === "command") result = await executeExtensionCommand(request);
    else if (request.op === "broker_write") result = await executeFileFallback(request, extensionWriteFallbacks, false);
    else if (request.op === "broker_delete") result = await executeFileFallback(request, extensionDeleteFallbacks, true);
    else if (request.op === "shutdown") {
      for (const controller of controllers.values()) controller.abort();
      for (const tool of tools.values()) await tool.onSession?.({ reason: "shutdown" }, {});
      result = {};
    } else throw new Error(`unknown operation ${request.op}`);
    writeMessage({ id: request.id, type: "result", result });
    if (request.op === "shutdown") {
      await writeTail;
      process.exit(0);
    }
  } catch (error) {
    writeMessage({ id: request.id, type: "error", error: error?.stack || String(error) });
  }
}

const lines = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
lines.on("line", line => {
  if (!line.trim()) return;
  let request;
  try { request = JSON.parse(line); }
  catch (error) { writeMessage({ id: "", type: "error", error: String(error) }); return; }
  void handle(request);
});

import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const modulePath = process.env.AZEM_PI_NATIVES;
if (!modulePath) {
  throw new Error("AZEM_PI_NATIVES is required");
}
const native = await import(pathToFileURL(modulePath).href);
const request = JSON.parse(readFileSync(0, "utf8"));
let result;
switch (request.operation) {
  case "grep":
    result = await native.astGrep(request.options);
    break;
  case "match":
    result = await native.astMatch(request.options);
    break;
  case "edit": {
    const options = { ...request.options };
    if (Array.isArray(options.rewriteOps)) {
      options.rewrites = Object.fromEntries(options.rewriteOps.map(({ pat, out }) => [pat, out]));
      delete options.rewriteOps;
    }
    result = await native.astEdit(options);
    break;
  }
  default:
    throw new Error(`unsupported AST bridge operation: ${String(request.operation)}`);
}
process.stdout.write(JSON.stringify(result));

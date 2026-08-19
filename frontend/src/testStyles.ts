// @ts-expect-error Vitest runs in Node; production TypeScript intentionally excludes Node types.
import { readFileSync } from "node:fs";

/** Resolve CSS import hubs recursively while preserving cascade order. */
export function readStylesheetTree(path: string): string {
  const source = readFileSync(path, "utf8");
  const directory = path.slice(0, path.lastIndexOf("/") + 1);
  return source
    .split("\n")
    .map((line: string) => {
      const imported = /^@import "\.\/(.+)";$/.exec(line)?.[1];
      return imported ? readStylesheetTree(`${directory}${imported}`) : line;
    })
    .join("\n");
}

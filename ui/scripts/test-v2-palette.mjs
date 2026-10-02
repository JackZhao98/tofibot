import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

const sourceRoot = new URL("../src/", import.meta.url).pathname;
const failures = [];

function inspect(directory) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      if (relative(sourceRoot, path) !== "lib/tofi-avatar/acceptance") inspect(path);
      continue;
    }
    if (!/\.(?:css|tsx?)$/.test(entry.name)) continue;
    const name = relative(sourceRoot, path);
    readFileSync(path, "utf8").split("\n").forEach((line, index) => {
      const withoutMask = line.replace(/mask-image:[^;}]+/g, "");
      if (name === "design-tokens.css" && /^\s*--/.test(withoutMask)) return;
      if (/#[\da-f]{3,8}\b|rgba?\(/i.test(withoutMask)) failures.push(`${name}:${index + 1}`);
    });
  }
}

inspect(sourceRoot);
if (failures.length) throw new Error(`Colors outside v2 tokens: ${failures.join(", ")}`);
console.log("v2 palette check: PASS");

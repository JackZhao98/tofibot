// Unified dropdown guard: every pick-one-of-N control is src/Select.tsx (Radix Select, styled with our tokens).
//  1. No native <select element in product source (ts/tsx/jsx under src/) except inside Select.tsx / SelectRadix.tsx.
//  2. Radix Select is imported by SelectRadix.tsx only, so it stays in a lazy chunk: Select.tsx loads it with import().
// Usage: npm run test:select-guard
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const src = join(ui, "src");
const walk = dir => readdirSync(dir).flatMap(name => { const path = join(dir, name); return statSync(path).isDirectory() ? walk(path) : [path]; });
const files = walk(src).filter(file => /\.(tsx?|jsx?)$/.test(file));
const allowed = new Set(["Select.tsx", "SelectRadix.tsx"]);
const failures = [];

for (const file of files) {
  const rel = relative(src, file);
  if (allowed.has(rel)) continue;
  const text = readFileSync(file, "utf8");
  text.split("\n").forEach((line, index) => {
    if (/<select[\s>/]/.test(line) || /createElement\(\s*["']select["']/.test(line)) failures.push(`${rel}:${index + 1}: native select element; use Select from src/Select.tsx`);
    if (/@radix-ui\/react-select/.test(line)) failures.push(`${rel}:${index + 1}: import @radix-ui/react-select only in SelectRadix.tsx`);
  });
}
const entry = readFileSync(join(src, "Select.tsx"), "utf8");
if (/from\s+["']@radix-ui\/react-select["']/.test(entry) || /from\s+["']\.\/SelectRadix["']/.test(entry)) failures.push("Select.tsx must not import Radix or SelectRadix statically; use lazy(() => import(\"./SelectRadix\"))");
if (!/import\(\s*["']\.\/SelectRadix["']\s*\)/.test(entry)) failures.push("Select.tsx must load SelectRadix with a dynamic import()");

if (failures.length) { for (const message of failures) console.log(`FAIL ${message}`); process.exit(1); }
console.log(`ok   no native select element in ${files.length} source files; Radix Select is lazy`);

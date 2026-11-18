// Writes the generated skin files from src/skin/v3.tokens.json (V3 PLAN 7.3): skin/v3.css, skin/structure.css,
// skin/tokens.ts and the two probe skins. Run: npm --prefix ui run build:skin
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { buildAll, ui } from "./skin-lib.mjs";

const files = buildAll();
for (const [file, text] of Object.entries(files)) {
  const path = join(ui, "src", file);
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, text);
}
console.log(`build:skin wrote ${Object.keys(files).join(", ")}`);

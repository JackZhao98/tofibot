"use strict";
// Producer belongs to the canonical Web build. The desktop consumer is independent.
const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const { execFileSync } = require("node:child_process");
const root = path.resolve(__dirname, "..");
const output = process.argv[2];
if (!output) throw new Error("Usage: node scripts/export-desktop-ui.cjs NEW_OUTPUT_DIRECTORY (after Web build)");
const destination = path.resolve(output);
if (fs.existsSync(destination)) throw new Error("Output must not exist");
const source = path.join(root, "ui", "dist");
function inventory(dir, prefix = "") {
  const result = {};
  for (const entry of fs.readdirSync(path.join(dir, prefix), { withFileTypes: true })) {
    const name = prefix ? `${prefix}/${entry.name}` : entry.name;
    if (entry.isDirectory()) Object.assign(result, inventory(dir, name));
    else if (entry.isFile()) result[name] = crypto.createHash("sha256").update(fs.readFileSync(path.join(dir, name))).digest("hex");
    else throw new Error("Non-regular UI build entry");
  }
  return result;
}
const revision = execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim();
if (execFileSync("git", ["status", "--porcelain", "--", "ui"], { cwd: root, encoding: "utf8" }).trim()) throw new Error("Web source must be committed before artifact export");
if (!fs.existsSync(path.join(source, "index.html"))) throw new Error("Build Web UI first");
fs.cpSync(source, destination, { recursive: true, errorOnExist: true, force: false });
fs.writeFileSync(path.join(destination, "ui-artifact.json"), JSON.stringify({ schema: 1, desktop_contract: 1, revision, files: inventory(destination) }, null, 2)+"\n");
console.log(destination);

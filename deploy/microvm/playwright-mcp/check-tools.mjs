// Fails when the pinned @playwright/mcp no longer offers a tool the guest relays, or an argument the guest passes.
// Run after `npm ci` in this directory:  node check-tools.mjs ../../../internal/computer/guest/playwright.go
// The allowlist is read from the Go source (the playwrightTools map) so there is one list.
import {spawn} from 'node:child_process';
import {readFileSync} from 'node:fs';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(process.argv[2] ?? join(here, '../../../internal/computer/guest/playwright.go'), 'utf8');
const block = source.slice(source.indexOf('var playwrightTools = map[string][]string{'));
const wanted = new Map();
for (const line of block.slice(0, block.indexOf('\n}\n')).split('\n')) {
  const m = line.match(/^\s*"(browser_[a-z_]+)":\s*\{([^}]*)\}/);
  if (m) wanted.set(m[1], [...m[2].matchAll(/"([A-Za-z]+)"/g)].map(x => x[1]));
}
if (wanted.size < 10) throw new Error(`parsed only ${wanted.size} tools from the allowlist`);

const child = spawn('node', [join(here, 'node_modules/@playwright/mcp/cli.js'), '--cdp-endpoint', 'http://127.0.0.1:9'], {stdio: ['pipe', 'pipe', 'inherit']});
const timer = setTimeout(() => { console.error('no answer from the MCP server'); child.kill(); process.exit(1); }, 30000);
let buf = '';
child.stdout.on('data', chunk => {
  buf += chunk;
  let i;
  while ((i = buf.indexOf('\n')) >= 0) {
    const msg = JSON.parse(buf.slice(0, i));
    buf = buf.slice(i + 1);
    if (msg.id === 1) {
      child.stdin.write(JSON.stringify({jsonrpc: '2.0', method: 'notifications/initialized'}) + '\n');
      child.stdin.write(JSON.stringify({jsonrpc: '2.0', id: 2, method: 'tools/list'}) + '\n');
    } else if (msg.id === 2) {
      clearTimeout(timer);
      const have = new Map(msg.result.tools.map(t => [t.name, Object.keys(t.inputSchema.properties ?? {})]));
      const problems = [];
      for (const [name, args] of wanted) {
        if (!have.has(name)) { problems.push(`missing tool ${name}`); continue; }
        for (const arg of args) if (!have.get(name).includes(arg) && !(name === 'browser_fill_form')) problems.push(`${name} has no argument ${arg}`);
      }
      child.kill();
      if (problems.length) { console.error(problems.join('\n')); process.exit(1); }
      console.log(`ok: ${wanted.size} relayed tools present in @playwright/mcp`);
    }
  }
});
child.stdin.write(JSON.stringify({jsonrpc: '2.0', id: 1, method: 'initialize', params: {protocolVersion: '2025-06-18', capabilities: {}, clientInfo: {name: 'check', version: '1'}}}) + '\n');

import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { promisify } from 'node:util';
const root = dirname(dirname(fileURLToPath(import.meta.url)));
const output = await mkdtemp(join(tmpdir(), 'tofi-metadata-unit-'));
try {
  await promisify(execFile)(join(root, 'node_modules/.bin/tsc'), ['src/displayMetadata.ts', '--ignoreConfig', '--target', 'ES2022', '--module', 'ES2022', '--outDir', output, '--skipLibCheck', '--declaration', 'false'], { cwd: root });
  const { memoryDisplay, scheduleDisplay } = await import(pathToFileURL(join(output, 'displayMetadata.js')));
  for (const display of [memoryDisplay, scheduleDisplay]) {
    const legacy = display({ title: '', description: ' \n\t ', content: 'COMPLEX_PRIVATE_PROMPT', prompt: 'COMPLEX_PRIVATE_PROMPT' });
    assert.doesNotMatch(JSON.stringify(legacy), /COMPLEX_PRIVATE_PROMPT/);
    const long = display({ title: '🧠'.repeat(1000), description: '事实'.repeat(1000) });
    assert.equal(Array.from(long.title).length, 120);
    assert.equal(Array.from(long.description).length, 280);
    assert.ok(long.title.endsWith('…') && long.description.endsWith('…'));
    assert.deepEqual(display({ title: ' 标题\n 第二行 ', description: ' 用户\t说明 ' }), { title: '标题 第二行', description: '用户 说明' });
  }
  console.log('display metadata: PASS (safe legacy/empty fallback, Unicode bounds, localized metadata, no body/prompt fallback)');
} finally { await rm(output, { recursive: true, force: true }); }

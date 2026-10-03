import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { promisify } from 'node:util';
const root = dirname(dirname(fileURLToPath(import.meta.url)));
const output = await mkdtemp(join(tmpdir(), 'tofi-edit-baseline-'));
try {
  await promisify(execFile)(join(root, 'node_modules/.bin/tsc'), ['src/contentEditPatch.ts', '--ignoreConfig', '--target', 'ES2022', '--module', 'ES2022', '--outDir', output, '--skipLibCheck', '--declaration', 'false'], { cwd: root });
  const { contentEditPatch } = await import(pathToFileURL(join(output, 'contentEditPatch.js')));
  const baseline = { title: '原标题', description: '原说明', content: '原始事实 / Original English prompt.' };
  assert.equal(contentEditPatch(baseline, { ...baseline }), undefined);
  for (const key of ['title', 'description', 'content']) {
    assert.deepEqual(contentEditPatch(baseline, { ...baseline, [key]: 'new value' }), { [key]: 'new value', expected: { [key]: baseline[key] } });
  }
  const draft = { ...baseline, content: 'changed' };
  draft.content = baseline.content; draft.title = 'new title';
  assert.deepEqual(contentEditPatch(baseline, draft), { title: 'new title', expected: { title: baseline.title } }, 'changed-then-restored content stays omitted');
  assert.deepEqual(contentEditPatch(baseline, { ...baseline, title: '' }), { title: '', expected: { title: baseline.title } }, 'explicit clear preserves its baseline');
  assert.deepEqual(baseline, { title: '原标题', description: '原说明', content: '原始事实 / Original English prompt.' });
  console.log('edit baselines: PASS (changed fields only, exact expected values, restored/no-op fields omitted, explicit clear, baseline immutable)');
} finally { await rm(output, { recursive: true, force: true }); }

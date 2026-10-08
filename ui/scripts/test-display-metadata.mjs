import assert from 'node:assert/strict';
import { openUiModules } from './ui-modules.mjs';
// Load through Vite so the module's i18n catalogs resolve; assertions pin the shipped zh-CN copy.
const ui = await openUiModules({ language: 'zh-CN' });
try {
  const { memoryDisplay, scheduleDisplay } = await ui.load('/src/displayMetadata.ts');
  assert.deepEqual(memoryDisplay({}), { title: '记忆', description: '已保存的记忆；展开查看完整内容。' });
  assert.deepEqual(scheduleDisplay(undefined), { title: '定时任务', description: '按计划执行的任务；在管理中查看完整指令。' });
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
  await ui.setLanguage('en');
  assert.equal(scheduleDisplay().title, 'Scheduled task');
  await ui.setLanguage('zh-CN');
} finally { await ui.close(); }

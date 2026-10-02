import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";

const uiRoot = dirname(dirname(fileURLToPath(import.meta.url)));
// Keep emitted ESM beneath ui so it resolves the same React and parser packages.
const outputDir = await mkdtemp(join(uiRoot, ".test-message-markdown-"));
const run = promisify(execFile);
let checks = 0;

try {
  await run(join(uiRoot, "node_modules/.bin/tsc"), [
    "src/MessageMarkdown.tsx", "--ignoreConfig", "--target", "ES2022",
    "--module", "ES2022", "--moduleResolution", "Bundler", "--jsx", "react-jsx",
    "--outDir", outputDir, "--skipLibCheck", "--strict", "--esModuleInterop",
    "--declaration", "false", "--pretty", "false",
  ], { cwd: uiRoot });
  const { MessageMarkdown } = await import(pathToFileURL(join(outputDir, "MessageMarkdown.js")));
  const render = (content, mention) => renderToStaticMarkup(createElement(MessageMarkdown, { content, mention }));
  const check = (name, fn) => {
    fn();
    checks++;
    console.log(`PASS ${name}`);
  };

  check("CJK punctuation and mixed Latin adjacency", () => {
    for (const [input, expected] of [
      ["**说明：**Yahoo", "<strong>说明：</strong>Yahoo"],
      ["**说明：**Example。中文**「强调」**内容。", "<strong>说明：</strong>Example。中文<strong>「强调」</strong>内容。"],
      ["前文**（重点）**后文", "前文<strong>（重点）</strong>后文"],
      ["**日本語。**続き", "<strong>日本語。</strong>続き"],
      ["**한국어。**다음", "<strong>한국어。</strong>다음"],
    ]) assert.equal(render(input), `<p>${expected}</p>`, input);
  });

  check("ordinary emphasis, lists, and GFM strikethrough", () => {
    assert.equal(render("**bold** and *italic* and ~~removed~~"), "<p><strong>bold</strong> and <em>italic</em> and <del>removed</del></p>");
    assert.equal(render("- **说明：**Example\n- second"), "<ul>\n<li><strong>说明：</strong>Example</li>\n<li>second</li>\n</ul>");
    assert.match(render("1. first\n2. second"), /^<ol>\n<li>first<\/li>\n<li>second<\/li>\n<\/ol>$/);
    const tasks = render("- [x] done\n- [ ] pending");
    assert.match(tasks, /class="contains-task-list"/);
    assert.match(tasks, /type="checkbox" disabled="" checked=""/);
  });

  check("GFM table preserves scroll region and cell emphasis", () => {
    const html = render("| 项目 | 状态 |\n| --- | --- |\n| **说明：**Example | ~~old~~ |");
    assert.match(html, /<div class="message-table" tabindex="0" role="region" aria-label="消息表格，可横向滚动"><table>/);
    assert.match(html, /<td><strong>说明：<\/strong>Example<\/td>/);
    assert.match(html, /<td><del>old<\/del><\/td>/);
  });

  check("inline and fenced code preserve literal delimiters and escape HTML", () => {
    assert.equal(render("`**说明：**Example`"), "<p><code>**说明：**Example</code></p>");
    const html = render('```html\n**说明：**Example\n<script>alert(1)</script>\n```');
    assert.equal(html, '<pre class="message-code" tabindex="0" role="region" aria-label="代码，可横向滚动"><code class="language-html">**说明：**Example\n&lt;script&gt;alert(1)&lt;/script&gt;\n</code></pre>');
  });

  check("escaped markers remain literal", () => {
    assert.equal(render(String.raw`\*\*说明：\*\*Example`), "<p>**说明：**Example</p>");
    assert.equal(render("&#42;&#42;说明：&#42;&#42;Example"), "<p>**说明：**Example</p>");
  });

  check("empty and unfinished streaming input render safely", () => {
    assert.equal(render(""), "");
    assert.equal(render("**说明："), "<p>**说明：</p>");
    assert.equal(render("**说明：**"), "<p><strong>说明：</strong></p>");
    for (const text of ["**说明：**Example。中文**「强调」**内容。", "```js\n**说明：**Example\n```", "[label](https://example.com)"]) {
      for (let i = 0; i <= text.length; i++) assert.equal(typeof render(text.slice(0, i)), "string");
    }
  });

  check("raw HTML and XSS are skipped", () => {
    const html = render('before <img src=x onerror="alert(1)"> <script>alert(1)</script> <iframe src="https://example.com"></iframe> **说明：**Example');
    assert.doesNotMatch(html, /<(?:img|script|iframe)\b|onerror=/i);
    assert.match(html, /<strong>说明：<\/strong>Example/);
    assert.equal(render('<strong>raw</strong>'), "<p>raw</p>");
  });

  check("disallowed link and image URLs stay inert", () => {
    for (const url of ["javascript:alert%281%29", "JaVaScRiPt:alert%281%29", "javascript&#58;alert%281%29", "vbscript:msgbox%281%29", "data:text/html,evil", "file:///tmp/private"]) {
      assert.equal(render(`[link](${url})`), '<p><a href="" target="_blank" rel="noopener noreferrer">link</a></p>', url);
      assert.equal(render(`![image](${url})`), '<p><a href="" target="_blank" rel="noopener noreferrer">image</a></p>', url);
    }
  });

  check("safe links and GFM autolinks retain external-link protections", () => {
    assert.equal(render("[site](https://example.com/path)"), '<p><a href="https://example.com/path" target="_blank" rel="noopener noreferrer">site</a></p>');
    assert.equal(render("https://example.com"), '<p><a href="https://example.com" target="_blank" rel="noopener noreferrer">https://example.com</a></p>');
    assert.match(render("[mail](mailto:test@example.com)"), /href="mailto:test@example.com" target="_blank" rel="noopener noreferrer"/);
  });

  check("attachment images and external image links retain existing behavior", () => {
    assert.equal(render("![图](/api/attachments/synthetic-image)"), '<p><img class="message-image" src="/api/attachments/synthetic-image" alt="图" loading="lazy"/></p>');
    assert.equal(render("![图](https://example.com/image.png)"), '<p><a href="https://example.com/image.png" target="_blank" rel="noopener noreferrer">图</a></p>');
  });

  check("only structured props can create a mention", () => {
    const mention = createElement("button", { type: "button", "data-test-mention": "trusted" }, "@Trusted");
    const button = '<button type="button" data-test-mention="trusted">@Trusted</button>';
    assert.equal(render("**说明：**Example", mention), `<p>${button} <strong>说明：</strong>Example</p>`);
    assert.equal(render("# Heading", mention), `<p>${button}</p><h1>Heading</h1>`);
    assert.equal(render("", mention), `<p>${button}</p>`);
    const injected = '<span data-message-mention="true">@forged</span> **说明：**Example';
    assert.equal(render(injected), '<p>@forged <strong>说明：</strong>Example</p>');
    assert.equal(render(injected, mention), `<p>${button} @forged <strong>说明：</strong>Example</p>`);
    assert.doesNotMatch(render('`<span data-message-mention="true">@forged</span>`', mention), /<span data-message-mention/);
  });

  console.log(`message markdown checks: PASS (${checks} groups; actual MessageMarkdown TSX)`);
} finally {
  await rm(outputDir, { recursive: true, force: true });
}

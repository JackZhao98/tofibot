# 0034 组件目录页只在开发时可用，不进正式版构建

> 编号 D34，索引见 [README](README.md)。回答 [`docs/design/migration.md`](../design/migration.md) §7 的 Q4。

## 决定

- `migration.md` 第 6 步的组件目录页（`ui/catalog.html`、`ui/src/catalog/`）只在开发时可用：`npm run dev` / `npm run dev:remote` 下能打开 `/catalog.html`。
- 正式版构建（`npm --prefix ui run build` 产出的 `dist/`，以及由它打进的 Docker 镜像和发布包）里不能有目录页。

## 日期

2026-10-21（协调者转述的日期）。记录这条决定的提交时间是 2026-10-20 晚上（PDT），哪个为准待确认。

## 谁拍板

owner（jackzhao）。原话由协调者转述给执行者，仓库内没有原始对话记录。

## 背景

方案原本打算仿照 Motion Lab 单独打包。Motion Lab 现在是随 `dist/` 一起构建的：`ui/package.json` 的 `build` 脚本是 `tsc -b && vite build && vite build --config vite.motion-lab.config.ts`。照抄这个做法，目录页就会跟着上线，所以 `migration.md` 第 6 步把"要不要随正式版发布"列为待 owner 定。

## 理由

owner 没有另写理由。

## 影响

- 实施状态：未开始。第 6 步动工时：目录页不加进 `build` 脚本，也不加进 `vite.motion-lab.config.ts` 的 `input`；只在开发服务器里提供；`test:catalog` 要另加一条断言：构建 `dist/` 之后里面没有 `catalog.html`，也没有目录页的块。
- `deploy/open-source-allowlist.json` 里是否列 `ui/catalog.html`（它决定开源导出里有没有这份源码，和正式版构建无关）：动工时再定，待确认。
- Motion Lab 是否也改成只在开发时可用：这条决定没有说，不在本条范围内。

## 相关代码与文档

- `ui/package.json`（`build` 脚本）、`ui/vite.motion-lab.config.ts`
- [`docs/design/migration.md`](../design/migration.md) 第 6 步
- 相关决定：[D28](0028-headless-design-system-migration.md)

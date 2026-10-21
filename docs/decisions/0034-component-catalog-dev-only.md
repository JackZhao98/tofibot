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

- 实施状态：**已实施**（迁移第 6 步，分支 `ds/step6-catalog`，等合并）。目录页不在 `build` 脚本里，也不在 `vite.motion-lab.config.ts` 的 `input` 里，只由开发服务器提供；`main.tsx` 在生产模式下直接抛错。`npm --prefix ui run test:catalog` 按 `build` 脚本把每个 `vite build` 跑一遍，断言产物里没有 `catalog.html`、没有带目录页标记的块、没有 `src/catalog/` 路径，并断言产品代码不 import 目录页；撤回验证见 [`migration.md`](../design/migration.md) 第 6 步。
- `deploy/open-source-allowlist.json` 列入 `ui/catalog.html`：开源导出带上目录页的源码（`ui/src/` 本来就整个导出，只列 `src/catalog/` 而漏掉入口页面会导出一份打不开的目录页）。这和正式版构建无关，正式版里仍然没有它。
- Motion Lab 是否也改成只在开发时可用：这条决定没有说，不在本条范围内。

## 相关代码与文档

- `ui/package.json`（`build` 脚本）、`ui/vite.motion-lab.config.ts`
- [`docs/design/migration.md`](../design/migration.md) 第 6 步
- 相关决定：[D28](0028-headless-design-system-migration.md)

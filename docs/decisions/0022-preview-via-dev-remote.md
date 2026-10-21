# 0022 界面预览用 dev:remote 连生产，不再每个小改动发一个 rc

> 编号 D22，索引见 [README](README.md)。

## 决定

- 预览界面改动时，用本地 Vite 开发服务器把 `/api` 代理到生产服务器（`npm run dev:remote`），在真实数据上看效果，改动即时热更新。
- 需要在局域网的其他设备（例如手机）上看时，开发服务器可以改用 HTTPS。
- 不再为每个小改动单独发一个 rc，rc 攒一批再发。

## 日期

2026-10-20（Pacific）

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

之前每次界面改动都要构建、打 rc、部署之后才能看到效果，一次要很久。

## 理由

- 改动要尽快到 owner 眼前；部署只在攒够一批改动之后做。
- Safari 在纯 http 的 localhost 上不保留 Secure 会话 cookie，所以文档要求用 Chrome，或者开局域网 HTTPS（`AGENTS.md` “Fast UI preview against a running server”）。

## 影响

- 实施状态：已实施：`e6aa7b1`（`npm run dev:remote`）、`55fb70d`（局域网 HTTPS）
- 首个仍存在的含此改动的标签：`dev:remote` v0.1.4-rc.27；局域网 HTTPS v0.1.4-rc.28（更早的 rc 标签已按 [D25](0025-github-release-cleanup.md) 清理）
- 命令：`cd ui && TOFI_DEV_API_ORIGIN=https://<server>:8321 npm run dev:remote`，然后打开 http://localhost:5173。`dev:remote` 等于 `TOFI_DEV_API_INSECURE=1 vite`，会接受服务器默认的自签名证书（`ui/package.json:9`）。
- 局域网 HTTPS：设置 `TOFI_DEV_HTTPS_CERT` 和 `TOFI_DEV_HTTPS_KEY`（`ui/vite.config.ts:11-12`）。
- 后端改动仍然要构建并部署，预览只能看前端。
- 发版节奏见 [`docs/process.md`](../process.md) 第 7.1 节（攒一批再发 rc），预览做法见第 6 节。参考：rc.26 到 rc.29 都在 2026-10-20（PDT）发出（`gh release list`）。

## 相关代码与文档

- `ui/vite.config.ts`、`ui/package.json`
- `AGENTS.md` “Fast UI preview against a running server”

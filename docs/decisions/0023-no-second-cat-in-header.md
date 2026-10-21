# 0023 顶栏不放第二只猫

> 编号 D23，索引见 [README](README.md)。

## 决定

聊天顶栏不再放一只表示谁在工作的猫。输入框上方停着的那只猫已经能看出是谁在工作。

## 日期

2026-10-20（Pacific）

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

单聊时，输入框上方有一只停着的猫（`ComposerPerch`）。发送时它会“看”，Bot 工作时它会“干活”，回复到达时它会“开心”（`ui/src/App.tsx:3156-3171`）。顶栏里已经有成员头像（`member-avatar-stack`，`ui/src/App.tsx:2477`）。

## 理由

同一个信息出现两次是多余的，而且顶栏的控件要保持低调。owner 原话的完整理由待确认。

## 影响

- 实施状态：规则。没找到对应的“删掉顶栏猫”的提交；这条可能是否决了一个提议（待确认）
- 以后不要在 `.chat-header` 里加第二个会动的工作状态猫。
- 顶栏里的 mini 头像（`Avatar` 带 `motion` 参数）算不算“猫”，这条决定是否覆盖它：待确认。

## 相关代码与文档

- `ui/src/App.tsx`（`ComposerPerch`、`.chat-header`）、`ui/src/CatStage.tsx`、`ui/src/chat-header.css`

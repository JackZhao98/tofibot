# 0033 手机上做 iOS 式的右滑返回手势：先做有门槛的对照实验，自研和 Motion 各一版，留好的那个

> 编号 D33，索引见 [README](README.md)。回答 [`docs/design/migration.md`](../design/migration.md) §7 的 Q3。

## 决定

- 要做：手机宽度下，手指按住对话往右拖，就回到会话列表（iOS 式的返回手势）。
- 做成有门槛的实验，做两版：一版自研（直接用 Pointer Events + WAAPI / CSS），一版用 Motion（命令式 `animate()`，懒加载）。按同一组门槛比较，留更好的那一版。
- 这决定了 `migration.md` 第 4 步实验②要做。

## 日期

2026-10-21（协调者转述的日期）。记录这条决定的提交时间是 2026-10-20 晚上（PDT），哪个为准待确认。

## 谁拍板

owner（jackzhao）。原话由协调者转述给执行者，仓库内没有原始对话记录。

## 背景

- 现在手机上只有点顶栏返回按钮一种方式回到列表，推入动画是 CSS transition（进 280 ms、退 220 ms，[D20](0020-phone-push-and-boot-screen.md)，`ui/src/styles.css` 的 `max-width:767px` 一段）。
- `ui/src` 里 grep `swipe` 没有结果（motion-lab 除外），`components.md` 把它记为问题 C38。
- 方案的判断是：Motion 真正比现在的 CSS transition 强的地方，只有"手指拖着走、松手后按速度用弹簧收尾"这一种情况（`migration.md` §3.2）。

## 理由

owner 没有另写理由。iOS 用户的肌肉记忆是从左边缘右滑返回（C38，原则 P5"用业界通行的做法"）。两版对照是因为 Motion 的命令式 `animate()` 懒加载块约 22.8 KB（`migration.md` §2.3），要证明它比自研的好才值得。

## 影响

- 实施状态：未开始。属于第 4 步，和第 1–3 步互不依赖（`migration.md` §6）。
- 门槛沿用第 4 步：帧预算不差于现有（`test:shell-budget` 4x CPU 一节）、中途打断有逐帧位置记录作证、减弱动效时立即到位、入口包不超预算（[D31](0031-entry-budget-plan-a.md)）、owner 在预览里看过。新测试：390 px 触摸，拖过一半松手完成返回，不到一半弹回。
- 点按返回仍然走现有的 CSS transition，`test:shell-budget` 第 3 节对 `transitionend` 时长的断言不放松。

## 相关代码与文档

- `ui/src/styles.css`（手机推入）、`ui/src/App.tsx`（`mobileList`）、`ui/scripts/test-shell-budget.mjs`
- [`docs/design/migration.md`](../design/migration.md) §3.2、第 4 步；[`docs/design/components.md`](../design/components.md) 第 21 节 C38
- 相关决定：[D20](0020-phone-push-and-boot-screen.md)、[D28](0028-headless-design-system-migration.md)、[D31](0031-entry-budget-plan-a.md)

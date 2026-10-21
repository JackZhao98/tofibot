# 0035 迁移第 4 步的结果：不引入 Motion；手机右滑返回用自研的 Pointer Events + Web Animations 版

> 编号 D35，索引见 [README](README.md)。执行 [D33](0033-phone-swipe-back-experiment.md)（"自研和 Motion 各做一版，留好的那个"）和 [`docs/design/migration.md`](../design/migration.md) 第 4 步的门槛。

## 决定

- **到达级联、手机推入（点按）、布局过渡：保留现有代码，不引入 Motion。**
  - 级联：Motion 版要在发消息的同一帧里同步启动，所以必须进入口包，实测入口 +22,734 B（338,091 B，预算 315,805 B），直接不过门槛；中途打断也没有更顺（下表）。
  - 点按推入：保留 CSS transition（D33 已定），`test:shell-budget` 第 3 节的 280 / 220 ms 断言不变。
  - 布局过渡（`AnimatePresence`、`layout`）：要 React 组件版的 Motion，必须进入口包（≥30.8 KB，`migration.md` §2.3），方案 A（[D31](0031-entry-budget-plan-a.md)）下不做。
- **手机右滑返回：上线自研版**（`ui/src/phoneSwipeGesture.ts` 手势核心 + `ui/src/phoneSwipeEngine.ts` Web Animations 收尾），只在手机布局（≤767 px）、只认触摸和触控笔。Motion 版（`ui/src/motion-lab/experiments/phoneSwipeMotion.ts`）留在实验目录里，不进任何构建。
- 手势规则：过半松手完成；不到一半弹回；向右快甩（≥0.5 px/ms，即 500 px/s）从任何位置都完成；向左快甩从任何位置都取消；拖到起点左边有橡皮筋阻力，最多让出宽度的 10%；松手时带着手指的速度走（临界阻尼弹簧 `SPRING.push`，不会冲过边缘）；收尾途中手指再按上去，会在当前位置接住；减弱动效时仍跟手，松手立即到位。
- `motion@14.4.0` 只作为 devDependency 留着，给 `npm run test:motion-experiment` 复现这次对照，以及 `TOFI_MOTION_EXPERIMENT=motion npm run dev:remote` 让 owner 在手机上体验 Motion 版；正式构建里没有它（`test:swipe-back` 和 `test:motion-experiment` 都断言）。

## 日期

2026-10-21（PDT）。

## 谁拍板

执行者按 D33 的授权（"留好的那个"）和 `migration.md` 第 4 步写好的门槛选定；门槛里的最后一条"owner 在预览里看过并认可"还没满足，见"影响"。

## 背景

- D33：owner 要 iOS 式的右滑返回，做成有门槛的实验，自研和 Motion 各一版。
- `migration.md` 第 4 步的门槛（全部满足才进生产）：帧预算不差于现有（`test:shell-budget` 4x CPU 一节"超过 25 ms 的帧 ≤ 1"）；中途打断比现有更顺，有逐帧位置记录作证；减弱动效时立即到位；入口包不超预算；owner 在预览里看过并认可。

## 理由

两版共用同一个手势核心（判定、跟手、橡皮筋、接住、提交），只有"松手以后怎么走"不同，所以差别只来自 Motion 本身。数字来自 `npm run test:motion-experiment`（每项 5 次取中位数，两版交替测；Chromium 有 CDP 计时，WebKit 没有）；00:56 PDT 机器空闲时又单跑了一次，松手后的主线程脚本（1.8 / 7.1 对 4.3 / 11.7 ms）和 WebKit 接住时的跳动都是同样的顺序。测量时同一台机器上还有另外四个分支在跑浏览器测试（负载平均值 8–34），绝对值偏高，两版之间的对比仍然有效。

| 指标 | 自研（Web Animations） | Motion 14.4.0 | 谁好 |
|---|---|---|---|
| 懒加载块（gzip） | 2,079 B | 24,099 B（+22,020 B） | 自研 |
| 入口包 | 315,357 B（两版相同；比第 1 步 +244 B，是加载桩和手势钩子） | 315,356 B | 相同 |
| 拖动中每次手指移动的脚本（1x / 4x） | 0.69 / 2.64 ms | 0.58 / 1.73 ms | 相同代码，差别是噪声 |
| 松手到提交的主线程脚本（1x / 4x） | 3.9 / 17.1 ms | 7.8 / 20.0 ms | 自研（Motion 每帧在主线程算弹簧、写样式） |
| 4x 下超过 25 ms 的帧 | 2（最差 33 ms） | 2（最差 33 ms） | 相同（机器负载下；空闲时自研测到 0–1） |
| 松手后两帧的速度 / 手指速度（Chromium / WebKit） | 0.87 / 1.23 | 0.77 / 0.66 | 中位数上自研好，但这项不稳：机器空闲时单跑一次，Chromium 反过来（0.83 对 1.09） |
| 收尾时间 | 300 ms | 300 ms（Chromium 一次 317） | 相同（同一个弹簧） |
| 半路接住那一帧的位移，对照前三帧最大位移（Chromium / WebKit） | 0 / 30.3 px，7.5 / 29.9 px | 20.9 / 30.0 px，50.6 / 24.6 px | 自研：Motion 在 WebKit 接住时跳了正常速度的 2.1 倍（空闲时单跑一次是 50.8 对 14.4 px，3.5 倍），超过方案"不超过 2 倍"的线 |

到达级联（1180×1150，一屏十几条短消息；Motion 两种用法："waapi" 动 `transform` 字符串，走合成线程但带不了速度；"js" 动 `y`，能带速度但每帧在主线程跑）：

| 指标 | 现有 | Motion waapi | Motion js |
|---|---|---|---|
| 入口包 | 315,357 B | 338,091 B（+22,734 B，**超预算**） | 同左 |
| 单条到达 1x：最差帧 / 过冲 / 落定 | 16.8 ms / 3.5 px / 223 ms | 16.8 ms / 3.5 px / 238 ms | 16.8 ms / 3.6 px / 234 ms |
| 单条到达 4x：最差帧 | 33 ms | 50 ms | 50 ms |
| 级联中又来一条（1x）：被打断那条消息的速度变化 | 0.72 px/ms | 0.73 px/ms | 0.71 px/ms |
| 同上，WebKit | 0.79 px/ms（变化/原速 0.80） | 0.66 px/ms（0.83） | 0.54 px/ms（0.89） |

三种做法在打断时都丢掉了约 80–90% 的速度，Motion 的"带速度打断"在这个场景里看不出来；而入口包一项就不过门槛。

另外核对了方案里待确认的一条：Motion 14.4.0 只把 `opacity`、`clipPath`、`filter`、`transform`、`backgroundColor` 交给 Web Animations（`motion-dom/dist/es/animation/waapi/utils/accelerated-values.mjs`），设了 `onUpdate` 时一律不交（`.../waapi/supports/waapi.mjs`）。所以 `translate`、`x`、`y` 和带速度的手势收尾都在主线程每帧跑。

## 影响

- 实施状态：分支 `ds/step4-motion`（从 `ds/step1-layers` 开出），已合进集成分支 `ds/integrate`，等 owner 预览和合并进 `main`。owner 预览后如果觉得 Motion 版手感更好，可以推翻本条：换回只要把 `phoneSwipeMotion.ts` 挪进 `src/` 并替换 `phoneSwipeEngine.ts` 的 `mount`，`motion` 改成 dependency，同时在 `ui/public/licenses/` 加许可证。
- 新增：`ui/src/phoneSwipeGesture.ts`、`ui/src/phoneSwipeEngine.ts`、`ui/src/usePhoneSwipeBack.ts`；`ui/src/App.tsx` 一行调用；`ui/src/styles.css` 手机段的 `touch-action` 和 `data-swipe` 规则；`ui/src/motion.ts` 新令牌 `SPRING.push`（临界阻尼，`test:motion-tokens` 守着）。
- 新测试：`npm --prefix ui run test:swipe-back`（Chromium 用 CDP 真触摸；`SWIPE_BACK_ENGINE=webkit` 用页面内合成的触摸 PointerEvent）；`npm --prefix ui run test:motion-experiment`（复现上面的表）。
- 不变：点按推入的 280 / 220 ms（`test:shell-budget` 第 3 节）；到达级联（`test:arrival-glide`）。
- 待确认：手机浏览器（Safari 非主屏模式）自己的左边缘返回手势会和这个手势抢同一下滑动，浏览器抢到时页面收到 `pointercancel`，对话弹回原位；浏览器"后退"在手机上回到哪里，仍是 `components.md` C38 的待确认项。

## 相关代码与文档

- `ui/src/phoneSwipeGesture.ts`、`ui/src/phoneSwipeEngine.ts`、`ui/src/usePhoneSwipeBack.ts`、`ui/src/motion.ts`（`SPRING`）、`ui/src/styles.css`（`max-width:767px` 一段）
- 实验：`ui/src/motion-lab/experiments/phoneSwipeMotion.ts`、`ui/src/motion-lab/experiments/arrivalCascadeMotion.ts`、`ui/motionExperimentPlugin.ts`、`ui/scripts/test-motion-experiment.mjs`、`ui/scripts/lib/swipe-harness.mjs`
- [`docs/design/migration.md`](../design/migration.md) 第 4 步；[`docs/design/components.md`](../design/components.md) 第 21 节；[`docs/design/tokens.md`](../design/tokens.md) §8
- 相关决定：[D19](0019-arrival-spring-cascade.md)、[D20](0020-phone-push-and-boot-screen.md)、[D28](0028-headless-design-system-migration.md)、[D31](0031-entry-budget-plan-a.md)、[D33](0033-phone-swipe-back-experiment.md)

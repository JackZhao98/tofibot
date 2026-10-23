# Logo 待办（owner 定）

owner 2026-10-22：外形用原版不改，只换颜色。设计稿三轮都在 `docs/design/logo-refine/`（round3 是配色）。等 owner 在新对话里拍板后实施。

## 待 owner 选
1. **主色**：(a) 对齐界面主色 `--clay #e07f55`、深色 `#ec9a74`（Fable 推荐，只改一个 fill，消除 logo 与发送键“两种橙”）｜(b) 保留现在 `#D27656`｜(c) 深柿子 `#c4502f`（浅底对比唯一达标，但要连 `--clay` token 一起动）｜(d) 墨黑｜(e) 其他。
2. **App 图标底**：(a) 深墨底配陶土（Fable 推荐，深浅一致）｜(b) 陶土底配奶白猫｜(c) 纸底配陶土｜(d) 蜂蜜底配墨色｜(e) 三花。
3. **眼睛**：(a) 保持镂空（推荐）｜(b) 双色，眼睛填 `--clay-text`。
4. **深色模式 logo**：(a) 跟 `--clay` 变浅（推荐）｜(b) 两种主题都用同一固定色。

## 实施（选定后）
- 改 `ui/public/brand/tofi-logo-color.svg` 的 `fill` + `BrandLogo.tsx` 的缓存参数；深色用 `-dark.svg` 或 `fill="currentColor"` + CSS `--clay`。
- 加 `tofi-appicon-{light,dark}.svg`。
- 不动界面语义色 token（蜂蜜/青/鸢尾各有含义，撞色）。
- 设计稿分支 `design/logo-refine`（未推）；选定后另起实施分支。

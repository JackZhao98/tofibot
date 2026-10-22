# 决策记录（Decisions）

这里记录 owner（jackzhao）拍板的每一条决定，一条一个文件。每个文件写明：决定了什么、什么时候定的、谁拍板、背景、理由、影响到哪些代码，以及相关代码与文档。

日期统一用美国太平洋时间（PDT）。

- D24–D29 的日期在整理时记为 10-21，但对应的提交和 GitHub 时间戳换算成 PDT 是 2026-10-20 晚上。已有提交的条目在各自文件里写明了提交时间。哪个日期为准待确认。D31–D34 同样：协调者转述的日期是 10-21，落地提交在 10-20 晚上（PDT）。
- 文件里写“待确认”的地方，是仓库里找不到证据的内容，不是推测。

## 全部决定

| 编号 | 标题 | 日期 | 一句话 | 文件 |
| --- | --- | --- | --- | --- |
| D01 | 人设由生成器负责，提示词不放例句 | 2026-10-15 | 人设有毛病就修生成器（onboarding / `set_bot_profile`），不修单个 Bot；提示词里不放例句、Not/But 对照和关键词清单 | [0001](0001-persona-generator-no-examples.md) |
| D02 | 界面改动要量交互开销 | 2026-10-15 | 起因是输入框每次按键 37 ms、触发 66 次排版；预算写成会失败的测试，例如 `test:composer-typing` | [0002](0002-measure-interaction-cost.md) |
| D03 | 改完立刻用真实对话测 | 2026-10-16 | 立刻造真实对话去测，亲自读回复判断是否自然，不等排期 | [0003](0003-test-live-immediately.md) |
| D04 | 结构化输出：`say` 工具 | 2026-10-17/18 | 一个 `say` 工具（answer / note / message）；工具调用之间的文字是内部的；模型没按协议走时发最后一段文字；工作区开关默认打开 | [0004](0004-structured-output-say.md) |
| D05 | 每个被 @ 的 Bot 都回答 | 2026-10-19 | 按被提到的顺序依次回答，第一个 Bot 不再负责协调其他 Bot | [0005](0005-every-mentioned-bot-answers.md) |
| D06 | 错过的群聊交流以摘要送达 | 2026-10-19 | 已经结束的群聊交流，以摘要形式送给错过的 Bot，不重新挑起旧话题 | [0006](0006-closed-group-exchange-gist.md) |
| D07 | 职责范围不是拒绝的理由 | 2026-10-19 | 范围管的是责任和主动性；闲聊照常说 | [0007](0007-scope-is-not-a-refusal.md) |
| D08 | 内部文字不进聊天 | 2026-10-19 | 工具调用之间的文字永远不在聊天里显示 | [0008](0008-internal-text-never-in-chat.md) |
| D09 | 可靠性原则 | 2026-10-19 | 写明假设后直接交付，不用提问收尾；“写”就是交出成稿，发送由人自己做；群里只给最终合并结果；声称“已核实”或“最新”必须在本次 run 里查过；不认识的名字先查再问 | [0009](0009-reliability-principles.md) |
| D10 | 人工输入统一走一个汇合点 | 2026-10-19 | 所有请人输入的方式统一到 questions 汇合点；私密输入也并入；人看不到的对话拒绝人工输入卡片 | [0010](0010-one-human-input-rendezvous.md) |
| D11 | 接管卡片 | 2026-10-19 | Bot 在聊天里请人接管屏幕（登录、2FA、验证码），或请人连接服务 | [0011](0011-handover-cards.md) |
| D12 | 非必要不用 Opus | 2026-10-19/20 | 评测里不用 Opus 当 Bot 模型；子代理写代码默认用 Sonnet，收集信息用 Haiku | [0012](0012-no-opus-unless-needed.md) |
| D13 | 用全新的 Bot 和群来测 | 2026-10-20 | 不用已经被旧对话污染的 Bot 和群 | [0013](0013-test-with-fresh-bots.md) |
| D14 | 模型分工：对话用 Sonnet，后台轻活用 Haiku | 2026-10-20 | 对话和所有 agentic 工作默认用 Sonnet；摘要、分诊、审查等后台轻活用 Haiku；run 中途不切换模型 | [0014](0014-sonnet-conversation-haiku-background.md) |
| D15 | 自动行为可见、可调 | 2026-10-20 | 每个自动行为都有可见、可调的设置，并带出厂默认值（Settings › 自动行为） | [0015](0015-automatic-behaviour-settings.md) |
| D16 | 乐观发送与回执 | 2026-10-20 | 回车后消息立即出现；状态为发送中、✓ 已送达、✓✓ 已读（Bot 开始处理）、未发送 · 重试；送达和已读只在操作行里出现 | [0016](0016-optimistic-send-receipts.md) |
| D17 | 消息操作行在消息下方 | 2026-10-20 | 无容器的低调图标行，消息间距加大、预留位置，不压住任何内容 | [0017](0017-quiet-message-action-row.md) |
| D18 | 渐变模糊的范围 | 2026-10-20 | 原则上给 Electron 和 iOS 用；网页端例外：消息窗格底部用真渐变模糊，顶栏用透明毛玻璃；新消息按钮不能被盖住 | [0018](0018-progressive-blur-scope.md) |
| D19 | 新消息弹簧链式联动 | 2026-10-20 | 从下往上约 15 ms 错开、越位 2–4 px、约 400 ms；流式输出时只轻柔跟随；减少动态效果时淡入 | [0019](0019-arrival-spring-cascade.md) |
| D20 | 手机端 iOS 式推入与模糊启动屏 | 2026-10-20 | 列表和聊天之间用推入动画；启动屏是模糊骨架加呼吸的灰色 logo；登录页和启动屏居中 | [0020](0020-phone-push-and-boot-screen.md) |
| D21 | 侧边栏拖动排序与置顶 | 2026-10-20 | 顺序存在服务端；有置顶放置区；置顶显示为大猫咪方块 | [0021](0021-sidebar-reorder-and-pins.md) |
| D22 | 用 dev:remote 预览，rc 攒一批再发 | 2026-10-20 | `npm run dev:remote` 连生产，可开局域网 HTTPS；不再每个小改动发一个 rc | [0022](0022-preview-via-dev-remote.md) |
| D23 | 顶栏不放第二只猫 | 2026-10-20 | 输入框上方的猫已经能看出谁在工作 | [0023](0023-no-second-cat-in-header.md) |
| D24 | “始终允许”长期授权 | 2026-10-21（提交在 10-20 PDT） | 一个 Bot 对一个写操作工具；绑定工具定义和服务器身份；高风险工具和人看不到的对话不能授权；可撤销，有审计 | [0024](0024-standing-tool-allowance.md) |
| D25 | GitHub 发布清理 | 2026-10-21 | 保留 4 个正式版和最近的 rc；删除旧 rc 的 Release、tag 和镜像版本 | [0025](0025-github-release-cleanup.md) |
| D26 | vite 8.3.3 | 2026-10-21（提交在 10-20 PDT） | 修复 Dependabot #15–17（launch-editor） | [0026](0026-vite-8-3-3-dependabot.md) |
| D27 | 文档就是系统 | 2026-10-21 | 决定、现状、流程、设计规范都写进 `docs/`，随每次合并、发版和决定同步更新；`AGENTS.md` 是入口 | [0027](0027-docs-are-the-system.md) |
| D28 | headless 设计系统迁移 | 2026-10-21 | 候选 Radix、Motion、dnd-kit，保留 TOFI 的外观；先出方案，再带预览一步步迁移 | [0028](0028-headless-design-system-migration.md) |
| D29 | 改账户密码不急 | 2026-10-21 | 暂缓 | [0029](0029-password-change-not-urgent.md) |
| D30 | 长期操作规则 | 长期 | 正式版标签先问 owner；部署前查正在跑的 run；生产操作只由协调者做；不碰密钥；绿不绿看退出码；修复配上回退就会失败的测试；放宽安全的改动先做对抗性审查 | [0030](0030-standing-operating-rules.md) |
| D31 | 迁移的体积方案：方案 A | 2026-10-21 | 库全部懒加载，入口预算 315,805 B 不动；搬出入口包的弹层代码支付懒加载桩 | [0031](0031-entry-budget-plan-a.md) |
| D32 | 接受第 1 步的层级变化 | 2026-10-21 | 层级刻度带来的先后顺序变化都接受，每一处列出来给 owner 在预览里看 | [0032](0032-step1-stacking-changes-accepted.md) |
| D33 | 手机右滑返回手势 | 2026-10-21 | 要做：手指把对话往右拖回列表；做成有门槛的实验，自研和 Motion 各一版，留好的 | [0033](0033-phone-swipe-back-experiment.md) |
| D34 | 组件目录页只在开发时可用 | 2026-10-21 | 目录页不进正式版构建 | [0034](0034-component-catalog-dev-only.md) |
| D35 | 第 4 步结果：不引入 Motion，右滑返回用自研版 | 2026-10-21 | 级联、点按推入、布局过渡保留现有代码；右滑返回上线 Pointer Events + Web Animations 版（Motion 版懒加载块大 22 KB、接住时会跳）；等 owner 预览 | [0035](0035-step4-motion-experiment-result.md) |
| D36 | 聊天上传的文件放进 Bot 的电脑 | 2026-10-21 | 用户消息的附件复制到 `/workspace/home/uploads/<对话 id>/`；懒复制、幂等、可见性同 `read_attachment`；复用文件通道反向（`files.import_chunk`） | [0036](0036-chat-uploads-on-the-computer.md) |
| D37 | Bot 电脑预装办公工具，只预装英文 | 2026-10-21 | LibreOffice、tesseract（英文）、poppler、pandoc、Python 办公库进镜像；其他语言的数据和字体由 Bot 取到用户目录（`TESSDATA_PREFIX`、`~/.local/share/fonts`）；matplotlib 不改默认字体 | [0037](0037-english-only-office-tooling.md) |
| D38 | 瞬时错误先重试，失败的运行不丢已发布的文件 | 2026-10-21 | 流内过载、限流、5xx、断流按错误码有界重试（约 2 分钟，服从 `Retry-After`）；运行失败时已发布的文件绑定到一条只带附件的消息上，链接仍可打开 | [0038](0038-failed-runs-keep-published-files.md) |
| D39 | `sandbox:` 链接服务端输出时改成附件地址 | 2026-10-21 | `sandbox:/api/attachments/<id>` 和与本消息附件唯一同名的 `sandbox:` 绝对路径，在 `Message.MarshalJSON` 里改成 `/api/attachments/<id>`；库里原文不变，其他协议不碰 | [0039](0039-serve-sandbox-links-as-attachments.md) |
| D40 | 查证原则放到决策点，并让 Bot 能读网页 | 2026-10-21 | 服务端 `web_fetch` 与联网搜索同开关，读过的网址记入证据；“人提到的作品/产品/人/事件先搜再答，说成有出处的数字要落在打开过的页面上”并入原有查询句，同时写进 `say` 说明和请求旁边；读网页被拒时去掉并记住 | [0040](0040-lookup-at-the-point-of-decision.md) |
| D41 | 内置交付技能 | 2026-10-21 | 五个 Skill（spreadsheet、document-report、slides、web-page、code-delivery）嵌在二进制里，默认对所有 Bot 可用，设置 › Skills 里标“内置”、可关不可删；提示词只多五行描述（+616 字符），脚本在 Bot 读 Skill 时复制到电脑 | [0041](0041-builtin-deliverable-skills.md) |
| D42 | 群成员名单带职责，按职责分工 | 2026-10-21 | 群里的成员名单每项带该成员持久指令开头一行（最多 140 字符）；提示词用一条原则说明：自己职责内的自己做，同事职责更合适的部分交给同事并并入同一条回复，单职责请求和闲聊不拆；待 owner 确认 | [0042](0042-group-roster-roles-and-delegation.md) |
| D44 | 早先说过的事：边学边存记忆，历史截断时留摘要和可搜索提示 | 2026-10-21 | 照 Anthropic 记忆工具、压缩、工具说明的做法补缺口：`save_memory` 说明写明何时存；记忆本来已在每轮上下文里；历史被截断时加一条“早先轮次存在，可用 `search_history`”；`search_history` 说明写何时用；无逐轮搜索 | [0044](0044-recall-context-memory-and-search.md) |

## 怎么新增一条决定

1. **编号**：用下一个空着的四位数编号（`ls docs/decisions/`），文件名是 `NNNN-kebab-case-英文短名.md`。编号一旦用过就不再复用，被推翻的决定也不删除。
2. **格式**：照现有文件写。标题是 `# NNNN 中文标题`，下面依次是这几节：

   | 节 | 写什么 |
   | --- | --- |
   | `## 决定` | 决定的内容 |
   | `## 日期` | 美国太平洋时间 |
   | `## 谁拍板` | 拍板的人 |
   | `## 背景` | 当时的情况 |
   | `## 理由` | 为什么这样定 |
   | `## 影响` | 第一行写实施状态和提交 |
   | `## 相关代码与文档` | 路径、`file:line`、提交、测试名 |

3. **证据**：每个事实都要能查到（路径、`file:line`、提交、tag、测试名）。查不到的写“待确认”，不要猜。owner 的原话没写理由时，照实说没写。
4. **同一个提交**：决定在哪个提交里落地，就在那个提交里加上决定文件，并在上面的表格里加一行。
   - 如果决定改变了 `AGENTS.md` 里写的约定，同一个提交里也改 `AGENTS.md`。
   - 如果决定影响产品现状或路线，同步改 `docs/product/status.md` 或 `docs/product/roadmap.md`。
5. **推翻或修改旧决定**：新开一个文件，写明它替代的是哪一条，例如“替代 D12 的第二点”。旧文件不改原文，只在顶部加一行“已被 NNNN 替代”，并在表格的“一句话”一栏注明。
6. **只记 owner 拍板的事**。实现中的工程取舍写进提交说明或相关的 spec，不放在这里。

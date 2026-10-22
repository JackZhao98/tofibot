# 能放心交给 TOFI 的任务

> 写于 2026-10-22 PDT。依据：`tofibot-taskeval` 分支 `eval/task-completion` 上的脚本 `eval/trust_report.py`（提交 `82ff7e6`）读本机 `eval/results/` 生成；审批边界按 `main` @ `61f4c3a` 的代码核对。
> 起因：owner 问“我并不知道我什么样的任务可以放心交给我们这个”。这页是诚实的回答，不是宣传。它是路线图 `docs/product/next-plan.md` §2.1 的 A6（该文件在 `docs/next-plan` 分支上，`main` 里还没有）。
> 每一档、每一题都能追到结果目录；没能核实的标 **待确认**。规则沿用 [D27](../decisions/0027-docs-are-the-system.md)：下次评测跑完，重跑脚本、更新本页。

## 先说三句话

1. **目前能放心交的，是“一句话就说得清、一次回复就能做完”的小活**：查个事实、换算单位、做张清单、定个提醒、在它自己的电脑上跑段小脚本。这几类在 Sonnet 5.5 上 3–4 次都没失手。
2. **“看一眼”和“先别”不等于做不了。** 很多是做出来了，但回复里漏了一句话（时区、基线存哪），或者评测本身可能判得太严。每一项下面都写了它到底栽在哪，你可以自己判断那一点你介不介意。
3. **每题只测过 3–4 次**，题目是固定的 15 道加几道专项和 8 道交付题，在生产环境用全新的 Bot 跑。**全对只代表“目前没见它失手”，不代表保证**：4/4 的 95% 区间是 51%–100%。真实使用还没有任何质量信号（`next-plan.md` 附录）。

## 怎么分的档

| 档 | 规则 | 一句话 |
|---|---|---|
| 放心交 | 通过率 ≥ 8/9 | 3 次里 3 次、4 次里 4 次通过 |
| 看一眼 | 5/9 ≤ 通过率 < 8/9 | 4 次里 3 次通过；交出来你扫一眼 |
| 先别 | 通过率 < 5/9 | 4 次里 2 次或更少；先别当成能自己办好 |
| 数据不够 | 可计的运行少于 3 次 | 不下判断，单独列在最后 |

- 规格写的是“≥ 8/9、5–7、≤ 4”，按比例读（我们每题只有 3–4 次，凑不出 9 次）。
- “通过”= 评分员（Opus 5.5）按 `task_rubric.md` / `deliverables_rubric.md` 给的 `completed`：只看结果、以记录为准，**偏严**。
- **只用一个模型判一题**：对话类用生产默认的 Sonnet 5.5（[D14](../decisions/0014-sonnet-conversation-haiku-background.md)），交付类用 Bot 的默认模型（实际是 codex-gpt-5.6-terra）。Haiku、Opus 只作旁证列出，不改档位——混着算会把谁失手藏起来。
- 脚本自己出错的运行（harness error）不算；只测评测脚本本身的目录（`smoke1`、`remote-proof*`、`local-regress`、`fix-proof`）不算。
- **版本**：结果文件里没有记版本号。表里的 rc 号来自 [status.md](status.md) §5 和 [releases.md](../releases.md)，标“按时间推断”的是拿结果生成时间对 rc 标签时间推的，**待确认**。A5 会让以后的结果自己带版本。

## 一眼看完（按日常说法）

### 放心交

| 你可以这样交给它 | 证据（Sonnet 5.5） |
|---|---|
| 在它自己的电脑上写个小脚本、跑一遍、把输出给你看 | 4/4 |
| 做一页能打印的清单（行李、采购、待办） | 4/4 |
| 单位换算，给一张小表 | 4/4（Haiku 有一次把 3 杯面粉算错，别派给 Haiku） |
| “几分钟后提醒我做件事” | 4/4 |
| 问一个开源项目还有没有人维护，要一句是/否加理由 | 4/4 |
| 问一个陌生的日文名字怎么念（私聊、群里各一题） | 3/3 + 3/3 |

这一档的共同点：**不动你的文件、不对外发东西、一次回复交完**。它们的失败模式：目前没有。

### 看一眼（交完你扫一眼再用）

| 你可以这样交给它 | 证据 | 它栽过的地方 |
|---|---|---|
| 给房东写封催修的信（草稿） | 3/4 | 最早一轮把“写”当成“发”，停下来要地址；之后三轮都好 |
| 群里一个 Bot 写、一个核实，给你一份定稿 | 3/4 | 最早一轮把草稿和交接话一起露给你；之后三轮都好 |
| 问个技术选型（Bun 还是 Node），要明确建议 | 3/4 | 有一轮写了没有出处的具体日期 |
| 每个工作日早上发 3 条 AI 新闻 | 3/4 | 最近一轮被判“第一次触发排在当天”；实际有没有多发，没证据 |
| “盯着下一个 Go 版本，发布了再通知我” | 3/4 | 最近一轮建了两条重复的定时任务，没发布也每次说一句 |
| 整理一批合同成按客户分类的索引表，每份一句摘要、60 天内到期的标出来 | 3/4（文件能用 3/4） | 第一轮上传的 zip 在它电脑上打不开，之后三轮都好 |
| 用销售数据做一份带图表的季度复盘报告（Word/PDF） | 3/4 | 第一轮没图表；最近一轮的回复有说过头的地方 |
| 做一个单文件的产品介绍网页 | 3/4 | 一轮手机宽度下排版挤到屏幕边、徽章被裁 |

### 先别（别当成它能自己办好）

按“为什么”分三类，比单看通过率有用：

**A. 稳定地栽在同一个地方**

| 任务 | 证据 | 栽在哪 |
|---|---|---|
| “牙医下周四 3 点，前一天早上提醒我” | 0/4 | 时间都对，**回复从没写是哪个时区**。rc.27 修过，rc.29 复测还是没写 |
| 问一个它可能不认识的片名（VisionQuest 好看吗） | 0/3 + 0/3 | 没查，凭记忆当成 1985 年的电影评了（回复里承认是凭记忆）。修复已在 rc.30，**修后没复测** |
| 把一季度发票（PDF 加照片）整理成 Excel，另列有问题的 | 0/4 | 四轮四种败法：文件打不开、4 张对不上只抓到 2 张、重复发票重复计数、最近一轮表全对但回复说“所有明细都核对了”实际只核了 PDF |
| 盯两个竞品定价页，每周一早 9 点有变化才报 | 0/4（东西是对的 4/4） | 摘要和定时任务都对，回复没说“基线存在哪”；一轮没说时区 |
| 群里写公告加配图（写手、设计、核实三个 Bot） | 0/4（成品能用 4/4） | 公告和图都合格能直接发；没过是因为**三个 Bot 没有分工，核实的从没动**。对你来说东西是对的 |
| 把复盘报告做成 10 页客户 PPT | 0/4（文件能用 0/4） | 一个形状越界、一个占比差 0.1 个点、评测没在页面上找到 Q1/Q2 收入。**文字溢出和“找不到数字”很可能是评测器误判**（估算字数、不读图表数据），A5 先审评测器；在那之前不能说它做不出 |

**B. 时好时坏**

| 任务 | 证据 | 栽在哪 |
|---|---|---|
| 给现有小工具加“按天导出 CSV”，改好代码发回来 | 2/4（最近两轮过） | 前两轮：上传的 zip 打不开；改好了但发文件接口连续 500。两个都已修，最近两轮通过 |
| “下个月搬家，只有两个周末”，给计划 | 2/4 | 计划都有，结尾列了一串问题而不是先说明假设再交付 |
| 群里两个 Bot 一起判断自托管 wiki 还是买 Notion，只要一条建议 | 2/4 | 建议是对的，但有 Bot 在群里对你说分工话（“笔记留给 Pip 汇总”）。D42 修复已在 rc.32，线上没复测 |
| 查三个城市人口，算倍数 | 2/4 | 算术对；一轮口径混用，一轮只看了搜索结果标题就把数字写成“出自维基” |

**C. 可能主要是评测的问题**

| 任务 | 证据 | 说明 |
|---|---|---|
| 一句含糊的话（“给 Sam 写条关于截止日期的消息”） | 2/4 | Bot 先给通用草稿再问细节，评测预设的回答有两轮没送到。[status.md](status.md) 5.3 也标了待确认，不一定是产品缺陷 |

### 数据不够（只测过 2 次，不下判断）

- 做一页今日科技新闻小结、发布成文件并把链接发你（Sonnet 1/2）
- 找到某篇新闻的配图地址给你（Sonnet 2/2）

## 它到底什么时候会先问你

先说结论：**不能写成“发送、发布、删除、付钱、改线上，TOFI 一律先问”。** 代码里这几类动作有的是硬闸，有的靠模型自己判断，有的根本没有闸。下面是我逐条对着代码和决定（D10、D11、D24、AGENTS.md 的 Security gates、`docs/agent-plan/mcp-tool-tiers.md`）核实的结果。

### 能对外说的版本（已按代码收紧）

> TOFI 替你起草邮件，但**发出去那一下永远是你点**。它在网页里遇到买东西、发送、发布、删除、改账户这类按钮，和调用外部服务（日历、Notion 之类）的写操作时，会先判断这是不是你明确要它做的，不是就先问你。登录、两步验证、验证码这些只能人做的步骤，它会请你自己来。你可以把任何一个外部工具设成“每次都问我”。其余的活它自己做。

### 逐条核实

| 动作 | 代码里怎么管 | 是不是硬保证 |
|---|---|---|
| **发邮件** | 模型只能起草（`prepare_email`）；只有登录的你能编辑、拒绝或发送那一版；发送由主机在你点击后执行（`internal/app/mail_drafts.go`）。Gmail 连接器本身不能发信 | **是**，对 TOFI 自己的邮件路径。它在自己电脑上另找办法发信不在这条路上，见下面“电脑上的命令” |
| **网页和桌面里的点击**（购买、发送、发布、删除、改账户） | 模型先给这一下标一个后果类型（`none / submit / purchase / send / delete / publish / account / other_external`）；标成有后果的，交给一个审核模型：你的原话明确要求这一下、后果有限就放行，花钱、发布、删除、改权限、不可撤销、意思不清就让 Bot 来问你；审核失败也是问你（`browser_read.go` 的 `guardAction`） | **不是**。靠模型如实标后果；标成 `none` 就不进闸。直接指令算确认（提示词：“A direct instruction is the confirmation”） |
| **外部服务的写操作**（MCP：Notion、日历等） | 每个工具三档：服务方自己标“只读”的直接跑，不审；其余算写操作，每次先审；你设成“始终问我”的每次都由人定，无人值守时直接拒绝（`mcp-tool-tiers.md`）。审核模型认为你的请求涵盖了这次调用，就直接执行，不问你 | **不是**。三处会绕过“问你”：服务方自己标的只读（信它的话）、审核模型放行、你点过“始终允许”（D24）或“本对话记住”（24 小时）。**“始终允许”在定时任务里也生效**。社区安装的连接默认按写操作审，不信它自己的只读标注（D47） |
| **装/改连接和技能**（`manage_extensions`） | 必须你批准那一份确切内容；定时等无人值守时拒绝（`extension_gate.go`） | **是** |
| **人看不到的内部对话**（Bot 之间的内部运行） | 所有“请人输入”的卡片和 MCP 审批一律拒绝，不会挂着等（D10，`requireHumanPresent`） | **是**（这是拒绝，不是放行） |
| **登录、两步验证、验证码** | Bot 发接管卡片，请你在它的电脑上亲手完成，运行挂起等你（D11） | **是**，Bot 不替你过这一步 |
| **它自己电脑上的命令**（`computer_shell`：删文件、`curl` 发请求、用 ssh 钥匙连服务器） | `guardAction` 只在浏览器点击和桌面操作两处被调用（`browser_read.go:222`、`microvm.go:455`）；**我在代码里没找到对 `computer_shell` 的审批闸**。AGENTS.md 也把“`computer_shell` 与共享 Chrome 调试端口没有隔离”列为已知缺口 | **没有闸** |
| **付钱** | 没有专门的付款闸。网页上点“购买”走上面的点击闸；提示词只写了处理弹窗时“绝不接受购买” | **不是** |
| **改线上/生产** | TOFI 没有“生产环境”这个概念，能改什么取决于你给了它什么钥匙（ssh 钥匙、连接）。动手的路径是电脑命令或外部服务写操作，各自的闸如上 | **没有专门的闸** |
| **发布文件**（`publish_file`） | 只生成一个附件，链接只有账户所有者能打开，不算对外发布 | 不是外发，不需要闸 |

### 要把它变成能对外承诺的话，还差什么（给 owner 参考，不是今晚的任务）

- 电脑命令里的外发动作（发信、`curl -X POST`、ssh 到服务器）目前没有审批闸；要么加闸，要么对外不承诺。
- 网页点击的“后果类型”由模型自己标；要硬保证就得让主机按页面元素（提交按钮、域名）自己判，不看模型的标记。
- “始终允许”和“本对话记住”在定时任务里会替你点头；要不要在这里加一个“无人值守时不适用”由 owner 定。
- 在改这些之前，对外只说上面那段“能对外说的版本”，不要说“一律先问”。

## 重新生成

```
cd tofibot-taskeval
python3 eval/trust_report.py --out /tmp/trust-evidence.md
```

脚本只读 `eval/results/`，不调用任何模型，不改结果目录。新增一道评测题，它会自动出现在档位里（没登记白话描述时显示题目原话）；要补“已知失败模式”和“修复分支”，改脚本里的 `TASKS` 表。评测账号的 `userTimezone` 是否配置、监测题为什么默默用洛杉矶时区，A5 第④项在核，**待确认**；在那之前“时区”相关的失败（`remind_dentist`、`deliv_watch_weekly`）不要解读成纯模型问题。

## 证据明细（脚本生成，勿手改）

结果目录都在 `tofibot-taskeval/eval/results/` 下(只存在本机,被 git 忽略)。

题数:放心交 7 题，看一眼 8 题，先别 12 题，数据不够 2 题

### 放心交(≥ 8/9)

- **让它在自己的电脑上写个小脚本、跑一遍、把输出贴给你**(`computer_fibonacci`)
  - 证据:**4/4** 通过(95% 区间 51%–100%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-sonnet-r1 过 @ 待确认,rc.19 之后;prod-sonnet-r2 过 @ rc.21;prod-sonnet-r3 过 @ rc.26;prod-sonnet-r4 过 @ rc.29 按时间推断
  - 已知失败模式:没有失手记录。
  - 对应修复分支:无(没有失败)
  - 结果目录:`prod-sonnet-r1/`、`prod-sonnet-r2/`、`prod-sonnet-r3/`、`prod-sonnet-r4/`
  - 旁证(不影响分档):claude-haiku-5-5 4/4；claude-opus-5-5 1/1
- **做一页能打印的清单文件(例如徒步行李清单)**(`file_packing_checklist`)
  - 证据:**4/4** 通过(95% 区间 51%–100%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-sonnet-r1 过 @ 待确认,rc.19 之后;prod-sonnet-r2 过 @ rc.21;prod-sonnet-r3 过 @ rc.26;prod-sonnet-r4 过 @ rc.29 按时间推断
  - 已知失败模式:没有失手记录。
  - 对应修复分支:无(没有失败)
  - 结果目录:`prod-sonnet-r1/`、`prod-sonnet-r2/`、`prod-sonnet-r3/`、`prod-sonnet-r4/`
  - 旁证(不影响分档):claude-haiku-5-5 4/4；claude-opus-5-5 1/1
- **把温度、里程、杯量换算成公制,给一张小表**(`no_question_units`)
  - 证据:**4/4** 通过(95% 区间 51%–100%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-sonnet-r1 过 @ 待确认,rc.19 之后;prod-sonnet-r2 过 @ rc.21;prod-sonnet-r3 过 @ rc.26;prod-sonnet-r4 过 @ rc.29 按时间推断
  - 已知失败模式:Sonnet 没有失手记录。Haiku 有一次把 3 杯面粉换算错了(360 mL),所以别把这类活派给 Haiku。
  - 对应修复分支:无(没有失败)
  - 结果目录:`prod-sonnet-r1/`、`prod-sonnet-r2/`、`prod-sonnet-r3/`、`prod-sonnet-r4/`
  - 旁证(不影响分档):claude-haiku-5-5 3/4；claude-opus-5-5 1/1
- **几分钟后提醒我做一件小事**(`remind_stretch_3min`)
  - 证据:**4/4** 通过(95% 区间 51%–100%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-sonnet-r1 过 @ 待确认,rc.19 之后;prod-sonnet-r2 过 @ rc.21;prod-sonnet-r3 过 @ rc.26;prod-sonnet-r4 过 @ rc.29 按时间推断
  - 已知失败模式:没有失手记录。有一次等了 208 秒才到点,是提醒本身的延时,不是出错。
  - 对应修复分支:无(没有失败)
  - 结果目录:`prod-sonnet-r1/`、`prod-sonnet-r2/`、`prod-sonnet-r3/`、`prod-sonnet-r4/`
  - 旁证(不影响分档):claude-haiku-5-5 4/4；claude-opus-5-5 1/1
- **问一个开源库还有没有人维护,要一句是/否加理由**(`research_requests_maintained`)
  - 证据:**4/4** 通过(95% 区间 51%–100%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-sonnet-r1 过 @ 待确认,rc.19 之后;prod-sonnet-r2 过 @ rc.21;prod-sonnet-r3 过 @ rc.26;prod-sonnet-r4 过 @ rc.29 按时间推断
  - 已知失败模式:Sonnet 没有失手记录。
  - 对应修复分支:无(没有失败)
  - 结果目录:`prod-sonnet-r1/`、`prod-sonnet-r2/`、`prod-sonnet-r3/`、`prod-sonnet-r4/`
  - 旁证(不影响分档):claude-haiku-5-5 2/4；claude-opus-5-5 1/1
- **问一个陌生的日文名字怎么念**(`dm_trafalgar`)
  - 证据:**3/3** 通过(95% 区间 44%–100%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-lookup-sonnet-5-5 过 @ rc.21 按时间推断;prod-lookup2-sonnet-5-5 过 @ rc.26 按时间推断;prod-lookup3-sonnet 过 @ rc.29 按时间推断
  - 已知失败模式:没有失手记录(三轮都过)。
  - 对应修复分支:无(没有失败)
  - 结果目录:`prod-lookup-sonnet-5-5/`、`prod-lookup2-sonnet-5-5/`、`prod-lookup3-sonnet/`
  - 旁证(不影响分档):claude-haiku-5-5 1/2
- **在群里问一个陌生的日文名字怎么念**(`group_trafalgar`)
  - 证据:**3/3** 通过(95% 区间 44%–100%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-lookup-sonnet-5-5 过 @ rc.21 按时间推断;prod-lookup2-sonnet-5-5 过 @ rc.26 按时间推断;prod-lookup3-sonnet 过 @ rc.29 按时间推断
  - 已知失败模式:没有失手记录(三轮都过)。
  - 对应修复分支:无(没有失败)
  - 结果目录:`prod-lookup-sonnet-5-5/`、`prod-lookup2-sonnet-5-5/`、`prod-lookup3-sonnet/`
  - 旁证(不影响分档):claude-haiku-5-5 2/2

### 看一眼(5–7 / 9)

- **整理一批合同和报价单(PDF、Word 混着,约 30 份)成按客户分类的索引表,每份一句摘要,60 天内到期的标出来**(`deliv_contracts`)
  - 证据:**3/4** 通过(95% 区间 30%–95%);交付的文件本身能用 3/4;模型 默认模型(实际 codex-gpt-5.6-terra)
  - 逐轮(旧到新):deliv-terra-r0 败 @ rc.29 按时间推断;deliv-terra-r1 过 @ rc.30 按时间推断;deliv-terra-r2 过 @ rc.30 或 rc.31,待确认;deliv-terra-r3 过 @ rc.32,status.md 记为线上版本
  - 已知失败模式:第一轮(r0)没过:上传的 zip 在 Bot 的电脑上打不开,什么也没交出来(诚实地说了打不开)。之后三轮过。
  - 评分员对最近一次失败(`deliv-terra-r0`)的原话:No index or zip was delivered because the Bot could not access the contents of contracts.zip. It tried several routes, then stated the blocker honestly without fabricating anything, but none of the deliverable criteria are met.
  - 对应修复分支:feat/computer-files-and-office(D36) — 上传文件进 Bot 的电脑,rc.30 起在线上,r1 起三轮通过
  - 结果目录:`deliv-terra-r0/`、`deliv-terra-r1/`、`deliv-terra-r2/`、`deliv-terra-r3/`
- **按产品资料做一个单文件的产品介绍网页,手机上也好看**(`deliv_landing_page`)
  - 证据:**3/4** 通过(95% 区间 30%–95%);交付的文件本身能用 3/4;模型 默认模型(实际 codex-gpt-5.6-terra)
  - 逐轮(旧到新):deliv-terra-r0 过 @ rc.29 按时间推断;deliv-terra-r1 过 @ rc.30 按时间推断;deliv-terra-r2 败 @ rc.30 或 rc.31,待确认;deliv-terra-r3 过 @ rc.32,status.md 记为线上版本
  - 已知失败模式:一轮(r2)没过:手机宽度下标题顶到屏幕左边,一个说明徽章被裁掉并压在杯子上。桌面版没问题。
  - 评分员对最近一次失败(`deliv-terra-r2`)的原话:A self-contained single-file product page was delivered with all 18 brief facts, no overflow and a clean desktop look. On the phone layout the hero text has no side padding and the badge is clipped and overlaps the cup, so the design criterion fails.
  - 对应修复分支:feat/deliverable-skills(D41) — 多宽度预览脚本 rc.32 起在线上;r3 通过,但只一轮
  - 结果目录:`deliv-terra-r0/`、`deliv-terra-r1/`、`deliv-terra-r2/`、`deliv-terra-r3/`
- **用三个季度的销售数据(csv、xlsx)和一页产品介绍,做一份带图表的季度复盘报告(Word 或 PDF)**(`deliv_quarterly_report`)
  - 证据:**3/4** 通过(95% 区间 30%–95%);交付的文件本身能用 3/4;模型 默认模型(实际 codex-gpt-5.6-terra)
  - 逐轮(旧到新):deliv-terra-r0 败 @ rc.29 按时间推断;deliv-terra-r1 过 @ rc.30 按时间推断;deliv-terra-r2 过 @ rc.30 或 rc.31,待确认;deliv-terra-r3 过 @ rc.32,status.md 记为线上版本
  - 已知失败模式:第一轮(r0)没过:报告里没有图表,也没点名 Q3 最畅销产品。之后三轮过;r3 的回复有超出记录的说法(见 D45)。
  - 评分员对最近一次失败(`deliv-terra-r0`)的原话:Milo parsed all four inputs without pandas, including the xlsx and docx, and published a 6-page PDF whose quarterly revenue and growth figures match the truth. The inspection finds no charts (images=0) and no named Q3 top product, and it cannot confirm the ana…
  - 对应修复分支:fix/reply-matches-work(D45) — 已合 main,不在 rc.32 里,rc.33 带;线上未复测
  - 结果目录:`deliv-terra-r0/`、`deliv-terra-r1/`、`deliv-terra-r2/`、`deliv-terra-r3/`
- **替我给房东写封信:暖气坏了一周,本周内要修好**(`draft_email_heater`)
  - 证据:**3/4** 通过(95% 区间 30%–95%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-sonnet-r1 败 @ 待确认,rc.19 之后;prod-sonnet-r2 过 @ rc.21;prod-sonnet-r3 过 @ rc.26;prod-sonnet-r4 过 @ rc.29 按时间推断
  - 已知失败模式:第一轮(r1)把“写”当成“发”,停下来要房东地址和你的信息,最后没给出可用的成稿。之后三轮都过。
  - 评分员对最近一次失败(`prod-sonnet-r1`)的原话:Pip wrote a suitable draft in its first reply but read the task as sending and stopped to ask for the landlord's address and the owner's details. After the owner said 'go ahead' it asked again and ended without a final ready-to-send email, though the task only…
  - 对应修复分支:rc.20 起“说明假设再交付”(D09) — 已在线上,r2 起三轮通过
  - 结果目录:`prod-sonnet-r1/`、`prod-sonnet-r2/`、`prod-sonnet-r3/`、`prod-sonnet-r4/`
  - 旁证(不影响分档):claude-haiku-5-5 2/4；claude-opus-5-5 0/1
- **群里两个 Bot:一个写给妈妈看的 WebAssembly 简介,一个事实核查,最后给我一份定稿**(`group_wasm_intro`)
  - 证据:**3/4** 通过(95% 区间 30%–95%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-sonnet-r1 败 @ 待确认,rc.19 之后;prod-sonnet-r2 过 @ rc.21;prod-sonnet-r3 过 @ rc.26;prod-sonnet-r4 过 @ rc.29 按时间推断
  - 已知失败模式:第一轮(r1)把初稿和给核查者的交接话一起露给了你,核查者没查就说“已核实”。之后三轮都过。
  - 评分员对最近一次失败(`prod-sonnet-r1`)的原话:Juno drafted the intro and Signal fact-checked it and posted a clearly marked final version, but the owner also saw Juno's full draft and the hand-off brief to Signal. Signal's check used no tools, and it stated specific facts as verified with no source and no…
  - 对应修复分支:rc.14–rc.21 的群聊交接与“已核实必须真查过” — 已在线上,r2 起三轮通过
  - 结果目录:`prod-sonnet-r1/`、`prod-sonnet-r2/`、`prod-sonnet-r3/`、`prod-sonnet-r4/`
  - 旁证(不影响分档):claude-haiku-5-5 3/3(+1 次脚本错误未计)；claude-opus-5-5 0/1
- **每个工作日早上给我发 3 条 AI 新闻(定时任务)**(`recurring_ai_digest`)
  - 证据:**3/4** 通过(95% 区间 30%–95%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-sonnet-r1 过 @ 待确认,rc.19 之后;prod-sonnet-r2 过 @ rc.21;prod-sonnet-r3 过 @ rc.26;prod-sonnet-r4 败 @ rc.29 按时间推断
  - 已知失败模式:最近一轮(r4)被判不过:说好“明天开始”,但系统里第一次触发排在当天,只靠指令里的一句日期判断压住今天那次;这条守卫没有实际触发过,评分员不认。前三轮都过。这是个偏严的判法,实际有没有多发一条,没有证据。
  - 评分员对最近一次失败(`prod-sonnet-r4`)的原话:Signal created a correct weekday 8am Pacific schedule with a clear instruction and replied honestly, including that a run fires today. The row's first due time is today rather than tomorrow, and today's silence relies on an untested guard in the instruction, s…
  - 对应修复分支:未登记
  - 结果目录:`prod-sonnet-r1/`、`prod-sonnet-r2/`、`prod-sonnet-r3/`、`prod-sonnet-r4/`
  - 旁证(不影响分档):claude-haiku-5-5 4/4；claude-opus-5-5 1/1
- **“Bun 比 Node 好吗?小 API 该用哪个”:查完给一个明确建议**(`research_bun_node`)
  - 证据:**3/4** 通过(95% 区间 30%–95%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-sonnet-r1 过 @ 待确认,rc.19 之后;prod-sonnet-r2 败 @ rc.21;prod-sonnet-r3 过 @ rc.26;prod-sonnet-r4 过 @ rc.29 按时间推断
  - 已知失败模式:一轮(r2)没过:写了一个没有出处的具体日期(Node 下次安全发布 10 月 21 日)。其余三轮过。
  - 评分员对最近一次失败(`prod-sonnet-r2`)的原话:Signal looked up the Bun blog and Node's release page and gave a clear, reasoned recommendation for Node. It also stated a specific upcoming Node security-release date (Oct 21) that nothing in the visible lookups supports, so the no-unverified-specifics criter…
  - 对应修复分支:rc.21“说已核实必须真查过”、D40 — 已在线上,r3、r4 通过
  - 结果目录:`prod-sonnet-r1/`、`prod-sonnet-r2/`、`prod-sonnet-r3/`、`prod-sonnet-r4/`
  - 旁证(不影响分档):claude-haiku-5-5 3/4；claude-opus-5-5 1/1
- **盯着下一个 Go 版本,发布了再通知我(定时任务)**(`watch_go_release`)
  - 证据:**3/4** 通过(95% 区间 30%–95%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-sonnet-r1 过 @ 待确认,rc.19 之后;prod-sonnet-r2 过 @ rc.21;prod-sonnet-r3 过 @ rc.26;prod-sonnet-r4 败 @ rc.29 按时间推断
  - 已知失败模式:最近一轮(r4)没过:建了两条重复的定时任务,而且两条都规定“没发布也每次说一句还没发”,违背“发布了才通知”;回复里还错误地说前面的“已安排”是假的。前三轮都过,所以是偶发,但后果是每次检查发两条打扰。
  - 评分员对最近一次失败(`prod-sonnet-r4`)的原话:Signal created an active Wed/Thu schedule to watch for Go 1.28, then made a duplicate after the harness reply and wrongly told the owner its first scheduling claim had been false. Both instructions post a 'not out yet' line on every run instead of pinging only…
  - 对应修复分支:未登记
  - 结果目录:`prod-sonnet-r1/`、`prod-sonnet-r2/`、`prod-sonnet-r3/`、`prod-sonnet-r4/`
  - 旁证(不影响分档):claude-haiku-5-5 4/4；claude-opus-5-5 1/1

### 先别(≤ 4 / 9)

- **一句含糊的话(“给 Sam 写条关于截止日期的消息”):它该先给草稿再问细节**(`clarify_ambiguous_sam`)
  - 证据:**2/4** 通过(95% 区间 15%–85%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-sonnet-r1 过 @ 待确认,rc.19 之后;prod-sonnet-r2 败 @ rc.21;prod-sonnet-r3 败 @ rc.26;prod-sonnet-r4 过 @ rc.29 按时间推断
  - 已知失败模式:两轮(r2、r3)没过:Bot 给了通用草稿后在聊天里问“哪个截止日期”,评测预设的回答没送到,草稿停在通用版。这可能是评测脚本的问题(status.md 5.3 也标了待确认),不一定是产品缺陷;r4 的同一做法被判通过。
  - 评分员对最近一次失败(`prod-sonnet-r3`)的原话:Juniper reasonably drafted a generic deadline check-in and asked in chat for the project and date, but did not use the question mechanism, so the canned answer never arrived. The required message about the beta deadline moving from Nov 3 to Nov 10 because the …
  - 对应修复分支:无产品修复分支 — 怀疑是评测口径,待 A5 一并核对
  - 结果目录:`prod-sonnet-r1/`、`prod-sonnet-r2/`、`prod-sonnet-r3/`、`prod-sonnet-r4/`
  - 旁证(不影响分档):claude-haiku-5-5 1/4；claude-opus-5-5 1/1
- **给我们的订单汇总小工具加一个“按天汇总导出 CSV”,改好的代码发回来并跑给我看**(`deliv_code`)
  - 证据:**2/4** 通过(95% 区间 15%–85%);交付的文件本身能用 2/4;模型 默认模型(实际 codex-gpt-5.6-terra)
  - 逐轮(旧到新):deliv-terra-r0 败 @ rc.29 按时间推断;deliv-terra-r1 败 @ rc.30 按时间推断;deliv-terra-r2 过 @ rc.30 或 rc.31,待确认;deliv-terra-r3 过 @ rc.32,status.md 记为线上版本
  - 已知失败模式:前两轮没过:r0 上传的 zip 在 Bot 电脑上打不开;r1 代码改好、测试也过了,但发文件接口连续 500,文件没到你手上。后两轮过。
  - 评分员对最近一次失败(`deliv-terra-r1`)的原话:Milo added and verified a `--daily-csv` export: tests passed and all 7 days matched the truth figures. It reported this honestly with a correct run command, but the changed code never reached the owner because every publish_file call failed, so the deliverable…
  - 对应修复分支:feat/computer-files-and-office(D36) — rc.30 起在线上;fix/publish-file-replay — 发文件失败后可重试,rc.32 起在线上;r3 通过
  - 结果目录:`deliv-terra-r0/`、`deliv-terra-r1/`、`deliv-terra-r2/`、`deliv-terra-r3/`
- **“下个月搬家,只有两个周末”:给一份按周末排好的计划**(`draft_move_plan`)
  - 证据:**2/4** 通过(95% 区间 15%–85%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-sonnet-r1 败 @ 待确认,rc.19 之后;prod-sonnet-r2 过 @ rc.21;prod-sonnet-r3 败 @ rc.26;prod-sonnet-r4 过 @ rc.29 按时间推断
  - 已知失败模式:计划本身每次都有,失手在结尾:列了一串问题而不是先说明假设再交付(尺寸、距离、预算没假设)。四轮里两轮这样。
  - 评分员对最近一次失败(`prod-sonnet-r3`)的原话:Juniper delivered a concrete, ordered two-weekend plan with every required element. It asked about the missing facts (size, movers) instead of assuming them, and the reply ends on that question, so criterion 2 is only partly met.
  - 对应修复分支:D09“说明假设再交付”(rc.20、rc.27 已上线) — 没有根治,仍间歇出现
  - 结果目录:`prod-sonnet-r1/`、`prod-sonnet-r2/`、`prod-sonnet-r3/`、`prod-sonnet-r4/`
  - 旁证(不影响分档):claude-haiku-5-5 0/4；claude-opus-5-5 0/1
- **群里两个 Bot 一起判断“团队 6 个人,wiki 自托管还是买 Notion”,只要一条建议**(`group_wiki_decision`)
  - 证据:**2/4** 通过(95% 区间 15%–85%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-sonnet-r1 过 @ 待确认,rc.19 之后;prod-sonnet-r2 败 @ rc.21;prod-sonnet-r3 败 @ rc.26;prod-sonnet-r4 过 @ rc.29 按时间推断
  - 已知失败模式:两轮(r2、r3)没过:其中一个 Bot 在群里对你说了分工话(如“我的部分写在笔记里留给 Pip 汇总”),建议本身是对的。
  - 评分员对最近一次失败(`prod-sonnet-r3`)的原话:Signal contributed a hidden analysis note and Pip verified Notion pricing, then delivered a single coherent recommendation. The task falls short because Signal's owner-visible message exposed the Bot-to-Bot handoff ('left my take in a note for Pip').
  - 对应修复分支:fix/group-real-delegation(D42) — 已合 main、已在 rc.32,线上没复测;r4 在 rc.29 上通过一次
  - 结果目录:`prod-sonnet-r1/`、`prod-sonnet-r2/`、`prod-sonnet-r3/`、`prod-sonnet-r4/`
  - 旁证(不影响分档):claude-haiku-5-5 2/4；claude-opus-5-5 1/1
- **查三个城市的人口,算里斯本是另外两个加起来的几倍**(`lookup_compare_cities`)
  - 证据:**2/4** 通过(95% 区间 15%–85%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-sonnet-r1 败 @ 待确认,rc.19 之后;prod-sonnet-r2 过 @ rc.21;prod-sonnet-r3 过 @ rc.26;prod-sonnet-r4 败 @ rc.29 按时间推断
  - 已知失败模式:两轮没过,原因不同:r1 三个数字口径混用;r4 搜索只回来一串标题,它却把具体人口数写成“出自维基”,没有打开任何页面核实。算术都是对的。
  - 评分员对最近一次失败(`prod-sonnet-r4`)的原话:Juniper gave consistent municipality figures and an arithmetically correct ratio of 1.28. However, its single search returned only titles and links, so the specific figures, especially the 2025 'Wikipedia' estimates, are presented as sourced without evidence t…
  - 对应修复分支:fix/verification-rigor(D40) — 已合 main、rc.30 起在线上;r4 在 rc.29 上跑,修后未复测;fix/verification-rigor-lookup-review — 试验分支,未合并:读网页到 100% 但费用 3 倍,评分不变,要 owner 定
  - 结果目录:`prod-sonnet-r1/`、`prod-sonnet-r2/`、`prod-sonnet-r3/`、`prod-sonnet-r4/`
  - 旁证(不影响分档):claude-haiku-5-5 2/4；claude-opus-5-5 1/1
- **群里写一份新品公告并配一张图(写手、设计、核实各一个 Bot)**(`deliv_group_announcement`)
  - 证据:**0/4** 通过(95% 区间 0%–49%);交付的文件本身能用 4/4;模型 默认模型(实际 codex-gpt-5.6-terra)
  - 逐轮(旧到新):deliv-terra-r0 败 @ rc.29 按时间推断;deliv-terra-r1 败 @ rc.30 按时间推断;deliv-terra-r2 败 @ rc.30 或 rc.31,待确认;deliv-terra-r3 败 @ rc.32,status.md 记为线上版本
  - 已知失败模式:四轮的公告和配图都合格、能直接发;没过是因为评分要求三个 Bot 分工,实际总是一个 Bot(或两个)独自做完,核实的 Bot 从没动过。对你来说东西是对的,只是“分工”没发生。
  - 评分员对最近一次失败(`deliv-terra-r3`)的原话:The owner received a publish-ready announcement with all given facts and one fitting image in a single message. The research Bot never took part, so the expected three-way split was not met.
  - 对应修复分支:fix/group-real-delegation(D42) — 已合 main、已在 rc.32;只用 Sonnet 在本机验过,对线上默认模型 terra 未验证
  - 结果目录:`deliv-terra-r0/`、`deliv-terra-r1/`、`deliv-terra-r2/`、`deliv-terra-r3/`
- **把一个季度的发票(PDF 加手机拍照,打成 zip)整理成 Excel:日期、供应商、金额、税额,另外把重复的、总额对不上明细的单独列出来**(`deliv_invoices`)
  - 证据:**0/4** 通过(95% 区间 0%–49%);交付的文件本身能用 1/4;模型 默认模型(实际 codex-gpt-5.6-terra)
  - 逐轮(旧到新):deliv-terra-r0 败 @ rc.29 按时间推断;deliv-terra-r1 败 @ rc.30 按时间推断;deliv-terra-r2 败 @ rc.30 或 rc.31,待确认;deliv-terra-r3 败 @ rc.32,status.md 记为线上版本
  - 已知失败模式:四轮都没过,四种不同的败法:r0 发出的文件打不开(404)且总额算错;r1 表是对的,但 4 张总额对不上的只抓到 2 张;r2 重复发票仍在主表里重复计数;r3 表全对,但回复说“所有发票明细都核对了”,实际只核对了 PDF 那 25 张,还把 35 张说成 40 份。评分员只认了 r3 的表可用(35 张去重后日期、金额全对,4 个不一致和 5 个重复都标出),但它的回复说过头了,所以回复不能全信。
  - 评分员对最近一次失败(`deliv-terra-r3`)的原话:A correct XLSX was delivered: all 35 invoices have correct dates and amounts, all 5 duplicates and 4 mismatches are flagged, and there is one rounding false positive. The final reply overclaims that line items were checked on every invoice, when only the PDFs …
  - 对应修复分支:fix/reply-matches-work(D45) — 已合 main,不在 rc.32 里,rc.33 带;本机重放探针方向正确,线上要重跑才算数
  - 结果目录:`deliv-terra-r0/`、`deliv-terra-r1/`、`deliv-terra-r2/`、`deliv-terra-r3/`
- **把一份复盘报告做成给客户开会用的 PPT,10 页左右**(`deliv_slides`)
  - 证据:**0/4** 通过(95% 区间 0%–49%);交付的文件本身能用 0/4;模型 默认模型(实际 codex-gpt-5.6-terra)
  - 逐轮(旧到新):deliv-terra-r0 败 @ rc.29 按时间推断;deliv-terra-r1 败 @ rc.30 按时间推断;deliv-terra-r2 败 @ rc.30 或 rc.31,待确认;deliv-terra-r3 败 @ rc.32,status.md 记为线上版本
  - 已知失败模式:四轮都没过,败法不一:r0 有一个形状超出页面;r1 有一个占比算成 69.6%(应为 69.5%);r2、r3 评测没在页面上找到 Q1/Q2 收入,r3 还估算有 6 处文字溢出。其中“文字溢出”和“找不到数字”很可能是评测器误判(估算字数、不读图表数据),所以这 0/4 不等于 TOFI 做不出来;A5 正在先审评测器。
  - 评分员对最近一次失败(`deliv-terra-r3`)的原话:A 10-slide client deck with 4 native charts was published, and its key Q3, growth, top-product and region facts match the source report. The inspection estimates text overflow in 6 boxes on slides 2 and 5, and Q1/Q2 revenue figures were not detected on the sli…
  - 对应修复分支:A5 评测器先审(inspect_deliverables.py:394 的字数估算) — 待做;feat/deliverable-skills(D41)、fix/skills-generic-only — 渲染预览与形状转储脚本 rc.32 起在线上
  - 结果目录:`deliv-terra-r0/`、`deliv-terra-r1/`、`deliv-terra-r2/`、`deliv-terra-r3/`
- **盯两个竞品定价页:先存一份现价摘要,之后每周一早 9 点看一次,有变化才给我发周报**(`deliv_watch_weekly`)
  - 证据:**0/4** 通过(95% 区间 0%–49%);交付的文件本身能用 4/4;模型 默认模型(实际 codex-gpt-5.6-terra)
  - 逐轮(旧到新):deliv-terra-r0 败 @ rc.29 按时间推断;deliv-terra-r1 败 @ rc.30 按时间推断;deliv-terra-r2 败 @ rc.30 或 rc.31,待确认;deliv-terra-r3 败 @ rc.32,status.md 记为线上版本
  - 已知失败模式:四轮的摘要文件和定时任务都对,统一败在回复:没说“基线存在哪、以后拿什么比”;r2 还没说时区(默认用了洛杉矶,而你可能在别的时区)。东西是对的,说明不全。
  - 评分员对最近一次失败(`deliv-terra-r3`)的原话:Milo read both pages, published an accurate summary file stating there is no pricing, stored a baseline, and created one correct change-only weekly schedule for Monday 09:00 Los Angeles time. The final reply does not say where the baseline is kept, so the repl…
  - 对应修复分支:fix/reply-matches-work(D45) — 已合 main,不在 rc.32 里,rc.33 带;线上未复测
  - 结果目录:`deliv-terra-r0/`、`deliv-terra-r1/`、`deliv-terra-r2/`、`deliv-terra-r3/`
- **“牙医下周四下午 3 点,前一天早上提醒我”**(`remind_dentist`)
  - 证据:**0/4** 通过(95% 区间 0%–49%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-sonnet-r1 败 @ 待确认,rc.19 之后;prod-sonnet-r2 败 @ rc.21;prod-sonnet-r3 败 @ rc.26;prod-sonnet-r4 败 @ rc.29 按时间推断
  - 已知失败模式:四轮全败,而且每次败得一样:提醒时间对(周三 10 月 28 日 9 点),回复里没写是哪个时区。rc.27 做过“带上时区”的修复,r4(rc.29)复测仍然没写,说明那次修复没管用。
  - 评分员对最近一次失败(`prod-sonnet-r4`)的原话:Juniper set a correct once-off reminder for Wed Oct 28 at 9:00 PDT about the Thursday 3pm dentist appointment, saved a memory, and openly stated how it read 'next Thursday'. The final reply never told the owner the assumed timezone (Pacific), so criterion 2 is…
  - 对应修复分支:rc.27 的时区修复(97cfa6a) — 已在线上,r4 复测仍失败,没有新的修复分支;评测账号 userTimezone 未配置(A5 第④项) — 待核:未配置时本应先问,评测里默默用了洛杉矶
  - 结果目录:`prod-sonnet-r1/`、`prod-sonnet-r2/`、`prod-sonnet-r3/`、`prod-sonnet-r4/`
  - 旁证(不影响分档):claude-haiku-5-5 1/4；claude-opus-5-5 0/1
- **“VisionQuest 好看吗”:一个它可能不认识的片名,该先查再答**(`dm_visionquest`)
  - 证据:**0/3** 通过(95% 区间 0%–56%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-lookup-sonnet-5-5 败 @ rc.21 按时间推断;prod-lookup2-sonnet-5-5 败 @ rc.26 按时间推断;prod-lookup3-sonnet 败 @ rc.29 按时间推断
  - 已知失败模式:三轮都一样:没查,凭记忆把它当成 1985 年的摔跤电影来评(回复里说了是凭记忆),而你问的是漫威剧集。
  - 评分员对最近一次失败(`prod-lookup3-sonnet`)的原话:Pip made no lookup and, from memory, reviewed the 1985 film 'Vision Quest' instead of identifying VisionQuest as the Marvel series. The task is not done because the show the owner asked about was never identified or assessed.
  - 对应修复分支:fix/verification-rigor(D40) — 已合 main、rc.30 起在线上;线上“不搜索”的真因是一条格式错误的 400 被误判成账户不能搜索,已修;修后未复测;fix/look-up-unknown-names — rc.9 起在线上,没能挡住这个失手
  - 结果目录:`prod-lookup-sonnet-5-5/`、`prod-lookup2-sonnet-5-5/`、`prod-lookup3-sonnet/`
  - 旁证(不影响分档):claude-haiku-5-5 0/2
- **在群里问“VisionQuest 好看吗”(@ 了 Bot)**(`group_visionquest`)
  - 证据:**0/3** 通过(95% 区间 0%–56%);模型 claude-sonnet-5-5
  - 逐轮(旧到新):prod-lookup-sonnet-5-5 败 @ rc.21 按时间推断;prod-lookup2-sonnet-5-5 败 @ rc.26 按时间推断;prod-lookup3-sonnet 败 @ rc.29 按时间推断
  - 已知失败模式:三轮都一样:没查,凭记忆评了 1985 年的电影,没认出是漫威剧集。
  - 评分员对最近一次失败(`prod-lookup3-sonnet`)的原话:Juniper answered from memory, said so plainly, and reviewed the 1985 film Vision Quest instead of the Marvel series VisionQuest, with no lookup to check which title was meant. It offered to search only if the owner clarified, so the intended show was never ide…
  - 对应修复分支:fix/verification-rigor(D40) — 已合 main、rc.30 起在线上;修后未复测
  - 结果目录:`prod-lookup-sonnet-5-5/`、`prod-lookup2-sonnet-5-5/`、`prod-lookup3-sonnet/`
  - 旁证(不影响分档):claude-haiku-5-5 2/2

### 数据不够(n < 3,不下判断)

- **做一页今日科技新闻小结,发布成文件,并把链接发我**(`own_link_recall`):只有 2 次可计的运行
  - 只测过 2 次,不下判断。
  - claude-haiku-5-5:1/1
  - claude-sonnet-5-5:1/2
  - 结果目录:`prod-resourceful-sonnet-5-5/`、`prod-resourceful2-sonnet/`；版本:rc.27 按时间推断、rc.29 按时间推断
- **找到路透社今天头条文章的配图地址给我**(`reuters_image`):只有 2 次可计的运行
  - 只测过 2 次,不下判断。
  - claude-haiku-5-5:0/1
  - claude-sonnet-5-5:2/2
  - 结果目录:`prod-resourceful-sonnet-5-5/`、`prod-resourceful2-sonnet/`；版本:rc.27 按时间推断、rc.29 按时间推断

### 生成时跳过或读不了的结果

- 只用来测评测脚本、不算产品成绩的目录(按 status.md 5.4 跳过):`fix-proof`、`local-regress`、`remote-proof`、`remote-proof2`、`smoke1`
- 读不了的:无(其余每个 `results.json` 都解析成功)

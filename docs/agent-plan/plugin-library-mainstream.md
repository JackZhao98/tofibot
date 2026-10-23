# 主流服务接入指南：TOFI 首屏推荐清单（第二稿）
**调研日期**：2026-10-09  
**基准对标**：OpenClaw（⭐391522）、Hermes Agent（⭐252220）官方支持的集成  
**调研方式**：本地克隆两个仓库，逐个读取 skills/、gateway/、plugin-catalog/ 目录和各服务文档

---

## 0. 研究方法与可信度标记

| 标记 | 含义 | 验证方式 |
|---|---|---|
| **local** | 2026-10-09 本地读取克隆仓库文件（/tmp/oc, /tmp/hermes） | cat / grep；无网络副作用 |
| **repo** | GitHub API 读取仓库元数据（星数、许可证、最后推送日期） | curl github.com/repos/API；GET only |
| **doc** | 厂商官方文档、README、API 说明 | raw.githubusercontent.com、官方网站；GET only |
| **owner-confirm** | 项目所有者确认，来源待查 | 本轮反馈中明确声明 |
| **UNVERIFIED** | 未能一手确认；上线前需实测 | 标注原因和来源缺口 |

**严格遵守的约束**：
- ✅ 读取公开文档、GitHub repo、npm registry（GET）
- ✅ 克隆公开仓库、读取本地文件
- ❌ 不注册 OAuth 客户端、不创建开发应用、不提交表单

---

## 1. OpenClaw 技能完整清单（52 个）

**来源**：/tmp/oc/skills/ 各文件夹中的 SKILL.md / README.md（**local**）

### 1.1 笔记 / 文档 / 内容管理
| 技能 | 功能 | 连接方式 | 来源 |
|---|---|---|---|
| notion | Notion CLI/API：页面、Markdown、数据源、文件、注释、搜索、Workers、raw API | OAuth (Notion 账户) | /tmp/oc/skills/notion/SKILL.md |
| obsidian | Obsidian 本地库：读/搜/创建/编辑笔记、任务、链接、属性、插件 | 文件系统 + 本地插件 | /tmp/oc/skills/obsidian/SKILL.md |
| bear-notes | Bear 笔记库（macOS）：创建、搜索、管理 | grizzly CLI | /tmp/oc/skills/bear-notes/SKILL.md |
| apple-notes | Apple Notes（macOS）：创建、查看、编辑、删除、搜索、移动、导出 | memo CLI + Keychain | /tmp/oc/skills/apple-notes/SKILL.md |
| apple-reminders | Apple Reminders（macOS）：列表、添加、编辑、完成、删除 | remindctl | /tmp/oc/skills/apple-reminders/SKILL.md |
| blogwatcher | RSS/Atom 博客监控：订阅、更新检查 | blogwatcher CLI | /tmp/oc/skills/blogwatcher/SKILL.md |
| nano-pdf | PDF 编辑：自然语言指令 | nano-pdf CLI | /tmp/oc/skills/nano-pdf/SKILL.md |

### 1.2 开发 / 代码
| 技能 | 功能 | 连接方式 | 来源 |
|---|---|---|---|
| github | GitHub 仓库、Issue、PR、CI、注释、review、release、gh api | PAT (GitHub token) | /tmp/oc/skills/github/SKILL.md |
| gh-issues | GitHub Issues 专用：获取、选择、生成修复、开 PR、处理 review | PAT | /tmp/oc/skills/gh-issues/SKILL.md |
| coding-agent | 代码审查、重构、设计；委托给 Codex/Claude Code/OpenCode | 后台 worker | /tmp/oc/skills/coding-agent/SKILL.md |
| node-connect | 诊断 Control UI 连接故障（路由、认证、配对、QR） | 诊断工具 | /tmp/oc/skills/node-connect/SKILL.md |
| node-inspect-debugger | Node.js 调试：--inspect、断点、CDP、堆、CPU profiles | Node.js CLI | /tmp/oc/skills/node-inspect-debugger/SKILL.md |
| python-debugpy | Python 调试：pdb、breakpoint()、post-mortem、debugpy 远程 | Python CLI | /tmp/oc/skills/python-debugpy/SKILL.md |
| tmux | tmux 会话管理：列表、捕获、发送按键、粘贴、监控 | tmux CLI | /tmp/oc/skills/tmux/SKILL.md |

### 1.3 项目管理 / 任务
| 技能 | 功能 | 连接方式 | 来源 |
|---|---|---|---|
| trello | Trello 看板、列表、卡片管理 | API key / OAuth | /tmp/oc/skills/trello/SKILL.md |
| things-mac | Things 3（macOS）：todo、inbox、today、project、area、tags | 文件系统 API | /tmp/oc/skills/things-mac/SKILL.md |

### 1.4 Google Workspace (gog CLI)
| 技能 | 功能 | 连接方式 | 来源 |
|---|---|---|---|
| gog | Google Workspace：Gmail、Calendar、Drive、Contacts、Sheets、Docs | OAuth (GCP) | /tmp/oc/skills/gog/SKILL.md |

### 1.5 通讯 / 邮件
| 技能 | 功能 | 连接方式 | 来源 |
|---|---|---|---|
| himalaya | IMAP/SMTP 邮箱：列表、读、搜、撰写、回复、转发、复制、移动、删除 | IMAP/SMTP | /tmp/oc/skills/himalaya/SKILL.md |
| xurl | X (Twitter) 平台：发帖、回复、搜索、DM、媒体、followers | X v2 API / CLI | /tmp/oc/skills/xurl/SKILL.md |

### 1.6 媒体 / 娱乐
| 技能 | 功能 | 连接方式 | 来源 |
|---|---|---|---|
| spotify-player | Spotify 播放控制、搜索 | spogo CLI / spotify_player | /tmp/oc/skills/spotify-player/SKILL.md |
| blucli | BluOS 音箱：发现、播放、分组、音量 | BluOS CLI | /tmp/oc/skills/blucli/SKILL.md |
| sonoscli | Sonos 音箱：发现、状态、播放、音量、分组 | Sonos CLI | /tmp/oc/skills/sonoscli/SKILL.md |
| gifgrep | GIF 搜索、下载、截取静帧/sheet | GIF 提供商 CLI/TUI | /tmp/oc/skills/gifgrep/SKILL.md |
| meme-maker | Meme 模板搜索、格式建议、生成 | 本地或托管 | /tmp/oc/skills/meme-maker/SKILL.md |

### 1.7 智能家居 / 控制
| 技能 | 功能 | 连接方式 | 来源 |
|---|---|---|---|
| openhue | Philips Hue 灯光、场景控制 | OpenHue CLI | /tmp/oc/skills/openhue/SKILL.md |
| eightctl | Eight Sleep pod：状态、温度、告警、日程 | Eight Sleep API | /tmp/oc/skills/eightctl/SKILL.md |
| peekaboo | macOS UI 捕获、自动化 | Peekaboo CLI | /tmp/oc/skills/peekaboo/SKILL.md |
| camsnap | RTSP/ONVIF 摄像头、USB 摄像头：采帧、录取、PTZ | 摄像头 API | /tmp/oc/skills/camsnap/SKILL.md |

### 1.8 安全 / 系统
| 技能 | 功能 | 连接方式 | 来源 |
|---|---|---|---|
| 1password | 1Password CLI：sign-in、Desktop integration、secret 读写 | 1Password Keychain | /tmp/oc/skills/1password/SKILL.md |
| healthcheck | OpenClaw 主机安全审计：SSH、防火墙、更新、暴露、备份、加密、Gateway | 主机诊断工具 | /tmp/oc/skills/healthcheck/SKILL.md |

### 1.9 AI / 代码生成
| 技能 | 功能 | 连接方式 | 来源 |
|---|---|---|---|
| gemini | Gemini CLI：单次提示、总结、生成、skills、hooks、MCP、Gemma 路由 | Gemini API / CLI | /tmp/oc/skills/gemini/SKILL.md |
| oracle | 第二意见审查：重构、设计、调试 | Oracle CLI | /tmp/oc/skills/oracle/SKILL.md |
| spike | 原型验证：可行性检查、方法对比、判决报告 | 后台工作 | /tmp/oc/skills/spike/SKILL.md |

### 1.10 信息查询 / 工具
| 技能 | 功能 | 连接方式 | 来源 |
|---|---|---|---|
| goplaces | Google Places：文本搜索、地点详情、解析、review、JSON | goplaces CLI | /tmp/oc/skills/goplaces/SKILL.md |
| weather | 天气、预报、wttr.in | Web API | /tmp/oc/skills/weather/SKILL.md |
| summarize | URL、YouTube、视频、播客、文章、书籍、PDF、本地文件总结/转录 | Web 抓取 + CLI | /tmp/oc/skills/summarize/SKILL.md |
| model-usage | CodexBar 本地成本日志汇总：模型、当期或全量 | 本地日志 | /tmp/oc/skills/model-usage/SKILL.md |

### 1.11 音频 / TTS / ASR
| 技能 | 功能 | 连接方式 | 来源 |
|---|---|---|---|
| openai-whisper | 本地 Whisper ASR（无 API key） | Whisper CLI | /tmp/oc/skills/openai-whisper/SKILL.md |
| openai-whisper-api | OpenAI 音频转录 API | OpenAI API | /tmp/oc/skills/openai-whisper-api/SKILL.md |
| sag | ElevenLabs TTS，mac-style say UX | ElevenLabs API | /tmp/oc/skills/sag/SKILL.md |
| sherpa-onnx-tts | 本地 TTS，离线无云 | sherpa-onnx CLI | /tmp/oc/skills/sherpa-onnx-tts/SKILL.md |
| songsee | 音频频谱图、特征面板可视化 | songsee CLI | /tmp/oc/skills/songsee/SKILL.md |

### 1.12 可视化 / 界面
| 技能 | 功能 | 连接方式 | 来源 |
|---|---|---|---|
| diagram-maker | SVG/HTML/Excalidraw 图表：概念、架构、流程、whiteboard | 本地或托管 | /tmp/oc/skills/diagram-maker/SKILL.md |
| visualize | 代码可视化、widget 创作 | show_widget | /tmp/oc/skills/visualize/SKILL.md |

### 1.13 系统命令 / CLI
| 技能 | 功能 | 连接方式 | 来源 |
|---|---|---|---|
| ordercli | Foodora 订单查询（Deliveroo WIP） | Foodora API | /tmp/oc/skills/ordercli/SKILL.md |
| blucli | BluOS 音箱控制 | CLI | /tmp/oc/skills/blucli/SKILL.md |

### 1.14 开发者工具 / 管理
| 技能 | 功能 | 连接方式 | 来源 |
|---|---|---|---|
| clawhub | ClawHub 技能搜索、安装、验证、更新、发布、同步 | ClawHub registry | /tmp/oc/skills/clawhub/SKILL.md |
| control-ui | Control UI 操作：连接客户端、会话、仪表板、Gateway | WebUI | /tmp/oc/skills/control-ui/SKILL.md |
| mcporter | MCP 服务器列表、配置、认证、调用、检查 | MCP HTTP / stdio | /tmp/oc/skills/mcporter/SKILL.md |
| skill-creator | AgentSkill 创作、修复、验证、重构 | 文件编辑 | /tmp/oc/skills/skill-creator/SKILL.md |

---

## 2. OpenClaw 消息通道（Message Channels）

**来源**：/tmp/oc/extensions/ 目录（**local**）

OpenClaw 的核心特性之一是**支持多个消息通道**，用户可以从任何通道与 bot 交互。

| 通道 | 连接方式 | 认证 | 双向? | 来源 |
|---|---|---|---|---|
| **Discord** | Discord bot token | Bot 令牌 (Developer Portal) | ✅ | /tmp/oc/extensions/discord |
| **Slack** | Slack bot / app token | OAuth 或 Bot token | ✅ | /tmp/oc/extensions/slack |
| **Telegram** | Telegram Bot API | BotFather token | ✅ | /tmp/oc/extensions/telegram |
| **WhatsApp** | WhatsApp Business API 或 Twilio | 账户 SID + token | ✅ | /tmp/oc/extensions/whatsapp |
| **Signal** | Signal bot | LibSignal / 注册号 | ✅ | /tmp/oc/extensions/signal |
| **iMessage** | macOS iMessage API | 系统 Keychain | ✅ (macOS 仅限) | /tmp/oc/extensions/imessage |
| **Matrix** | Matrix homeserver | Matrix token | ✅ | /tmp/oc/extensions/matrix |
| **Google Chat** | Google Chat bot | Google Cloud OAuth | ✅ | /tmp/oc/extensions/googlechat |
| **Microsoft Teams** | Teams bot | Bot Framework token | ✅ | /tmp/oc/extensions/msteams |
| **Mattermost** | Mattermost bot | App token | ✅ | /tmp/oc/extensions/mattermost |
| **Synology Chat** | Synology Chat bot | Bot token | ✅ | /tmp/oc/extensions/synology-chat |
| **LINE** | LINE bot | Channel access token | ✅ | /tmp/oc/extensions/line |
| **Zalo** | Zalo bot | Zalo API key | ✅ | /tmp/oc/extensions/zalo |
| **IRC** | IRC bot | Nickname + channel | ✅ | /tmp/oc/extensions/irc |
| **Slack Huddles** | Slack voice channel | 同 Slack token | ✅ (音频) | /tmp/oc/extensions/slack-huddles |
| **Zoom Meetings** | Zoom bot | Zoom API key | ✅ (会议) | /tmp/oc/extensions/zoom-meetings |
| **Voice/Phone** | SIP/Twilio | 电话号码 + token | ✅ (音频) | /tmp/oc/extensions/voice-call |
| **SMS** | Twilio | Twilio SID + token | ✅ | /tmp/oc/extensions/sms |
| **FaceTime** | macOS FaceTime API | 系统 Keychain | ✅ (macOS 仅限) | /tmp/oc/extensions/facetime |

**重点说明**：
- **Telegram** 通过 BotFather token 轻松支持，是一个**完全可行的消息通道**，不是"永久不可行"
- iMessage / FaceTime 仅 macOS
- 所有通道都支持从客户端向 OpenClaw 发送消息（"talk to bot"）

---

## 3. Hermes Agent 的工具、技能、MCP

**来源**：/tmp/hermes/ 目录（**local**）

### 3.1 Hermes 内置工具（sample，完整列表 > 100）
| 工具 | 功能 | 类型 |
|---|---|---|
| send_message_tool | 发送消息到 Discord / Slack / Telegram / 等 | 频道集成 |
| react_to_message_tool | 对消息做反应 (emoji) | 频道集成 |
| discord_tool | Discord 特定操作 | 频道 |
| browser_tool | 浏览器控制 (Playwright MCP) | 通用 |
| computer_use | 电脑控制 | 通用 |

**来源**：/tmp/hermes/tools/（**local**）

### 3.2 Hermes 可选 MCP（optional-mcps，67 个）
**来源**：/tmp/hermes/optional-mcps/（**local**）

| MCP | 服务 | 认证 | 官方? |
|---|---|---|---|
| airtable | Airtable 数据库 | PAT | community |
| asana | Asana 项目管理 | OAuth | community |
| atlassian | Jira / Confluence | OAuth | community |
| canva | Canva 设计 | OAuth | community |
| circleci | CircleCI CI/CD | API key | community |
| clickup | ClickUp 项目管理 | API key | community |
| cloudflare | Cloudflare API | API token | community |
| cloudinary | Cloudinary CDN | API key | community |
| context7 | Context7 库文档 | API key | community |
| datadog | Datadog 监控 | API key | community |
| deepwiki | DeepWiki 仓库百科 | 无 | community |
| dropbox | Dropbox 存储 | OAuth | vendor (Dropbox) |
| figma | Figma 设计 | token | community |
| fireflies | Fireflies 会议记录 | OAuth | community |
| gamma | Gamma AI 演示 | API key | community |
| gitlab | GitLab 仓库 | OAuth | community |
| globalping | GlobalPing 网络诊断 | 无 | community |
| grafana | Grafana 可观测 | API token | vendor (Grafana) |
| ... | **更多 40+ MCPs** | ... | ... |

**来源**：/tmp/hermes/optional-mcps/（**local**）

### 3.3 Hermes 可选技能（optional-skills，23 个分类）
**来源**：/tmp/hermes/optional-skills/（**local**）

| 分类 | 包含技能示例 | 用途 |
|---|---|---|
| autonomous-ai-agents | AI agent 框架 | 自主代理 |
| blockchain | Web3 工具 | 加密/区块链 |
| communication | 沟通 | 一对一谈话规则 |
| creative | 创意工具 | 创意生成 |
| data-science | 数据分析 | Jupyter、analytics |
| devops | DevOps | 基础设施管理 |
| dogfood | 内部测试 | QA 工具 |
| email | 邮件管理 | 邮件集成 |
| finance | 财务分析 | DCF、模型、股票 |
| gaming | 游戏 | 游戏工具 |
| health | 健康 | 健康数据 |
| mcp | MCP 框架 | fastmcp、OAuth 网关 |
| migration | 迁移工具 | 迁移脚本 |
| mlops | ML 运维 | 模型部署 |
| payments | 支付 | 支付集成 |
| productivity | 生产力 | Shopify、canvas |
| research | 研究 | 搜索、分析 |
| security | 安全 | 安全审计 |
| smart-home | 智能家居 | 家居自动化 |
| social-media | 社交媒体 | 社交发布 |
| software-development | 开发 | 代码工具 |
| web-development | Web 开发 | Web 框架 |
| yuanbao | 中文 AI 工具 | 中文支持 |

### 3.4 Hermes 消息集成（plugin-catalog 中的通道相关）
**来源**：/tmp/hermes/plugin-catalog/ grep (discord|telegram|whatsapp|slack)（**local**）

| 插件 | 功能 | 类型 |
|---|---|---|
| discord-tps.yaml | Discord 通道集成 | channel |
| hermes-discord-rpc.yaml | Discord Rich Presence | presence |
| go-whatsapp.yaml | WhatsApp 通道 | channel |
| whatsapp-agent-platform.yaml | WhatsApp agent 平台 | channel |
| telegram_dashboard_probe.yaml | Telegram 仪表板 | probe |
| telegram-notify.yaml | Telegram 通知 | channel |
| hermes-telegram-business.yaml | Telegram Business 通道 | channel |

**来源**：/tmp/hermes/gateway/channel_directory.py + /tmp/hermes/plugin-catalog/（**local**）

---

## 4. Robinhood（官方 MCP，第三方可接）

> 第一稿把第三方聚合服务 Trade It 误当成了 Robinhood 官方 MCP，已由集成方于 2026-10-09 核实后更正。Trade It 是独立的多券商聚合服务（trade-it-inc/trade-it-mcp），可作为"其他券商"候选，但不是 Robinhood 官方。

| 属性 | 值 | 来源 |
|---|---|---|
| 官方 MCP | ✅ Robinhood 官方（2026-05 发布 "agentic trading"） | https://robinhood.com/newsroom/robinhood-is-now-open-to-agents/ |
| 端点 | `https://agent.robinhood.com/mcp/trading`（远程，Streamable HTTP） | live：未授权 POST 返回 401 + `WWW-Authenticate: Bearer resource_metadata=…` |
| 认证 | OAuth 2.1 授权码 + PKCE(S256) + refresh token；**支持动态客户端注册**（`registration_endpoint: https://agent.robinhood.com/oauth/trading/register`），授权页 `https://robinhood.com/oauth` | live：`/.well-known/oauth-protected-resource/mcp/trading`、`/.well-known/oauth-authorization-server/mcp/trading`（只做 GET，未注册） |
| 第三方客户端 | 开放：文档列出 Claude、ChatGPT、Codex、Cursor、Grok 及"任何支持 MCP 的平台"；owner 确认可用 | 搜索结果中的官方说明 + owner-confirm |
| 能做什么 | 读取所有 Robinhood 账户的持仓、订单历史、报价、自选股；**只能在单独开立并注资的 Agentic 账户里下股票单** | 官方 newsroom（经搜索摘要，工具名待登录后核实） |
| 费用 | MCP 本身不收费，正常券商费用照常 | 同上 |
| 连接难度 | **简单（一键 OAuth 登录）** | — |
| 默认信任 | 读类工具：自动；下单、改单、撤单、资金相关：**每次确认**，不提供"总是允许" | TOFI 策略 |
| 未核实 | 具体工具名与参数（需登录后 tools/list）；回调地址：owner 10-22 确认只接受 127.0.0.1（见 [D55](../decisions/0055-loopback-only-oauth.md)），端口与路径是否被接受待用真实账户核实 | PARTLY VERIFIED |

---

## 5. 第一阶段推荐清单（12 + N 服务）

基于 **OpenClaw ∪ Hermes** 官方支持 + MCP 可用性 + 普通用户日常频率

### 5.1 Featured List（首屏推荐 13 个）

| 排 | 服务 | 类别 | MCP 状态 | 认证 | 接入难度 | 消息通道? | 推荐理由 | 来源 |
|---|---|---|---|---|---|---|---|---|
| 1 | **GitHub** | 开发 | ✅ 官方 | PAT | 简单 | ❌ | 开发者刚需；OpenClaw + Hermes 都支持 | plugin-library.md + local |
| 2 | **Notion** | 生产力 | ✅ 官方 | OAuth+DCR | 简单 | ❌ | 笔记管理；OpenClaw + Hermes | plugin-library.md + local |
| 3 | **Linear** | 项目管理 | ✅ 官方 | OAuth+DCR/key | 简单 | ❌ | 科技公司标配；OpenClaw + Hermes | plugin-library.md + local |
| 4 | **Slack** | 通讯 | ⚠️ 社区 (⭐1862) | Bot token | 中等 | ✅ | 企业通讯；OpenClaw 消息通道 + Hermes 工具 | /tmp/oc/extensions/slack + local |
| 5 | **Discord** | 社区通讯 | ⚠️ 社区 (⭐538) | Bot token | 中等 | ✅ | 开源/游戏社区；OpenClaw 消息通道 | /tmp/oc/extensions/discord + local |
| 6 | **Todoist** | 任务 | ✅ 官方 | OAuth+DCR | 简单 | ❌ | 个人任务；OpenClaw + Hermes | plugin-library.md + local |
| 7 | **Google Workspace** (gog) | 生产力 | ❌ 无 MCP | OAuth (GCP) | 中等 | ❌ | Gmail/Calendar/Drive/Sheets/Docs；OpenClaw 内置 gog CLI | /tmp/oc/skills/gog/SKILL.md |
| 8 | **Dropbox** | 存储 | ✅ 官方 | OAuth | 简单 | ❌ | 文件同步；Hermes MCP | plugin-library.md + hermes/optional-mcps/dropbox |
| 9 | **Atlassian** (Jira/Confluence) | 项目管理 | ✅ 官方 | OAuth+DCR | 简单 | ❌ | 企业级；Hermes MCP | plugin-library.md + hermes/optional-mcps/atlassian |
| 10 | **Figma** | 设计 | ⚠️ 社区 (⭐171) | Editor token | 中等 | ❌ | 设计师刚需；Hermes MCP | hermes/optional-mcps/figma + local |
| 11 | **Telegram** | 消息通道 | ⚠️ Bot API | Bot token (BotFather) | 简单 | ✅ | 快速消息；OpenClaw 消息通道 | /tmp/oc/extensions/telegram |
| 12 | **Robinhood** | 财务/交易 | ✅ 官方 | OAuth（DCR） | 简单 | ❌ | 持仓/报价只读 + Agentic 账户下单 | agent.robinhood.com（live 元数据）、robinhood.com/newsroom |
| 13 | **Trello** | 看板管理 | ❌ 无 MCP | API key | 中等 | ❌ | 轻量看板；需 adapter；OpenClaw 支持 | /tmp/oc/skills/trello/SKILL.md |

**说明**：
- "MCP 状态"：✅ = 有官方或社区成熟 MCP；❌ = 需新建 adapter；⚠️ = 社区实现可用但需验证
- "消息通道"：✅ = 可作为消息入口（用户从该应用与 bot 交互）
- "推荐理由"：同时出现在 OpenClaw + Hermes 的服务优先推荐

### 5.2 Coming Soon / 需开发者设置

| 服务 | MCP 状态 | 需要做什么 | 优先级 | 来源 |
|---|---|---|---|---|
| **Gmail** | ❌ 无 MCP | GCP OAuth app + 轻量 adapter | 高 | gog CLI 替代，缺 MCP 包装 |
| **Google Calendar** | ❌ 无 MCP | GCP OAuth app + adapter | 高 | gog CLI 包含，缺 MCP 包装 |
| **Google Drive** | ❌ 无 MCP | GCP OAuth app + adapter | 高 | gog CLI 包含，缺 MCP 包装 |
| **Microsoft OneDrive** | ❌ 无 MCP | Microsoft Entra OAuth + adapter | 中 | OpenClaw microsoft 扩展已有，缺 MCP |
| **Microsoft Outlook** | ❌ 无 MCP | Microsoft Entra OAuth + adapter | 中 | OpenClaw microsoft 扩展已有，缺 MCP |
| **Asana** | ❌ 无 MCP | OAuth app + adapter | 中 | 有开发者控制台，缺 MCP |
| **Airtable** | ⚠️ 社区 | Hermes 已有 MCP；需验证完整性 | 中 | /tmp/hermes/optional-mcps/airtable |
| **Shopify** | ⚠️ 社区 (⭐19) | 验证社区 MCP；或用 API key | 低 | antoineschaller/shopify-mcp-server |

### 5.3 不可行列表及原因

| 服务 | 原因 | 分类 | 来源 |
|---|---|---|---|
| **WhatsApp** | 官方商用 API 仅限企业；个人无法接入 | 政策限制 | whatsapp.com/business |
| **WeChat / 微信** | 官方 API 仅限企业；个人 API 政策禁止 | 政策限制 | wechat.com |
| **Apple iCloud** | 无开发者 API；仅支持 IMAP/CalDAV（自行实现） | 无 API | support.apple.com |
| **Uber / DoorDash** | 消费者 API 不存在（仅 Driver / Restaurant） | 政策限制 | 各服务官方开发平台 |
| **Zoom** | 官方 MCP 不存在；OAuth 审核复杂 | 缺 MCP | **UNVERIFIED**；无成熟 MCP 实现 |
| **YouTube** | 无 MCP；API 成熟但无 wrapper | 缺 MCP | **UNVERIFIED** |
| **Spotify** | 无官方或成熟 MCP；OAuth 可用但缺实现 | 缺 MCP | OpenClaw spotify-player (CLI only) |

**说明**：
- Telegram **已移除** from "不可行"：作为消息通道完全可行（Bot API + BotFather）
- 可以作为"消息通道"（用户从该应用与 bot 交互）但不作为"工具"（bot 调用该服务的 API）的服务不列在"不可行"中

---

## 6. 实施路径

### 6.1 快速上线（v0.1 starter）
- ✅ GitHub、Notion、Linear、Todoist：4 个官方 MCP（已在 plugin-library.md）
- ✅ Slack、Discord：2 个社区 MCP（已在 plugin-library.md）
- ➕ Telegram：消息通道（OpenClaw 扩展已有，仅需配置）
- ➕ Robinhood：官方 MCP（新增）

### 6.2 v0.2 阶段
- 验证 Google Workspace (gog CLI) 在 TOFI Runner 的可用性
- 为 Trello / Asana 开发轻量 adapter（API 文档成熟）
- 评估 Figma MCP 完整性

### 6.3 v0.3 阶段
- Google Drive/Gmail/Calendar：GCP OAuth + adapter（三个合一）
- Microsoft OneDrive/Outlook：Entra OAuth + adapter

---

## 7. 核查清单（上线前）

| 项 | 状态 | 来源 |
|---|---|---|
| Slack DCR 是否接受 loopback | **UNVERIFIED** | 需实测 |
| Discord MCP 完整性（事件覆盖） | **UNVERIFIED** | 需实测 |
| Figma MCP (Antonytm) 完整性 | **UNVERIFIED** | 对比官方 API |
| Robinhood MCP OAuth 端点 | 已核实（live 元数据） | 工具名待登录后核实 |
| gog CLI 在 Docker/VM 中的 OAuth 流程 | **UNVERIFIED** | gog SKILL.md 提及 remote/headless，需实测 |
| Telegram 通道在 TOFI 中的集成点 | **UNVERIFIED** | 需设计集成方式 |

---

## 附录 A：数据来源完整索引

| 来源 | 路径 | 说明 |
|---|---|---|
| OpenClaw skills | /tmp/oc/skills/{skill-name}/SKILL.md | 本地读取 |
| OpenClaw extensions | /tmp/oc/extensions/ | 本地目录列表 |
| Hermes optional-mcps | /tmp/hermes/optional-mcps/ | 本地目录列表 |
| Hermes optional-skills | /tmp/hermes/optional-skills/ | 本地目录列表 |
| Hermes tools | /tmp/hermes/tools/ | 本地 Python 文件 |
| Hermes gateway | /tmp/hermes/gateway/channel_directory.py | 本地代码 |
| Hermes plugin-catalog | /tmp/hermes/plugin-catalog/*.yaml | 本地 YAML 配置 |
| Trade It MCP | https://github.com/trade-it-inc/trade-it-mcp | 官方 GitHub repo (⭐60) |
| Trade It README | https://raw.githubusercontent.com/trade-it-inc/trade-it-mcp/main/README.md | 官方文档 |
| Trade It 端点 | https://mcp.tradeit.app/mcp | 官方 endpoint (live tested) |
| Trade It 注册表 | https://registry.modelcontextprotocol.io/?q=app.tradeit%2Fmcp | 官方 MCP 注册表 |

---

## 附录 B：术语说明

- **MCP**：Model Context Protocol（模型上下文协议）
- **消息通道（Message Channel）**：用户可以从该应用向 bot 发送消息的平台（Discord、Telegram、Slack 等）
- **工具（Tool）**：Bot 可以调用该服务 API 的能力（列出账户、创建任务等）
- 一个服务可以同时是**消息通道** AND **工具**（如 Discord：既可以在 Discord 中与 bot 聊天，也可以让 bot 调用 Discord API）
- **草稿优先（Draft-first）**：在交易中创建草稿但不立即执行，需用户明确确认


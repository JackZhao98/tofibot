# 自托管安装与资源占用评审

> 写于 2026-10-22 PDT。依据 `main` @ `256d4b9`（rc.34 门禁进行中）。视角：一个人把 TOFI 装到自己的小服务器上，从浏览器用。
> 只读代码与文档得出，没有在真机上计时或量过内存；凡是推算的都写明算式，没能核实的标 **待确认**。竞品部分是凭记忆写的，没有联网核对，一律标 **待核**。
> 对应 [next-plan.md §7](../product/next-plan.md#7-0220-追加自托管--web-为最高优先级对标-grok-bot--workbuddy--muse)（自托管为最高优先级）和 [installer-backlog.md](../agent-plan/installer-backlog.md) #5。

## 1. 现状（带出处）

### 1.1 硬件门槛

| 项 | 硬门槛（装不了） | 只警告 | 出处 |
|---|---|---|---|
| 架构 / 系统 | Linux x86_64；Ubuntu 22.04/24.04、Debian 12/13（其他要 `TOFI_ALLOW_UNSUPPORTED=1`） | — | `install.sh:392`、`deploy/self-host/README.md:22-26` |
| KVM | `/dev/kvm` 必须存在，否则 "KVM is not available… Use bare metal or a VM with nested virtualization enabled"，退出且不改任何东西 | — | `install.sh:403`；TUI 把三种电脑后端全部置灰并说明原因（`install.sh:1093-1106`、`2028-2057`） |
| 其他内核条件 | `/dev/net/tun`、cgroup v2、AppArmor | — | `install.sh:402`、README:24 |
| CPU | ≥ 2 vCPU | — | `MIN_CPUS=2`，`install.sh:22`、`tofi_host.py:72` |
| 内存 | MemTotal ≥ 3584 MiB（“4 GiB 机器”） | < 7680 MiB 提示 8 GiB 更好；**无 swap 且 < 15872 MiB 提示加 swap** | `install.sh:23-25`、`446-451`；`tofi_host.py:73-78` |
| 磁盘 | `/var/lib` 下 ≥ 30 GiB 空闲（仅首次安装） | < 40 GiB 警告 | `install.sh:26-27`、`460-462` |
| 端口 | 8321 空闲；`--domain` 另需 80/443 | — | `install.sh:466-467`、`tofi_host.py:849-856` |

安装器自己装 Docker（Docker 官方 apt 源，兜底 get.docker.com；`install.sh:541`、README:220-222）。

### 1.2 没有 KVM / 没有 swap 时发生什么

- **没有 KVM**：硬停。`gVisor container` / `Plain container` 两项在 TUI 里存在但置灰 "not supported yet (coming in a later release)"（README:45-49）；`--computer gvisor|container` 直接 `die`（`install.sh:298-302`）。决定出处：`one-command-install.md:7-10`（owner 2026-10-07：只支持 Linux + KVM，不做容器电脑模式）。
- **没有 swap**：只是警告，安装继续（`install.sh:450-451`；`tofi_host.py:570-590` 的 `memory_report`）。警告原文说“一台电脑耗尽内存可能冻住整台主机”。实际的保护层有两道：每台 VM 有自己的 cgroup `memory.max`（guest 内存 + 512 MiB 开销，`tofi_host.py:80-86`），Worker 容器整体 `mem_limit = 预算 + 256 MiB`（`tofi_host.py:975`、`compose.yaml:20`）。所以更可能的后果是 **guest 内部 OOM → 停止应答 → 看门狗重启这台电脑，正在跑的任务丢**，而不是主机整体冻住；installer-backlog #5 记录的 2026-10 事故正是这种（1 GiB 电脑开三个新闻页 Chrome 后被看门狗重启）。Guest 镜像本身没有 swap 也没有 zram（`deploy/microvm/init.sh` 只有 page cache 丢弃与 compact，`init.sh:65-75`；backlog #5 仍 OPEN）。主机层“整机冻住”的说法**待确认**：cgroup 上限在的情况下我没找到会让主机冻住的路径。

### 1.3 每个账户占多少资源（算式）

**内存。** 新电脑的默认尺寸由 `default_computer_resources`（`tofi_host.py:882-904`）决定：

- 预算 `budget = max(1536, MemTotal − 2048)` MiB，CPU 预算 `max(1, nproc − 1)`（`runtime_budgets`，`tofi_host.py:876-879`）；
- 单台目标 `target = clamp(budget ÷ 4, 2048, 4096)` MiB，放不下就退到 2048、再退到 1024（“tight for Chrome”）；每台另加 512 MiB 开销；
- 并发上限：静态 claim 不超额（`account_provisioner.py:460-478`：`Σ(memory_mib+512) + 新的 ≤ 预算`，再查实时 `MemAvailable ≥ memory + 512 + 1024 headroom`）。可选 `runtime_memory_overcommit_percent` 100–200（`worker_entrypoint.py:35`），安装器不写它。
- 空闲电脑：virtio balloon + free page reporting，桌面闲置后约 0.3 GiB RSS（README:505-514）；**但 claim 仍按满额算**，并发数由 claim 决定，不由实际占用决定。
- 桌面闲置 900 s（15 min，`manager.py:326`）后休眠：快照写盘，Firecracker 进程退出，RAM 归零（`manager.py:60-67`）；再次使用从快照恢复（有快照）或冷启动（Guest 换过版本时）。

按主机大小代入（MemTotal 取标称值减约 100 MiB）：

| 主机 | 预算 MiB | 单台电脑 | 每台占 | **同时在跑的电脑数** |
|---|---|---|---|---|
| 2 vCPU / 4 GiB | 1852 | 1 vCPU · 1024 MiB（“tight for Chrome”） | 1536 | **1** |
| 2–4 vCPU / 8 GiB | 5852 | 2 vCPU · 2048 MiB | 2560 | **2** |
| 4 vCPU / 16 GiB | 13852 | 2 vCPU · 3463 MiB | 3975 | **3** |
| 8 vCPU / 32 GiB | 30000 | 2 vCPU · 4096 MiB | 4608 | **6** |

注意这条曲线：主机越大，**先把单台电脑放大**（到 4 GiB 封顶），并发数涨得很慢。16 GiB 的机器只同时服务 3 个正在用电脑的人。

**磁盘。** 每个账户的承诺是物理预留，包括稀疏文件里的洞（`account_capacity.py:3-6`）：

- 工作盘 8 GiB（`TOFI_ACCOUNT_COMPUTER_DISK_GIB=8`，`compose.yaml:94`）+ 内部预留 8 GiB（`per_account_internal_reserved_bytes = 8 GiB`，`tofi_host.py:932`；owner 决定“保持生产的 8 GiB”，`one-command-install.md:21`）= **16 GiB**；第一个账户进不来安装就失败（`FIRST_ACCOUNT_BYTES`，`tofi_host.py:90`、`1251-1254`）；
- 休眠快照预留 = 电脑内存 + 64 MiB（`account_provisioner.py:423-427`）：8 GiB 主机上每账户约 2.1 GiB；
- App 数据库上限 1 GiB/账户（`TOFI_ACCOUNT_DB_MAX_BYTES`，`compose.yaml:95`）；
- 共享底座：Guest 发行版（`rootfs.ext4` 5 GiB 稀疏，办公工具加入前约 1.6 GB 实占，`one-command-install.md:55`；D37 办公工具估算 +870 MB，`next-plan.md:124`，**待量**）+ 上一版 Guest（回滚用，README:434-437）+ 镜像 + Docker 本身，按 8–10 GiB 算；
- 备份：更新前自动备份保留 3 份（数据 + 设置，不含磁盘）；手动备份默认带电脑磁盘，只有最近 2 份保留磁盘（D48）。

| 用户数 | 承诺磁盘（16 GiB × n + 快照 2 GiB × n） | 加底座与备份 | 内存：同时在用的人数上限 |
|---|---|---|---|
| 1 | 18 GiB | ≈ 30 GiB（这就是 30 GiB 门槛的来历） | 4 GiB 主机 1 人 |
| 5 | 90 GiB | ≈ 100–110 GiB | 8 GiB 主机 2 人同时；其余休眠 |
| 20 | 360 GiB | ≈ 380 GiB | 16 GiB 主机 3 人同时 |
| 50 | 900 GiB | ≈ 1 TiB | 32 GiB 主机 6 人同时 |

磁盘是线性且刚性的（ledger 拒绝超额，`account_provisioner.py:337`）；内存靠休眠弹性化。两者都没有安装器参数可调。

### 1.4 从 curl 到第一条回答要多久

**仓库里没有任何一次实测记录**（搜过 `docs/`、`README`、acceptance 目录；`one-command-install.md` 的验收清单只有通过/不通过）。步骤与估算：

| 步 | 做什么 | 估算（100 Mbps VPS） | 依据 |
|---|---|---|---|
| 1–4 | 检查 + 卡片 + 问题 | < 1 min（6 个问题，均有默认值） | README:34-77 |
| 5–6 | apt + Docker 安装 | 1–3 min | README:219-222 |
| 7–8 | manifest + host bundle | 秒级 | README:223-228 |
| 10 | 拉 app（Alpine + 单二进制）、worker（python slim）镜像；**下载 Guest tar.zst**（zstd -12，`release.yml:181`）、校验、解包 5 GiB 稀疏 ext4 | 镜像 < 1 min；Guest 下载 2–4 min（压缩后 1–1.5 GB，**待量**）+ 解包校验 1 min | README:230-236 |
| 11 | Worker 就绪探针（最多 120 次尝试）+ App `/health` 60 s | 0.5–1.5 min | `tofi_host.py:1235`；`one-command-install.md:195-196` |
| 浏览器 | 接受自签证书 → 输入 setup key → 设密码 → Settings › Model provider 贴 key → 创建 Bot → 第一句话触发**电脑冷启动**（Firecracker + Chrome） | 2–4 min 人工；冷启动 README 示例里写 14.2 s（示例输出，非实测） | README:249-254、374-377 |

合计 **约 8–15 min，待在 VM 106 上实测**（B1 正在跑 v0.1.3 → rc.33 升级验收，顺手加一行计时就能拿到）。瓶颈是 Guest 下载和人工的 key/Bot 步骤，不是脚本本身。

### 1.5 远程访问方式与摩擦

| 方式 | 命令 | 摩擦 | 出处 |
|---|---|---|---|
| 默认：按 IP 自签 HTTPS | 无 | 每个浏览器/设备都要点过证书警告一次；要对比指纹才算安全；换 IP 要 `tofi regenerate-cert`；Safari 在 http localhost 不存 Secure cookie（dev 用） | README:153-177；`AGENTS.md` 预览一节 |
| 域名 + Caddy 自动 HTTPS | `--domain D [--email E]` | 要公网 IP、80/443 空闲、DNS 先指过来（否则要确认）；**Caddy 后面的 cookie 没有 Secure**（已知缺口） | README:197-199、564-567 |
| 已有反代（Caddy/Nginx/Cloudflare Tunnel） | `--behind-proxy IP[,CIDR]` | 要知道反代地址填 `TOFI_TRUSTED_PROXIES`；打印 Caddy/Nginx 片段 | README:185-208；backlog #1 DONE |
| 只本机 + SSH 隧道 | `--local-only` | 每次用前先开隧道；手机上基本不可行 | README:129 |
| Tailscale / WireGuard 之类 | 无专门支持 | 能用默认模式（tailnet IP 访问 `:8321`），但证书 SAN 只放“全局地址”（`tofi_host.py:646`、`4490`），100.64/10 不是全局地址，所以 SAN 里没有它——警告照样出现；没有一句文档告诉用户“这是最省事的办法” | 推断自代码 |

缺一段“我在家里 NAT 后面 / 没有公网 IP 怎么办”的指引——这恰是“个人服务器”用户的常态。

### 1.6 $5–10 VPS 用户会撞到什么

按门槛逐项对照典型套餐（套餐规格凭记忆，**待核**）：

| 套餐（约） | vCPU/RAM/盘 | 撞到 |
|---|---|---|
| $4–6（DO/Vultr/Linode 入门，Hetzner CX22） | 1–2 / 1–4 GB / 25–40 GB | 1 vCPU 或 < 3.5 GiB 直接失败；25 GB 盘失败；4 GB + 40 GB 的（Hetzner CX22 一类）能过门槛，但**没有 nested virt 就停在 KVM**。DigitalOcean、Linode、OVH VPS 据我所知默认不开 nested virt（**待核**）；Hetzner Cloud 据我所知开（**待核**）；GCP/Azure 要选特定机型并开标志；AWS 只有 metal |
| $8–12 | 2 / 4 GB / 40–80 GB | 过门槛。单台电脑 1 GiB + 1 vCPU，“tight for Chrome”；只 1 台并发；无 swap 警告 |
| $15–25 | 2–4 / 8 GB / 80–160 GB | 舒适：2 GiB 电脑，2 台并发，5 个账户的磁盘刚好 |

结论：**$5 档基本装不了（KVM + 30 GiB + 3.5 GiB 三道门槛各挡一批），$10 档能装但只够一个人用。** 对标的 OpenClaw “$5 VPS 可跑”（`next-plan.md:172`）在当前架构下不成立。

### 1.7 成本能看见多少

- 模型费用：设置 › 用量（`ui/src/UsagePanel.tsx`；`usage_calls.equivalent_usd / price_known`，`internal/app/usage.go:53-54,120`），按 Bot/对话/模型分；Codex terra 无价（Q10）。
- **没有预算上限、没有告警**（`next-plan.md:176` 自己也这么记）。
- 主机资源：`tofi status` 的电脑行有 RSS（README:372-381）；A4 的运行健康汇总已合入；**网页里看不到磁盘承诺用了多少、还能开几个账户**。
- 托管费（VPS 本身）当然不在产品里；但“这台机器还能承载几个人”这个数字产品是算得出的（ledger 有 `remaining_bytes`，provisioner 有 claim 总和），只是没露出来。

### 1.8 模型提供方（自托管用户最在意的一项）

后端 `internal/provider/provider.go:277-325` 已支持 openai / openai_codex / openai_completions / anthropic / gemini / deepseek / groq / openrouter / together / ollama，以及任意带 `BaseURL` 的 OpenAI 兼容端点；**设置页只露出 anthropic、openai 和 Codex 登录**（`ui/src/ProviderSettings.tsx:11-14`）。`next-plan.md:178` 已记为缺口。

## 2. 对标（凭记忆，全部待核）

| | TOFI（本文） | OpenClaw | Hermes（Nous） | NanoClaw | ZeroClaw |
|---|---|---|---|---|---|
| 安装 | `curl \| sudo bash`，装 Docker，拉 3 个镜像 + 1 个 Guest 包 | `npm i -g` 或一条脚本，Node 运行时（待核） | `pip`/`uv` 装 Python 包（待核） | TypeScript 小代码库，clone 后跑（待核） | 单个 Rust 二进制（待核） |
| 隔离 | 每账户一台 Firecracker 微 VM（真硬边界） | 默认同进程；可选 Docker 沙箱（待核） | 可选 Docker/云沙箱（待核） | 每个 agent 一个容器（Apple containers / Docker）（待核） | 无 VM；靠进程与权限（待核） |
| 常驻内存 | App 512 MiB 上限 + Worker + 每台电脑 1–4 GiB | 数百 MB 量级（待核） | 数百 MB（待核） | 容器一个几十到几百 MB（待核） | 宣称个位数 MB（待核） |
| 硬件门槛 | KVM、4 GiB、30 GiB | 任意 Linux/macOS、$5 VPS、树莓派（待核） | 任意（待核） | macOS/Linux + 容器运行时（待核） | 任意，含树莓派（待核） |
| 浏览器 / 桌面 | 内置，Chrome 在 VM 里，带接管卡 | 需用户自带或插件（待核） | 工具链里有，无 GUI 桌面（待核） | 无 GUI 桌面（待核） | 无（待核） |
| 备份 / 体检 | `tofi backup --verify`、`security audit`（今晚上线） | `backup create --verify`、`security audit`（next-plan §7 引用） | 不详 | 不详 | 不详 |
| 多租户 | 账户隔离、邀请制、7 语言 | 单人为主（待核） | 单人 | 单人 | 单人 |

读法：TOFI 用 **“每人一台真电脑”** 换来了竞品没有的隔离与桌面能力，代价是把门槛从“任何机器”抬到了“带 KVM 的 4 GiB 机器 + 每账户 16 GiB 盘”。这个代价是架构性的，owner 2026-10-07 已拍板接受（不做容器电脑模式）；本文的问题清单不推翻它，只在它之内把门槛和摩擦降到最低，并把“要不要开第二档后端”列为拍板题。

## 3. 问题清单（按严重度）

| # | 问题 | 影响谁 | 证据 | 修法 | 量 |
|---|---|---|---|---|---|
| P1 | **磁盘按账户刚性预留 16 GiB（8 工作盘 + 8 内部预留）**，20 人要 360 GiB，50 人要 900 GiB；安装器无参数 | 任何 > 3 人的小团队；所有 VPS | `tofi_host.py:932`、`compose.yaml:94`、`account_capacity.py:3-6`；§1.3 表 | 内部预留改为安装器按主机盘大小推算或问一次“几个人用”（`tofi install --accounts N`），写进 `worker.json`；工作盘 8 GiB 保持（manager 下限 8，`manager.py:324`）；**预留值由 owner 定** | M |
| P2 | **没有 KVM 就装不了**，$5 档 VPS 几乎全军覆没 | 想花最少钱试一下的人 | `install.sh:403`、README:45-49；§1.6 | 短期（S）：README 开头列“哪些云能装”（nested virt 清单，待核）+ 安装器 KVM 失败时打印这张表；长期（L，拍板题 Q1）：容器后端（gVisor / 普通容器，TUI 里已占位） | S / L |
| P3 | **内存曲线：主机变大先放大单台电脑，并发涨得慢**（16 GiB 只 3 台） | 5–20 人的团队 | `tofi_host.py:892`（`budget ÷ 4`）；§1.3 表 | 默认单台固定 2048 MiB（Chrome 够用，D37 办公工具也够），`budget ÷ 4` 只在显式 `--computer-memory` 时生效；或安装器问“几个人用”后反推 | S |
| P4 | **无 swap/zram 只警告；guest 无内存压力缓冲**，OOM 时看门狗重启、任务丢（backlog #5） | 4–8 GiB 主机上的每个人 | `install.sh:450`、`init.sh:65-75`、backlog #5 | 主机侧（S）：安装器在“无 swap 且 < 16 GiB”时**问一句**“建一个 4 GiB swap 文件？[Y/n]”（对标 Proxmox/Ubuntu 安装器的做法）；guest 侧（M）：`init.sh` 开 zram（`zram-generator` 或 `modprobe zram` + `mkswap`），要重建 Guest 镜像 + 对抗审查（next-plan §4“暂缓不砍”） | S + M |
| P5 | **首装时间没有实测数字**，README 和落地页都没法承诺 | 决定要不要装的人 | §1.4 | B1 在 VM 106 跑时加 `date` 两端计时，写进 README“Requirements”下一行；安装器自己在 banner 里打印“用时 m:ss” | S |
| P6 | **没有“家里 NAT 后面 / 无公网 IP”指引**；Tailscale 这条最省事的路没写 | 个人服务器用户的多数 | §1.5 | README 加一节“三种暴露方式怎么选”（Tailscale/自签 IP/域名）；安装器“How will you open TOFI?”加第四项 “Through a VPN/Tailscale (IP, self-signed)”——行为同默认，只是文案与 SAN 把 CGNAT 地址也放进去（`tofi_host.py:4490` 的 `is_global` 放宽为非回环非链路本地） | S |
| P7 | **模型只露出 Anthropic/OpenAI/Codex**，后端已支持 ollama/openrouter/deepseek/任意兼容端点 | 自托管用户（本地模型、便宜模型是他们装自托管的主因） | `provider.go:277-325` vs `ProviderSettings.tsx:11-14` | 设置页加“OpenAI 兼容端点”一项（base URL + key + 模型名），直接走 `default` 分支；价格表没有的模型按“无价格”显示（Q10 口径） | M |
| P8 | **成本看不见上限**：无预算、无告警；网页看不到“这台机器还能装几个人” | 付费的 owner | §1.7 | 用量页加“本月上限 $X，到 80% 提醒”（走 D15 自动行为设置）；设置 › 服务器加一行“磁盘承诺 x/y GiB，可再开 n 个账户；内存并发 m 台”——数据 ledger 和 provisioner 已有 | M |
| P9 | 自签证书：每个设备点一次警告，换 IP 要手动 `regenerate-cert` | 所有默认安装 | README:153-177 | 保持默认（Proxmox 同款，用户群能接受）；`tofi status` 已印指纹；加一行“移动端建议 Tailscale 或域名” | S（文档） |
| P10 | Caddy 后 cookie 无 Secure（`X-Forwarded-Proto` 未支持） | `--domain` 用户 | README:564-567 | App 在 `TOFI_TRUSTED_PROXIES` 命中时读 `X-Forwarded-Proto=https` 置 Secure；是已知缺口，顺手修 | S |
| P11 | 更新把所有电脑休眠、忙的任务被打断；auto-update 固定 04:00 本地 | 多人共用的机器 | README:424-432、328-331 | 已有 active-run guard；再加 `tofi config update-window HH:MM` 即可，不急 | S |
| P12 | Guest 包体积没量过（办公工具 +870 MB 是估算），直接决定 P5 | 装的人 | `next-plan.md:124` | release 工作流里 `ls -l dist` 已打印（`release.yml:184`），把数字抄进 releases.md 即可 | S |

## 4. “最低可行自托管”规格（建议）

**目标用户**：一个人或一个 ≤ 5 人的小团队，把 TOFI 装在一台 VPS 或家里的小主机上，从手机和电脑的浏览器用。

| 项 | 目标 | 现状差距 |
|---|---|---|
| 目标硬件（档 A，推荐） | 2 vCPU / 4 GiB / 40 GiB，带 KVM（Hetzner CX22 一类，约 $5–6；待核） | 过门槛，但电脑 1 GiB “tight”、1 台并发、无 swap 警告 → P3、P4 |
| 目标硬件（档 B，团队） | 4 vCPU / 8 GiB / 160 GiB | 磁盘只够 5–6 个账户 → P1 |
| 不支持但要说清 | 无 KVM 的 VPS、ARM、macOS/Windows | 失败信息已清楚；缺“哪些云可以”清单 → P2 |
| curl → 第一条回答 | **≤ 10 min**，其中人工步骤 ≤ 3 步（证书、setup key、模型 key） | 估 8–15 min，未实测 → P5、P12 |
| 远程访问 | 装完一句话告诉用户三选一：Tailscale（最省事）/ IP 自签 / 域名 | 缺第一项 → P6 |
| 模型 | 至少 Anthropic / OpenAI / 一个 OpenAI 兼容端点（Ollama、OpenRouter） | 缺兼容端点 → P7 |
| 成本 | 用量页首屏看得到本月花费与上限；服务器页看得到还能装几个人 | 无上限、无容量 → P8 |
| 运维 | 自动补丁 + 备份/验证/还原 + 体检一条命令 | **已达标**（safe-updates、D48、D49） |

## 5. 分步落地

**第 1 步（今晚/明早，全部 S，不需拍板）**：P5 计时（B1 顺手）、P12 抄体积、P2 短期的“哪些云能装”表（标待核，owner 补核）、P6 的 README 一节与安装器第四个选项文案、P9 文档一句。

**第 2 步（第 1 周，拍板后）**：P1 预留参数（拍板 Q2）、P3 默认 2048 固定（拍板 Q3）、P4 主机 swap 提问（拍板 Q4）、P7 兼容端点（拍板 Q5）。四项都只改 `tofi_host.py` / `install.sh` / `ProviderSettings.tsx`，测试照 `test_tofi_host.py` / `test_install_sh.py` 的写法各加“撤掉就红”的用例；`make self-host-embed` 别忘。

**第 3 步（第 2 周）**：P8 预算上限 + 容量行（走 D15 设置）、P10 `X-Forwarded-Proto`、P11 更新窗口。

**第 4 步（之后，L）**：P4 guest zram（重建镜像 + 对抗审查）；P2 长期的容器后端（如果 Q1 拍板要做）。

每一步合并时按 D27 改 README“Requirements”、`status.md` 和本文的“现状”表。

## 6. owner 拍板题

| # | 问题 | 选项（推荐在前） |
|---|---|---|
| Q1 | **要不要开第二档电脑后端（无 KVM 的容器模式）** | **A** 不做，坚持 2026-10-07 决定，把精力放在 KVM 机器上的门槛（P1/P3/P4）和“哪些云能装”的清单 / B 做 gVisor 档，失去桌面与接管卡但能上 $5 VPS / C 做普通容器档（隔离最弱，不建议） |
| Q2 | **每账户内部预留 8 GiB 要不要变成可调** | **A** 安装器按主机盘推算：盘 < 100 GiB 时预留 2 GiB，否则 8 GiB，并写成 `tofi.env` 可改 / B 保持 8 GiB 常量 / C 问用户“几个人用” |
| Q3 | **单台电脑默认内存** | **A** 固定 2048 MiB，并发随主机增长（16 GiB → 5 台）/ B 维持 `budget ÷ 4`（大主机单台更大，并发少） |
| Q4 | **无 swap 时安装器要不要动手建 swap 文件** | **A** 问一句，默认是，4 GiB 文件（对标 Ubuntu 安装器）/ B 只警告（现状） |
| Q5 | **设置页加 OpenAI 兼容端点** | **A** 加（base URL + key + 模型名，价格未知就显示无价格）/ B 不加，自托管只给三家 |
| Q6 | **首装时间要不要写成承诺** | **A** 实测后写“约 N 分钟（100 Mbps）”进 README 与落地页 / B 不写 |
| Q7 | **预算上限** | **A** 月上限 + 80% 提醒，做成自动行为设置（D15）/ B 只显示不拦 |

## 相关代码与文档

- `install.sh:22-27`、`:298-302`、`:402-462`；`deploy/self-host/tofi_host.py:72-97`、`:555-600`、`:815-879`、`:882-937`、`:1235-1254`
- `deploy/self-host/compose.yaml:19-20`、`:62-63`、`:94-95`；`deploy/self-host/README.md`（Requirements、Exposure、Budgets、Known limits）
- `deploy/microvm/manager.py:60-82`、`:320-348`、`:758-762`；`deploy/microvm/account_provisioner.py:23-34`、`:460-478`、`:423-427`；`deploy/microvm/account_capacity.py:1-6`；`deploy/microvm/init.sh:65-75`；`deploy/microvm/Dockerfile`；`.github/workflows/release.yml:176-184`
- `internal/provider/provider.go:277-325`；`ui/src/ProviderSettings.tsx:11-14`；`internal/app/usage.go`；`ui/src/UsagePanel.tsx`
- [installer-backlog.md](../agent-plan/installer-backlog.md) #5、[one-command-install.md](../agent-plan/one-command-install.md)、[safe-updates.md](../agent-plan/safe-updates.md)、[next-plan.md §7](../product/next-plan.md)、[process.md §7.6](../process.md)、D37、D48、D49

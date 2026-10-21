# TOFI 工作流程

本文写的是 TOFI 平时怎么干活：分支怎么开、测试怎么证明、评测怎么跑、UI 怎么预览、版本怎么发、哪些改动要先过安全审查、什么活用什么模型，以及文档怎么跟着更新。新的会话（人或 AI）接手时，先读仓库根目录的 `AGENTS.md`，再读本文。

约定：

- 每条事实都给出处（路径、`文件:行号`、commit、tag 或测试名）。查不到出处的写 **待确认**，不猜。
- `Dxx` 指 owner 拍板的决定，编号与 `docs/decisions/` 一致（编号来自协调者整理的决定清单，若目录文件名不同，以 `docs/decisions/` 为准）。
- 本文不写服务器地址、账号和任何密钥。命令里的服务器一律写成 `<server>`。
- 本文以 `main` 的 `748c774` 为准核对过。

## 1. 谁做什么

| 角色 | 做什么 | 不做什么 |
|---|---|---|
| owner | 拍板产品和对外的决定；确认正式版 `v*` 标签（D30）；看预览给反馈 | — |
| 协调者（主会话） | 拆任务、派工、验收、合并进 `main`、推送、打标签、线上部署和运维（D30） | 不把线上操作交给子代理 |
| 执行者（子代理 / 并行会话） | 在自己的 worktree 和分支上改代码、写测试、跑测试、提交 | 不合并 `main`，不 push，不打 tag，不 ssh，不碰线上；只改分配给自己的文件 |

## 2. 分支与 worktree

**主干**：`main`。所有改动都在分支上做，做完由协调者合并回 `main`。

**一个任务 = 一个分支 = 一个 worktree**。worktree 放在主仓库旁边，目录名 `tofibot-<主题>`。当前的例子（`git worktree list`）：

| worktree | 分支 |
|---|---|
| `../tofibot-allow` | `feat/standing-tool-allowance` |
| `../tofibot-chat-history` | `fix/chat-history-reload` |
| `../tofibot-lookup` | `fix/group-lookup-before-asking` |
| `../tofibot-taskeval` | `eval/task-completion` |
| `../tofibot-docsys` | `docs/system-opus` |

**分支前缀**（本地现存分支，`git branch --list`，10-20 统计）：`fix/`（70 个）、`feat/`（31 个）、`codex/`（7）、`ws/`（5）、`i18n/`（5）、`test/`（2），`eval/`、`docs/`、`chore/`、`take/` 各 1。`ui/`、`perf/` 前缀的分支合并后已删，只留在合并提交里（如 `d4d0fa4 Merge ui/sidebar-pins`、`a028e49 Merge perf/questions-poll`）。另有自动工具生成的 `worktree-agent-*`（23 个）、`worktree-wf_*`（5 个）。

开一个新任务：

```sh
git -C <主仓库> worktree add ../tofibot-<主题> -b fix/<主题> main
```

**合并**：协调者用合并提交（不快进）合并，提交信息格式是 `Merge <分支>: <一句话说明>`，例如 `748c774 Merge feat/standing-tool-allowance: ...`、`a028e49 Merge perf/questions-poll: ...`。分支落后太多时先把 `main` 合进分支（例：`0730fb7 Merge main into feat/handover-cards`），在分支上跑完测试再合回去。

**临时搁置工作**：用一个 WIP 提交，不用 stash。例：`b097d31 WIP (interrupted): standing per-Bot tool allowance, backend start`。stash 栈是所有 worktree 和所有会话共用的，**禁止裸 `git stash` / `git stash pop`**；实在要用，必须 `git stash push -u -m "<唯一标签>"`，记下 SHA，用 `git stash apply <sha>` 恢复，再按标签找到并删掉自己那一条。

**提交信息结尾**写 `Co-Authored-By` 署名行（见会话给出的署名规则）。

## 3. 测试

### 3.1 用退出码证明通过

"全绿"只能用退出码证明。不许数 `ok` / `PASS` 行数，不许看日志尾巴判断。

```sh
go test ./...; echo "exit=$?"
```

要注意的坑：

- 接了管道（`| tail`、`| grep`）之后，`$?` 是最后一个命令的退出码，不是测试的。要么不接管道，要么 bash 里 `set -o pipefail`，或读 `${PIPESTATUS[0]}`（zsh 里是 `$pipestatus[1]`）。
- **退出码 0 不一定等于跑过了**。下面 7 个 UI 脚本在没设 `PLAYWRIGHT_MODULE` 时打印 `SKIP: PLAYWRIGHT_MODULE is not set` 然后 `exit 0`：`test-automation-settings.mjs:15`、`test-model-providers-browser.mjs:8`、`test-onboarding.mjs:14`、`test-primary-followups.mjs:11`、`test-settings-polish.mjs:38`、`test-settings-shell.mjs:11`、`shot-tool-outcome.mjs:10`（均在 `ui/scripts/`）。报结果时必须同时说明输出里没有 `SKIP`。`test-tool-tiers-e2e.mjs:662` 是正确示范：它要求退出码 0、输出含 `--- PASS` 且不含 `SKIP`。
- 汇报格式：列出跑了哪些命令，每条的退出码，以及有没有 SKIP。

### 3.2 每个修复都要有"回退就会挂"的测试

D30：每个修复都配一个测试，并且要证明：**把修复撤掉，这个测试会失败**。没有这一步，测试可能根本没测到改动。

做法（不用 stash）：

```sh
# 修复和测试已经在当前提交里；只把产品代码撤回去，测试保留
git show HEAD -- <修复涉及的产品文件> | git apply -R
go test ./internal/app -run <测试名>; echo "exit=$?"   # 必须非 0
git checkout -- <修复涉及的产品文件>                    # 恢复修复
go test ./internal/app -run <测试名>; echo "exit=$?"   # 必须为 0
```

汇报里写清楚：撤回时失败的退出码和失败信息，恢复后通过的退出码。

### 3.3 各套件

| 套件 | 命令 | 在 CI 里吗 |
|---|---|---|
| Go 构建和单测 | `go build ./...`、`go test ./...`（`Makefile:12-13`） | 是，`.github/workflows/ci.yml:13-23` |
| UI 类型检查 | `npm --prefix ui run typecheck` | 是，`ci.yml:35` |
| UI 硬编码文案检查 | `npm --prefix ui run test:i18n`（规则见 `ui/src/locales/README.md`） | 是，`ci.yml:36` |
| UI 构建 | `npm --prefix ui run build` | 是，`ci.yml:37` |
| Worker / Guest / 安装器 / 发版脚本（Python） | `make self-host-test`（`Makefile:38-41`，三组 `unittest discover`） | 是，`ci.yml:39-54`，Python 3.10 |
| Shell 语法和 shellcheck | `make self-host-lint`（`Makefile:47-49`） | 是，`ci.yml:56-63` |
| compose 渲染 | `make self-host-config`（`Makefile:51-53`），外加 `docker compose -f compose.yaml config --quiet` | 是，`ci.yml:65-79` |
| 本机 HTTP 验收 | `make acceptance`（`Makefile:30-34`，`scripts/acceptance.py` 起一个一次性实例） | 否 |
| Docker 冒烟 | `scripts/docker-smoke.sh [image]`（一次性容器和卷，只绑 127.0.0.1） | 否 |
| UI 浏览器套件和性能预算 | `npm --prefix ui run test:<名字>`，见 3.4 | **否**，CI 只跑上面四条 UI 命令 |

注意：CI 不跑任何 UI 浏览器测试和性能预算，这些只能在本地跑并按 3.1 汇报。

### 3.4 UI 浏览器套件与 `PLAYWRIGHT_MODULE`

`ui/package.json` 里没有 Playwright 依赖。浏览器测试通过环境变量 `PLAYWRIGHT_MODULE` 加载一个已装好的 Playwright：

- 有的脚本写 `import(process.env.PLAYWRIGHT_MODULE || "playwright")`（例：`ui/scripts/lib/chat-fixture.mjs:14`），没设变量时会因为找不到 `playwright` 直接报错退出（非 0）。
- 有的脚本写 `import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href)`，要求**绝对路径**，没设就 SKIP 并 `exit 0`（见 3.1）。
- 所以统一设成 Playwright 的 `index.mjs` 绝对路径。本机现成的一份在 `../tofibot-web/node_modules/playwright/index.mjs`（`ui/scripts/computer-window-fixture.mjs:55` 把它当默认值）。
- 可选 `CHROME_EXECUTABLE` 指定浏览器（`chat-fixture.mjs` 里 `executablePath`）。
- 性能预算类测试会先用 vite 打一份**生产包**再量（`chat-fixture.mjs:16-20`，理由写在注释里：开发版 React 单帧就能卡 150 ms），所以别拿 `npm run dev` 的手感代替。

批量跑并按退出码汇报：

```sh
cd ui
export PLAYWRIGHT_MODULE=/abs/path/to/playwright/index.mjs
for s in test:composer-typing test:sidebar-pins test:stream-cost; do
  npm run -s "$s" > "$TMPDIR/$s.log" 2>&1; code=$?
  grep -q 'SKIP' "$TMPDIR/$s.log" && skip=SKIP || skip=-
  echo "$s exit=$code $skip"
done
```

按改动范围挑套件（预算和出处见 `AGENTS.md` 各节）：

| 改了什么 | 至少跑 |
|---|---|
| 任何用户可见文案 | `test:i18n` |
| 动效、过渡、`backdrop-filter` | `test:motion-tokens`（字面时长、`cubic-bezier()`、缺 `-webkit-` 都会挂；基线 `ui/scripts/motion-tokens-baseline.json` 只能减） |
| 消息列表、流式输出、滚动 | `test:stream-cost`、`test:timeline-rows`、`test:perf-structure`、`test:arrival-glide` |
| 切换会话 | `test:switch-budget`、`test:chat-switch`，以及 `CHAT_SWITCH_ENGINE=webkit npm --prefix ui run test:chat-switch` |
| 输入框 | `test:composer-typing`（D02：起因是每键 37 ms、66 次排版） |
| 侧栏排序和置顶 | `test:sidebar-pins` |
| 外壳首屏 | `test:shell-budget` |
| 菜单、弹出卡片、层级 | `test:overlays`（含 `OVERLAYS_ENGINE=webkit`）、`test:layers` |
| 加了组件或组件状态、改了组件的 CSS 选择器或 DOM 结构 | `test:catalog`（组件目录页每个状态都还能打开、弹层在最上面、键盘能关；正式构建里仍没有目录页） |
| Safari 相关 | `test:chat-safari` |
| 设置页、自动行为 | `test:settings-shell`、`test:settings-polish`、`test:automation-settings` |
| 问题卡、交接卡、常驻授权卡 | `test:question-poll`、`test:handover-cards`、`test:standing-allowance` |
| 对话框、层级 | `test:dialogs`（`ModalDialog` 的契约、Radix 不进入口包）、`test:layers` |

D02：UI 改动必须量交互开销，预算由测试守住。新做的高频交互要么进已有预算测试，要么新加一个。

### 3.5 其他测试规矩

- 测试只用合成数据（`AGENTS.md:23`）。用户给的真实例子放在隔离的验收测试里，不进产品提示词和模型路由（`AGENTS.md:18-20`）。
- 源码能编译不等于部署成功（`AGENTS.md:23`）。线上是否生效，要按第 7 节在真机上验。
- 改完马上自己造场景实测，不等定时任务（D03）；用全新的 Bot 和群测，不用被旧对话污染的老 Bot（D13）。读 Bot 的回复，判断像不像人话。

## 4. 提示词规则

| 规则 | 出处 |
|---|---|
| 所有由我们写的模型系统提示词和内置 Skill 指令都用英文 | `AGENTS.md:20-21` |
| 提示词里不放例句，不写 "Not X, but Y" 式的对比，不放硬编码关键词列表 | D01 |
| Bot 人设有毛病，改人设生成器（onboarding 里的 `set_bot_profile` 提示词），不去改单个 Bot | D01 |
| 用户的具体例子只进隔离的验收测试，不进产品提示词 | `AGENTS.md:18-20` |
| 改提示词要有评测前后对比，不凭感觉 | `docs/agent-plan/structured-output.md` 第 7、12 节的做法：同一批探针、同样的运行次数，前后各跑一次 |
| 措辞放在哪里比措辞本身更重要，改之前先量 | `structured-output.md:287`（同一句话放在系统文本末尾 45%，放在请求旁边 77-88%） |
| 可靠性原则：带着说明的假设直接交付，不为细节反问；"写"是交出成稿，发送由人来做；群里只显示最终合并结果；说"已核实/最新"必须在本次运行里查过；不认识的名字先查再问 | D09 |
| Bot 的职责范围不是拒绝闲聊的理由 | D07 |

## 5. 评测

有两套评测，用途不同：

| | 回复质量评测 | 任务完成评测 |
|---|---|---|
| 脚本 | `eval/run_eval.py`（在 `main`） | `eval/run_tasks.py`（**只在分支 `eval/task-completion`**，worktree `../tofibot-taskeval`，最新提交 `820121f`；尚未合进 `main`） |
| 测什么 | Bot 让人看到的东西是否正确（结构化输出 `say` 的遵守率、可见文本） | 一句话、信息不全的任务，Bot 能不能一直做到完 |
| 输入 | `eval/probes.json`、`eval/personas.json`、评分标准 `eval/rubric.md` | `eval/tasks.json`（15 个任务）、`eval/task_rubric.md` |
| 跑在哪 | 本地：自己 `go build ./cmd/tofi`，起真实服务器，每轮一个全新数据目录和全新 Bot | 本地模式同左；**正式用远程模式**：`--remote`，只走 HTTP，打在线上产品上 |
| 结果 | `eval/results/<名字>/`（`.gitignore:57` 忽略，不进仓库） | 同左，例：`../tofibot-taskeval/eval/results/prod-sonnet-r3/summary.md` |

### 5.1 回复质量评测 `eval/run_eval.py`

```sh
TOFI_EVAL_KEY_FILE=<密钥文件路径> TOFI_EVAL_ANTHROPIC_WORKSPACE=<workspace id> \
  python3 eval/run_eval.py --models claude-haiku-5-5,claude-sonnet-5-5 --runs 1 \
  --grader-model claude-sonnet-5-5
```

- 选项见文件头 `eval/run_eval.py:9-16`：`--models`、`--runs`（每轮全新数据目录和全新 Bot）、`--probes`（按 id 或类别）、`--grader-model`、`--out`、`--with-computer`、`--no-grade`、`--binary`（`run_eval.py:648-656`）。
- 密钥只从 `TOFI_EVAL_KEY_FILE` 指的文件读，只发给本地服务器的 provider-key 接口和评分请求头，不打印不落盘（`run_eval.py:18-19`、`load_key`）。这个 key 没绑 workspace，必须带 workspace id。**不要读、打印或复制密钥文件内容。**
- `--grader-model` 默认是 `claude-opus-5-5`（`run_eval.py:652`）。按 D12"非必要不用 Opus"，本文建议显式传 Sonnet；评分是否允许继续用 Opus **待确认**（近期结果都用了 Opus 评分，例：`prod-sonnet-r3/summary.md` 第 3 行）。

### 5.2 任务完成评测 `eval/run_tasks.py`（远程模式）

远程模式的设计（`run_tasks.py:23-28`）：

- **只走 HTTP**：不启动服务器、不设 provider key、不改全局模型、不碰数据库；证据全部来自产品 API。
- **每个任务都是全新的 Bot**：通过 `POST /api/bots`（`onboarding: true`，模型固定）经真实人设生成器创建；群任务另开一个全新的群。Bot 和群不跨任务、跨轮、跨模型复用（`run_tasks.py:8-12`，对应 D13）。
- **名字统一加前缀 `评测-`**（`NAME_PREFIX`，`run_tasks.py:528`），线上一眼能认出来，也方便清理。
- **会越出任务范围的卡片一律拒绝**（`run_tasks.py:26`）。
- **按顺序跑**，`--max-cost USD` 预算到了就不再开新任务（`Budget`，`run_tasks.py:58`；跳过的任务记为 `skipped: budget`）。
- **跑完清理**（`r_cleanup`，`run_tasks.py:898-932`）：删掉该任务 Bot 的所有定时任务并复查剩余数，把群和 Bot 归档；任务中途出错也会归档已建的 Bot（`run_tasks.py:1042-1050`）。`--keep` 关闭清理。
- **归档之后删除**：脚本只归档，不删除。删除走产品的 `DELETE /api/bots/{id}` 和 `DELETE /api/conversations/{id}`（`internal/app/deletion.go:278-286`），由协调者在评测结束后对 `评测-*` 执行。这一步目前是否有脚本 **待确认**。

调用（只允许 Haiku 和 Sonnet 当 Bot 模型，D12）：

```sh
cd ../tofibot-taskeval
TOFI_EVAL_KEY_FILE=<密钥文件路径> TOFI_EVAL_ANTHROPIC_WORKSPACE=<workspace id> \
  python3 eval/run_tasks.py --remote https://<server> --cookie-file <cookie jar> \
  --models claude-haiku-5-5,claude-sonnet-5-5 --runs 1 --max-cost <USD> \
  --grader-model claude-sonnet-5-5 --out eval/results/<名字>
```

必须注意：

- `--models` **必须显式传**。默认值含 Opus：`DEFAULT_MODELS = "claude-haiku-5-5,claude-sonnet-5-5,claude-opus-5-5"`（`run_tasks.py:51`）。D12：评测里不用 Opus 当 Bot 模型。
- `--grader-model` 默认也是 Opus（`run_tasks.py:1171`），追问识别也用 Opus（commit `820121f`）。处理同 5.1，**待确认**。
- `--cookie-file` 是登录会话（curl 格式 cookie jar），等同凭据：放在仓库外，不打印，不提交。
- 其他选项：`--tasks`（id 或类别）、`--task-cap`（每阶段等待上限，默认 900 秒，所有模型相同）、`--insecure`（自签证书）、`--no-grade`（`run_tasks.py:1166-1182`）。
- 评测会在线上产生真实对话和模型花费。跑之前按 7.4 看一下当前有没有 owner 自己的运行在跑。

### 5.3 结果怎么存档

`eval/results/` 不进仓库，所以每次评测后把结论抄进 `docs/product/status.md`：日期、被测版本（tag 或 commit）、模型、任务数、完成数、评分模型、结果目录名。现有数字（rc.17、rc.21、rc.26 各模型完成数）在协调者的上下文清单里，已交给 `status.md`。

## 6. UI 预览（不发 rc）

D22：小的 UI 改动用开发服务器连真实服务器预览，不为每个小改动打一个 rc。

只看组件、不需要真实数据时，用组件目录页（D34，只在开发时可用）：`cd ui && npm run dev`，打开 http://localhost:5173/catalog.html 。每个组件的每种状态都是真组件配假数据，切浅色/深色、1280/390 px、7 种语言；页面不调用 `/api`，所以 `dev:remote` 下打开也碰不到服务器。

```sh
cd ui
TOFI_DEV_API_ORIGIN=https://<server>:8321 npm run dev:remote
# 用 Chrome 打开 http://localhost:5173，登录一次
```

- `dev:remote` 就是 `TOFI_DEV_API_INSECURE=1 vite`（`ui/package.json:9`），让代理接受默认安装的自签证书（`ui/vite.config.ts:18-19`）。
- `/api` 和 `/health` 代理到 `TOFI_DEV_API_ORIGIN`，不设时是 `http://127.0.0.1:8321`（`vite.config.ts:14-25`）。UI 改动热更新，数据是那台服务器的真实数据。
- Safari 在纯 http 的 localhost 上留不住 Secure 会话 cookie，所以用 Chrome（`AGENTS.md` "Fast UI preview" 一节）。
- **局域网里另一台设备（比如手机）看预览**：会话 cookie 是 Secure 的，需要 HTTPS。设两个变量指向证书和私钥文件（`vite.config.ts:10-13`，commit `55fb70d`），并让 vite 监听局域网：

  ```sh
  TOFI_DEV_HTTPS_CERT=<cert.pem> TOFI_DEV_HTTPS_KEY=<key.pem> \
  TOFI_DEV_API_ORIGIN=https://<server>:8321 npm run dev:remote -- --host
  ```

  `vite.config.ts` 没配 `server.host`，所以要靠 `-- --host` 才能被局域网访问。证书要让那台设备信任（用什么工具生成 **待确认**）。证书和私钥文件不进仓库。
- 预览连的是线上数据：在预览里发消息、改设置都是真操作。
- 后端改动预览不了，仍然要构建和部署（`AGENTS.md` 同一节）。
- 请 owner 看预览时，写清楚"看哪个页面、做什么操作、应该看到什么"。

## 7. 发版

### 7.1 版本号和节奏

- 标签格式只有两种：`vMAJOR.MINOR.PATCH` 和 `vMAJOR.MINOR.PATCH-rc.N`，其他格式发版流程直接报错（`.github/workflows/release.yml:40-43`）。
- 带 `-rc` 的是预发布：GitHub 上标为 prerelease 且 `--latest=false`（`release.yml:268-270`），`releases/latest` 永远不会给出 rc。
- **攒一批再发 rc**（D22）：改动先合 `main`，能用预览验的先用预览验；需要真机验证的一批攒够了再打一个 rc。
- **正式版 `v*`（不带 rc）要 owner 确认**（D30）。原因之一：开了自动更新的主机会自动装同一 `MAJOR.MINOR` 下的正式补丁版，绝不会装 rc（`auto_update_allowed`，`deploy/self-host/tofi_host.py:1926-1933`），所以正式版会直接推到别人的机器上。问 owner 之前，列出这一批"已验证"和"未验证"的清单。
- GitHub 上只保留 4 个正式版和最近的 rc，旧 rc 的 release、git tag 和容器版本都已清理（D25）。当前标签：`v0.1.0`–`v0.1.3`，`v0.1.4-rc.26`–`rc.29`（`git tag --list 'v*'`）。

### 7.2 打标签前

1. 这一批都已合进 `main`，`main` 上按第 3 节跑完相关套件，退出码都是 0，没有 SKIP。
2. `docs/releases.md` 写好这个版本的条目，和要打标签的提交一起（见第 10 节）。
3. 改了安装器、`deploy/self-host/compose.yaml` 或发版流程中的任何一个，要三者一起改（`AGENTS.md:25-28`）；改了 `deploy/self-host/tofi_tui.py` 要 `make self-host-embed` 重新嵌进 `install.sh`（`Makefile:44-45`）。
4. 安装器在干净的 Linux/KVM 机器上通过 `docs/agent-plan/one-command-install.md` 的验收之前，都按"候选版"对待（`AGENTS.md:25-27`）。

### 7.3 打标签和 CI 发版流程

协调者在 `main` 上打标签并推送：

```sh
git tag v0.1.4-rc.<N>
git push origin v0.1.4-rc.<N>
```

推送 `v*` 标签会触发 `.github/workflows/release.yml`（也可以手动 `workflow_dispatch` 填一个已有标签重建，`release.yml:12-16`）。各 job：

| job | 做什么 | 出处 |
|---|---|---|
| `version` | 校验标签格式，判断是否 prerelease | `release.yml:32-49` |
| `test` | 复用整个 `ci.yml`（Go、UI、Python、shell、compose） | `release.yml:51-53` |
| `images` | 无缓存构建并推送 `ghcr.io/jackzhao98/tofi`、`tofi-worker`、`tofi-mcp-runner`（后者只给开发 compose 用，不进 manifest），写入 `TOFI_SOURCE_COMMIT` 和 `TOFI_VERSION`；检查镜像标签 `io.tofi.data-schema`、`io.tofi.account-runtime`、`io.tofi.account-worker`、`io.tofi.account-guest-protocol` | `release.yml:55-143` |
| `guest` | 构建 Guest 二进制，组装封装好的账户 Guest 发行版并用 `account_release_check.py` 校验，打成 `tofi-guest-<tag>-x86_64.tar.zst`（稀疏 tar + zstd）和 `.sha256`；上限 120 分钟 | `release.yml:145-192` |
| `bundle` | `scripts/release/make_bundle.sh` 打主机包 `tofi-host-<tag>.tar.gz`（可复现）；Caddy 按 digest 固定；`scripts/release/make_manifest.py` 生成 `manifest.json`（App/Worker 按 digest 固定） | `release.yml:194-237` |
| `publish` | 检查两个镜像能匿名拉取，否则报错停止；`gh release create` 上传 `manifest.json`、`install.sh`、主机包、Guest 包和校验文件 | `release.yml:239-279` |

看进度：`gh run list --workflow release.yml`、`gh run watch <run id>`。`publish` 因镜像不公开失败时，按 `deploy/self-host/README.md` "Publishing a release" 一节把包设为 Public 后重跑失败的 job。

镜像里的版本号来自构建参数：`Dockerfile:15-16` 把 `TOFI_VERSION` 写进 `internal/app.Version`（`internal/app/update_notice.go:21`）。

### 7.4 部署到服务器（只由协调者做）

部署前**必须先查有没有正在跑的运行**，然后再切版本。曾经有一次部署打断了 owner 正在进行的提问。

`tofi update` 自带运行检查：

- 它统计每个 App 数据库里状态为 `queued`、`running`、`waiting` 的运行（`ACTIVE_RUN_STATES`，`tofi_host.py:134`；`active_runs`，`tofi_host.py:1355`，只读打开）。`waiting` 包括停在问题卡上等人回答的运行，问题卡默认 30 分钟才过期（`AGENTS.md` "Asking a human" 一节）。
- 有运行在跑时，在终端里默认等 10 分钟，带 `--yes` 或非终端时不等；每 15 秒查一次；等完还有就拒绝，并提示"Nothing was changed"（`wait_for_idle`，`tofi_host.py:1376-1400`；参数说明 `tofi_host.py:3700-3702`）。
- 下载完、停服务之前会再查一次（`tofi_host.py:2437`）。
- `--force` 会打断正在跑的运行。**没有 owner 同意不用 `--force`。**

步骤：

```sh
sudo tofi status                                        # 当前版本、服务、健康状况、每台账户电脑
sudo tofi update --check --version v0.1.4-rc.<N>         # 只报告，不改动；退出码 0=已是最新，10=有更新，1=出错
sudo tofi update --version v0.1.4-rc.<N>                 # 装 rc 必须带 --version；不带时只取最新正式版
```

- 不带 `--version` 时取的是 `releases/latest`，也就是最新正式版，永远拿不到 rc（`manifest_url`，`tofi_host.py:502-505`）。
- 更新过程：停服务 → 备份数据 → 切配置和版本 → 启动新版；新版起不来就恢复备份、回滚到原版本（`upgrade` / `_upgrade`，`tofi_host.py:2366-2475`）。结果写进 `/var/lib/tofi/update-history.jsonl`（`deploy/self-host/README.md:326`）。
- 数据结构（镜像标签 `io.tofi.data-schema`）变了时，`tofi update` 会拒绝，要加 `--allow-schema-change`（`tofi_host.py:2425-2427`）。遇到这种情况停下来问 owner。这是本文的建议，还没有 owner 决定，**待确认**。

### 7.5 部署后验证

1. `sudo tofi status`、`sudo tofi version`：版本是新标签，服务都健康。
2. 以管理员身份 `GET /api/system/update`，返回里的 `current` 等于新标签（`internal/app/update_notice.go:75`，路由 `internal/app/app.go:2904`）。
3. 打开网页，确认能登录、能加载会话。
4. 针对这一批改动，在线上**新建** Bot 或群实测（D03、D13），亲自读回复。
5. 需要量化的，跑第 5 节的任务完成评测，结果记进 `docs/product/status.md`。
6. 有电脑仍挂在旧 Guest 快照上时，`tofi update` 会提示用 `sudo tofi computers upgrade --all` 切换（`tofi_host.py:2470-2473`）。

`tofi update` 失败已自动回滚时，`sudo tofi status` 会显示 `Update` 一行和恢复建议（`deploy/self-host/README.md:335`）。

## 8. 安全审查闸

D30：**放宽安全的改动，合并前必须经过一次对抗式审查**，审查者不能是写这段代码的同一个执行者。

哪些算"放宽安全"（只要碰到其中之一就要过闸）：

| 范围 | 代表文件 |
|---|---|
| 登录、会话、限流、密码 | `internal/app/owner_auth.go` |
| 问人和审批：问题卡、表单、私密输入、"必须有人在场"（`requireHumanPresent`） | `internal/app/questions.go`、`internal/app/handover.go` |
| MCP 工具分级、审批闸、自动审查、常驻授权 | `internal/app/mcp_tool_tiers.go`、`mcp_call_approval.go`、`mcp_standing_allowance.go` |
| 密钥和保险库、导入导出里的密钥 | `internal/app/secrets.go`、`portability_secrets.go` |
| 扩展、网络访问范围 | `internal/app/extension_gate.go` |
| CSRF、Origin 检查、cookie | `internal/app/app.go:2753`、`2797-2807`（`originOK`） |
| 安装器的权限和隔离 | `deploy/self-host/worker.apparmor.template`、`worker.seccomp.json`、`install.sh`、`tofi_host.py` |
| 发版流程的权限 | `.github/workflows/release.yml`（`permissions`） |

怎么过闸：

1. 另起一个审查者（本地子代理，或 `/security-review`、`/cso` 这类审查工具），给它分支的 diff，要求它从攻击者角度找绕过办法：隐藏会话、定时运行、模型自己构造的输入、改了定义后的旧授权、并发和重启。
2. 每条成立的发现都修掉，并按 3.2 配一个回退会挂的测试。修复单独成提交，信息里写"close review findings"。例：`5592eb3 Standing allowance: close review findings (hidden traces, tool binding, tier revoke, auto-mode reviewer, audit)`，以及 `dd11fb4`、`775fc0e`。
3. 合并提交前，在 `docs/decisions/` 或该功能的 spec 里写下审查结论和剩下的已知问题（例：`AGENTS.md` 写明的已知问题"`computer_shell` 与共享 Chrome DevTools 端口共用 uid 和回环地址"）。
4. 不经审查不合并。审查找出的问题没修完也不合并。

## 9. 模型使用

### 9.1 产品默认

| 用途 | 默认模型 | 出处 |
|---|---|---|
| 对话、定时运行、工具调用等所有 agentic 工作 | `claude-sonnet-5-5` | `internal/app/automation_settings.go:47-49`，D14 |
| 后台轻活：摘要、群聊要点、分诊、审查 | `claude-haiku-5-5` | `automation_settings.go:52`，D14 |
| 运行中途不按触发条件换模型 | — | D14 |

每个自动行为都要在 设置 › 自动行为 里有可见、可调的开关和默认值（D15）。

### 9.2 评测

- Bot 模型只用 Haiku 和 Sonnet，不用 Opus（D12）。两个脚本的默认值都含 Opus，必须显式传参（见 5.1、5.2）。
- 评分模型用什么 **待确认**（见 5.1）。

### 9.3 开发时派工

| 活 | 模型 | 出处 |
|---|---|---|
| 写代码的子代理 | Sonnet | D12 |
| 收集信息的子代理（读代码、查资料，附出处） | Haiku | D12 |
| Opus | 非必要不用；owner 点名要求时才用 | D12 |
| 计划、验收、合并、线上操作 | 协调者自己 | D30 |

## 10. 文档跟着改

D27：文档就是这套系统。**每次合并、发版、owner 拍板，都在同一个提交里更新对应文档。** `AGENTS.md` 是入口。

| 发生了什么 | 同一提交里要改 |
|---|---|
| 合并一个分支进 `main` | `docs/product/roadmap.md`（挪到"已完成"或更新"进行中"）；改了实现约定的，同步改 `AGENTS.md` 对应一节；有 spec 的更新 spec 的状态行（例：`docs/agent-plan/structured-output.md` 第 3 行） |
| 打 rc 或正式版标签 | `docs/releases.md` 加这个版本的条目，写在被打标签的那个提交里；`docs/product/status.md` 更新"当前线上版本" |
| 部署到线上 | `docs/product/status.md` 的线上版本和日期（时间写太平洋时间） |
| owner 拍板 | `docs/decisions/` 新增一个文件：日期、原话要点、为什么、影响到哪些代码；被推翻的旧决定标"已被 Dxx 取代"，不删 |
| 跑完一次评测 | `docs/product/status.md` 的评测表（第 5.3 节要求的字段） |
| 改了工作流程（测试、发版、审查、模型分工） | 本文 `docs/process.md` |
| 改了设计规范或组件 | `docs/design/` 对应文件 |
| 文档过时 | `docs/README.md` 的索引里标"已过时"，写明被哪份取代 |

合并或发版前的清单：

- [ ] 测试按 3.1 用退出码证明，没有 SKIP；修复有回退会挂的测试（3.2）
- [ ] 碰到第 8 节范围的，已过安全审查，发现都已修复
- [ ] 提示词改动有评测前后对比（第 4 节）
- [ ] `docs/product/roadmap.md` 已更新
- [ ] 有新决定的，`docs/decisions/` 已加文件
- [ ] 发版的，`docs/releases.md` 已加条目；正式版已得到 owner 确认（7.1）
- [ ] 实现约定变了的，`AGENTS.md` 已同步
- [ ] 文档里的行号、commit、命令都是在这次提交上核对过的
- [ ] 文档里没有服务器地址、账号和密钥

## 11. 待确认

| 事项 | 说明 |
|---|---|
| 评测的评分模型 | 两个脚本默认用 Opus 评分（`eval/run_eval.py:652`、`run_tasks.py:1171`），近期结果也是 Opus 评分；D12 只明确禁止 Opus 当 Bot 模型。是否改成 Sonnet 评分需要 owner 定 |
| 删除 `评测-*` Bot | 脚本只归档（`run_tasks.py:898-932`）；删除是否有固定脚本未查到 |
| 任务完成评测并入 `main` | `eval/run_tasks.py` 只在 `eval/task-completion` 分支上，那个 worktree 里还有未提交的 `eval/run_eval.py` 改动 |
| 数据结构变更的更新 | `--allow-schema-change` 是否每次都要 owner 同意，还没有决定 |
| 局域网 HTTPS 证书 | 用什么工具生成、怎么让手机信任，没有记录 |
| 一键安装的最终验收 | 干净 Linux/KVM 验收（`docs/agent-plan/one-command-install.md`）是否已通过，本文未核对 |

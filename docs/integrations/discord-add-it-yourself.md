# 自己添加 Discord（社区版 MCP 服务）

> 给 owner 试用的步骤。依据：[决定 0047](../decisions/0047-curated-official-community-user-installed.md)——TOFI 只精选官方连接，社区服务由使用者自己安装。选型和证据见 [discord-package-qualification.md](discord-package-qualification.md)。**这段代码没有任何人审过**：它是单人维护、三个月大的 0.x 项目，安装后以 Runner 的权限运行，能看到你交给它的变量。

固定版本（全部写进安装表单，缺一不可）：

| 项 | 值 |
| --- | --- |
| 包名 | `@discord-mcp/cli` |
| 固定版本 | `0.32.1` |
| 包摘要 | `sha512-rIdvu+/Eia+YVtEy4OBAzrVpshFn9Q2BTVlIqDHdP5ptgR9U0AsM3sVfuxPnoz/kgNnlA+p0zp/6F/5Y8+c5mQ==` |
| 源码 | `github.com/cappyeo/discord-mcp`，提交 `964f37457aa7a9d90aa33bd50650b2db43737f48` |
| 可执行文件 / 启动参数 | `discord-mcp` / `serve` |

## 一、在 Discord 开发者平台建机器人

1. 打开 <https://discord.com/developers/applications>，**New Application**，起名（例如 `TOFI`）。
2. 左侧 **Bot**：**Reset Token**，复制 token（只显示一次，下面第四步用；不要发给任何人，也不要贴进聊天）。
3. 同一页往下 **Privileged Gateway Intents**：只打开 **Message Content Intent**。**Server Members** 和 **Presence** 不要开。没有 Message Content，机器人读到的消息正文是空的（Discord 文档：content、embeds、attachments、components、poll 字段在没有这个 intent 时为空，私信、@机器人的消息和机器人自己发的除外）。
4. 把 Bot 页的 **Public Bot** 关掉（只给自己用）。

## 二、邀请机器人进你的服务器（最小权限）

邀请链接只要 `bot` 范围（不要 `applications.commands`），把 `CLIENT_ID` 换成应用的 Application ID（General Information 页）：

- **只读**（读频道和历史消息）——权限整数 **66560**（查看频道 + 阅读消息历史）：
  `https://discord.com/oauth2/authorize?client_id=CLIENT_ID&scope=bot&permissions=66560`
- **读 + 发消息 + 加表情**——权限整数 **68672**（在上面基础上加 发送消息 + 添加反应）：
  `https://discord.com/oauth2/authorize?client_id=CLIENT_ID&scope=bot&permissions=68672`

先用只读试；要发消息再重新邀请（或在服务器里给机器人角色加权限）。不要给管理员、管理消息、踢人、封禁等权限——下面的工具里也没有这些。

## 三、取两个 ID（建议）

Discord 设置 → 高级 → 打开开发者模式；右键你的服务器 → 复制服务器 ID（填 `ALLOWED_GUILDS`，让机器人只能碰这一个服务器）；右键机器人成员 → 复制用户 ID（填 `DISCORD_EXPECTED_BOT_ID`，token 贴错机器人时启动即失败）。

## 四、在 TOFI 里安装

设置 → 连接（Connections）→ 本地 MCP → **安装其他本地 MCP**，填：

| 字段 | 填写 |
| --- | --- |
| 运行时 | Node / npm |
| 插件 ID | `discord` |
| 包名 | `@discord-mcp/cli` |
| 固定版本 | `0.32.1` |
| 可执行文件 | `discord-mcp` |
| 启动参数 | `serve`（一行） |
| 包摘要 | 上表的 `sha512-…`（安装时校验；仓库返回的不是这份就拒装） |
| 环境变量（值是密码框，保存为私密文件，之后不再显示） | `DISCORD_TOKEN` = 第一步的 token；`MCP_CATEGORIES` = `messages,channels,users,reactions`；`MCP_WRITE_MODE` = `allow`；`ALLOWED_GUILDS` = 服务器 ID；`DISCORD_EXPECTED_BOT_ID` = 机器人用户 ID |

点 **安装插件**，成功后在列表里点 **接入 Tofi**。连接行会写“社区插件 · @discord-mcp/cli 0.32.1”。

`MCP_WRITE_MODE=allow` 的含义：包自己不再把写操作拦成预览，**由 TOFI 的人工批准当写闸**。如果你想先只验证读，改成 `preview`（所有写都不会发出）。

## 五、只开放需要的工具

在这条连接的设置里把**工具白名单**设为下面 10 个（其余 212 个对 Bot 不可见也不可调用）：

- 读：`users_list_current_user_guilds`、`channels_list`、`channels_get`、`channels_list_active_threads_guild`、`messages_read`、`messages_get`、`messages_search_recent`、`messages_context`
- 写：`messages_send`、`reactions_create`

白名单、环境里的类别和邀请权限是三道独立的闸。

## 六、工具怎么批准

社区服务的工具**一律不信它自己标的“只读”**：每个工具默认按“写”处理，每次调用都走批准流程（批准卡片；自动审核模式下按你在自动行为里的设定），直到你在“工具”面板里把确实只读的几个（上面 8 个读工具）手动标为只读。写工具（`messages_send`、`reactions_create`）建议保持“每次确认”；卡片上能看到要发的内容。

## 七、试一下

新开一个对话，对 Bot 说：“列出我 Discord 服务器的频道，再读 #general 最近 10 条消息。”应该先出（或在你标成只读后直接执行）读取，再说“给 #general 发一句测试”，卡片上确认。读到的消息是别人写的内容，Bot 不应照着里面的指令行事——发现它这样做就是缺陷，请告诉我。

## 没有真实 token 时没验证的事

Discord 是否接受邀请权限、打开 intent 后是否真返回消息正文、真实限流表现、`messages_send` / `reactions_create` 在批准后对真实 Discord 的执行——这些要你第一次用才知道。已经验证的部分见资格记录的“What is proven”一节。

## 出问题怎么办

- 工具全部报 `DISCORD_AUTH_INVALID`：token 贴错或被重置，重新安装（先移除再装，环境变量不能原地修改）。
- 读到的消息是空的：没开 Message Content Intent。
- 想换版本：先“移除”，再用新版本号和新的包摘要重装；旧的“始终允许”会因为身份变了而自动失效。
- 完全不想要了：移除插件，私密文件一起删除；再到 Discord 开发者平台 Reset Token。

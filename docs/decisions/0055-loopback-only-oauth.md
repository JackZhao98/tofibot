# 0055 只接受 127.0.0.1 回调的服务：网页端用“粘贴回填”，不再走公网回调

> 编号 D55，索引见 [README](README.md)。承接 [0047](0047-curated-official-community-user-installed.md)（目录只收官方连接器）和 Robinhood 的接入记录（[plugin-library-mainstream.md §4](../agent-plan/plugin-library-mainstream.md)）。

## 决定

owner 2026-10-22：Robinhood 的 OAuth 只接受 `127.0.0.1` 回调（原生/命令行客户端的做法）。网页端在 https 下一直登记的是公网回调 `https://<public-origin>/api/extensions/mcp/<name>/oauth/callback`，所以登录停在“等待授权”。owner 拍板：**最稳当的就是用 127.0.0.1，然后把链接粘贴回来**——不依赖 Bot 电脑，每个自托管都能用。

1. **连接自带一个属性，不按名字写分支。** `OAuthConfig.RedirectPolicy`（`oauth.redirect_policy`，唯一取值 `"loopback"`）。目录条目写 `oauthRedirect: "loopback"`（`ui/src/integrationCatalog.ts`，目前只有 Robinhood），保存连接时随 `oauth` 一起存。含义：这个服务只接受 `http://127.0.0.1` 回调，TOFI 不为它登记、也不使用别的回调。未知取值保存时拒绝；编辑时漏写不会丢掉它（同一地址下继承旧值）；改了地址就是新服务，不继承。
2. **通用兜底，不靠目录。** 任何自定义 MCP，只要动态客户端注册用公网回调时被服务方以**错误码** `invalid_redirect_uri`（RFC 7591 §3.2.2，`oauthex.ClientRegistrationError.ErrorCode`，不看描述文字）拒绝，`OAuthStart` 就把这个连接记为 `loopback`，返回 `ErrOAuthLoopbackRequired`，网页起始接口回 `409 oauth_loopback_required`，界面随即改走粘贴回填。选择已持久化，下次直接走回填。别的注册错误不触发（不会因为措辞误标）。
3. **三条路线怎么选**（`ui/src/mcpOAuthRoute.ts`，`mcpOAuthRoute(..., loopbackOnly)`）：
   - 原生客户端：不变，仍是自己的 `127.0.0.1:43821` 监听（`desktopOAuthRedirect`）。
   - 网页：`loopbackOnly` 时一律 **粘贴回填**，不管是 https 还是 http、有没有共享电脑、有没有公网地址（不需要 `options`）。**不再**走 https 弹窗，也不再优先用共享电脑（owner 在中途改了方向：共享电脑做主路径依赖 Bot 电脑，回填对所有安装都成立）。服务端的共享电脑接口（`vm_oauth.go`）原样保留，它的回调本来就是 `127.0.0.1`，仍能通过 `OAuthStart` 的环回校验；只是网页端不再为这类服务选它。
   - 其他服务：完全不变（https 弹窗 / http 用共享电脑 / 被挡）。
4. **粘贴回填的流程。** 点“授权”→ 在点击里同步打开一个标签页（之后才被对话框指向服务方授权页；被拦截时对话框里仍有“打开登录页”按钮）→ 服务端 `POST /api/extensions/mcp/<name>/oauth/paste/start` 用 `http://127.0.0.1:43821/oauth/callback` 登记并开会话 → 用户登录后浏览器落在一个打不开的 `127.0.0.1` 页 → 复制地址栏里的完整地址，粘到对话框 → `POST …/oauth/paste/complete {session_id, url}` → 服务端校验后换取令牌。固定端口沿用桌面端的 `43821`：同一个动态注册的客户端可以在桌面和回填之间复用（`DynamicClientRedirectURI` 相同就不重注册）。
5. **界面。** 连接行在需要授权/等待时多一行安静的路线说明（`extensions:route.loopback_paste`，七种语言），在连接对话框的“登录方式与限制”里也是同一句。等待授权的大插图（TOFI 方块、虚线弧、深色钥匙块）整个删除，只留状态文字和“继续授权”主按钮上一圈呼吸光环（`.mcp-button[data-pending]::after`，只用 transform 和 opacity，`--ease-inout`，减少动效时静止）。

## 安全评审（流程 §8）

- **状态绑定。** 回填会话就是 `OAuthStart` 建的那一个：`state` 是 32 字节随机、常量时间比较；会话 10 分钟过期、一次性（成功、拒绝或 `iss` 不符都会删除；`state` 不符则不消耗，允许重粘）；会话还绑定服务名和回调地址，换服务、换渠道（桌面完成接口与网页完成接口各自的回调地址不同）都不能互相完成。粘贴的 `state` 必须与会话一致，不信任用户提供的任何别的值。
- **PKCE。** 始终 S256；`code_verifier` 只在服务端内存里，随授权码一起发给令牌端点，服务方验证。DCR 客户端是公共客户端（`token_endpoint_auth_method: none`）。
- **`iss`（RFC 9207）。** 服务方元数据里 `authorization_response_iss_parameter_supported` 为真时（Robinhood 为真），粘贴的地址必须带 `iss` 且与元数据的 `issuer` 完全相同（常量时间比较）；缺失或不符一律拒绝并**销毁会话**（混淆攻击的特征）。不声明的服务不要求。只在回填路径做；其他路线不变。
- **从不登记非环回回调。** 三层：`OAuthStart` 在登记前对 `loopback` 连接用 `ValidateLoopbackRedirect` 校验（仅 `http`、主机字面等于 `127.0.0.1`，不含用户信息、查询串、片段；`localhost`、`[::1]`、`127.0.0.1.evil.example` 都拒绝）；`StartOAuth` 和 `OAuthHandler.RegisterClient`/`config` 各自再校验一遍（`OAuthFlowConfig.LoopbackOnly`）；网页 https 起始接口对这类连接直接 `400 oauth_loopback_only`，一次登记请求也不发。
- **回填解析的上限**（`parsePastedRedirect`，`internal/app/oauth_paste_back.go`）：整段 ≤ 4096 字节，不含空白/控制字符（首尾空白先修剪）；必须是 `http`，不含用户信息/片段/opaque，主机（含端口）与路径与登记的回调**逐字相同**（不是前缀、不是 `localhost`、不是公网地址）；必须有查询串；`state`（≤ 512）必填，`code`（≤ 2048）必填（除非服务方回了 `error`，此时按“拒绝”处理，`error` ≤ 128），`iss`（≤ 512）；这几个参数重复出现即拒绝；其余参数一律忽略、不保存。授权码只取自这一个 URL。
- **不回显、不记录。** 错误体只有固定码（`oauth_paste_invalid`、`oauth_denied`、`oauth_issuer_mismatch`、`oauth`）和固定英文句，从不含用户粘贴的任何字符；服务端不对该 URL、`code`、`state` 写日志、事件、表或结果（测试抓取 `log` 输出确认）；前端不保存也不拼进消息，成功、取消、被拒后输入框清空；输入框 `autocomplete=off`。工作区事件只带失效范围。
- **闸与认证。** `…/oauth/paste/start|complete` 与桌面完成接口一样需要所有者会话（`ownerIndependentRoute` 里没有它们，有测试）；只对 `loopback` 连接开放（其他服务 `400 oauth_paste_not_needed`）。
- **残余风险。** 回填把授权码交给了用户的浏览器地址栏——这正是原生/命令行客户端的标准做法（RFC 8252），码一次性、有 PKCE 绑定、会话 10 分钟；若用户把完整地址贴到别处，攻击者没有 `code_verifier` 无法换取。自动兜底只信注册错误码，不信文字；服务方若返回别的错误码，需要在目录里手动标 `oauthRedirect`。Bot 自己创建的 MCP（`manage_extensions`）没有这个属性，除非走兜底；目录以外的 URL 不按主机名猜。

## 未在线上核实

- Robinhood 实际接受的回调形式：只确认了服务方“只收 127.0.0.1”（owner 所述）和元数据；**没有**对它的注册/授权端点发过任何请求。端口 `43821` 是否被接受、路径 `/oauth/callback` 是否被接受、`iss` 是否真的出现在回跳地址里，都要用真实账户走一遍。
- 回跳页在各浏览器的表现（Safari 对 `127.0.0.1` 的 http 页、浏览器拦截弹窗的措辞）。
- Agentic 账户权限范围与工具列表（原本就待核实）。

## 影响的代码

`internal/extensions/management.go`（`RedirectPolicy`、`ValidateLoopbackRedirect`、`OAuthStart` 的校验与兜底、`markLoopbackOnly`、`OAuthLoopbackOnly`、`OAuthFlow.Issuer`）、`oauth_transport.go`（`LoopbackOnly`、元数据里的 `iss` 声明、`IssuerCheck`）、`oauth_callback_service.go`（`OAuthPasteCallback`）、`internal/app/oauth_paste_back.go`、`extension_management.go`；`ui/src/mcpOAuthRoute.ts`、`MCPSettings.tsx`、`connections/PasteBackDialog.tsx`、`connections/RowView.tsx`、`connections.css`、`HandoverCards.tsx`、`OnboardingServices.tsx`、`integrationCatalog.ts`；删除 `ui/src/oauth-link-motion.css`。

## 测试

- Go：`internal/extensions/oauth_loopback_test.go`（环回连接只登记/使用环回回调；目录属性读回；其他服务不变；策略校验与编辑保留）；`internal/app/oauth_paste_back_test.go`（`TestParsePastedRedirectIsStrict`、`TestLoopbackOnlyConnectionRoutes`、`TestRegistrationRefusalOfPublicRedirectFallsBackToLoopback`、`TestPasteBackChecksIssuerWhenAdvertised`、`TestPasteBackRoutesRequireOwnerSession`）。
- 界面：`test:mcp-oauth`（路线）、`test:connections`（7f：回填对话框、不回显、取消、不开 https 弹窗；其他服务不变）、`test:catalog`（待授权行无插图、光环呼吸/减少动效静止、路线说明）、目录状态 `connection-waiting-loopback`、`connection-needs-auth-loopback`、`paste-back*`、`connect-robinhood`。

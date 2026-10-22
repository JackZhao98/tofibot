# 0048 `tofi security audit`：只读、只看主机能看到的事实，FAIL 只留给直接泄密或登录失效

> 编号 D48，索引见 [README](README.md)。**这是执行者在任务 S2 里做的取舍，不是 owner 的原话；owner 尚未确认，待确认。** 若与别的分支撞了编号，合并时顺延。

## 决定

1. **只读，且不联网为默认。** 命令不拿生命周期锁、不调用 `load_state()`（它会整理被打断的还原）、不写版本缓存（`latest_release()` 会写）。最新版本默认读 `/var/cache/tofi/latest-release.json`，没有就报 WARN"未知"；`--online` 才用 `fetch_manifest` 现查，也不写缓存。
2. **只检查代码和主机文件能证实的事。** 不探测公网可达性（看不到路由器和防火墙），只在监听 `0.0.0.0` 且某网卡有全球可路由地址时 WARN；不探测运行中的电脑（要问 Worker，留给 `tofi computers`）；登录限流只能确认 `compose.yaml` 开了 `TOFI_OWNER_AUTH=1` 和受信代理配置，限流本身是否生效要在线上实测。
3. **等级口径。** FAIL = 机密被放宽读取（密钥、保险库、备份、`tofi.env`、TLS 私钥的权限或属主不对，或为符号链接）、明文 HTTP 暴露在非回环地址、证书过期或缺失、限流关闭或受信代理过宽（可伪造 `X-Forwarded-For`）、保险库里有未封存或明文的模型密钥、固定的 Guest 版本不在盘上。WARN = 风险但不是已经泄露：管理员还没建、疑似密钥写在 `tofi.env`、版本落后或未知、休眠电脑在旧 Guest、没有近 7 天的备份、监听面有公网地址、LAN 明文登录开着、证书 30 天内到期。
4. **退出码。** 0 无 FAIL（WARN 不影响）；2 至少一条 FAIL；1 命令没能运行。选 2 而不是 1，是为了让调用方分得清"体检发现问题"和"体检没跑成"（`update --check` 已把 10 用作"有更新"，1 为出错）。
5. **不输出密钥值。** 结果里只有名字、个数、路径、权限位和版本；测试用金丝雀值断言文本与 JSON 都不含它们。

## 日期

2026-10-22（太平洋时间）。

## 谁拍板

执行者（任务 S2）提出；owner 待确认。

## 理由

- 同类参照 `openclaw security audit`：一份 PASS/WARN/FAIL 清单加修法，自托管者能自己照着改。
- 只读加不联网，让它可以放心在生产上随时跑，也能离线跑。
- 不发明代码证实不了的检查：每一项都能指到 `tofi_host.py` 里读到的文件或字段。

## 影响

`deploy/self-host/tofi_host.py`（`security_audit` 及 `audit_*`、子命令 `security audit`）、`test_security_audit.py`、`deploy/self-host/README.md` 命令表、`docs/process.md` §7.7。7 天备份阈值（`BACKUP_STALE_DAYS`）和"监听面有公网地址即 WARN"是执行者定的默认，可调。**需要真机验证：** 权限属主的期望值（根/10001）与真实安装是否一致、公网地址判定、证书类型输出、保险库文件名与 JSON 字段。

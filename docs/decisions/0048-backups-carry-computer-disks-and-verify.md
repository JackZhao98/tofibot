# 0048 手动备份带上电脑磁盘，自动备份不带；备份可以不还原就验证

> 编号 D48，索引见 [README](README.md)。

## 决定

1. **`tofi backup` 默认带上每台账户电脑的工作区磁盘**（`/var/lib/tofi/worker/state/<账户>/workspace.ext4`）。备份目录里多一个 `disks/<账户>.ext4.zst`：对稀疏文件直接 `zstd`（零区间压得几乎为零），还原时 `zstd -d --sparse` 展开回稀疏文件。`meta.json` 里多一个 `disks` 列表（账户、标称大小、实占、压缩后大小、压缩文件的 sha256、是否已休眠）。`--no-disks` 可以不带。没有装 zstd 时带磁盘的备份直接报错并提示 `--no-disks`。
   - **owner 2026-10-22 的决定：“压缩 + 带磁盘的只留最近 2 份”。** 压缩：选 zstd，没有保留 reflink 快路径（两条路径会让代码和验证都翻倍，而 reflink 不省空间，只省时间）。保留：带磁盘的备份只留最近 2 份；做第 3 份时，把最旧那份带磁盘备份里的 `disks/` 删掉、`meta.json` 去掉 `disks`，它的数据、设置、Worker 归档作为不带磁盘的备份留下（`prune_disk_backups`），并打印删了什么。不带磁盘的备份不算在这 2 份里，按原规则（手动的不删）。
   - **空间估算**：备份前打印 “Space estimate”：数据和设置的大小，加上磁盘实占 × 假定压缩比 0.5（`DISK_COMPRESSION_RATIO`，是猜的，不是实测，输出里明说是估算）。可用空间不够（2 倍数据和设置 + 1 GiB + 预计磁盘压缩后大小）就拒绝，信息点名 `--no-disks`。
2. **自动备份（`pre-update`、`pre-restore`）永远不带磁盘。** 它们每次更新都做、只留 3 份；磁盘动辄几十 GiB，每次更新都复制既慢（停机时间变长）又吃盘。
3. **`tofi backup --verify [ID|all]`**：不还原、不停服务，检查一份备份（默认最新）：meta.json 能读、每个归档和磁盘都在、大小和 sha256 对得上、每个归档从头到尾能读完（tar 结构和压缩流）、每个压缩磁盘通过 `zstd -t`、`tofi.env` 在且可用、没有 meta.json 没列出的多余文件。任何一项不过，列出全部问题，退出码 1。`tofi backup` 做完后会自动对刚写的那份跑一遍同样的检查。
4. **`tofi restore <ID>` 早就有**（见下“现状”）。这次补的：备份带磁盘时默认也还原磁盘（`--no-disks` 不还原）；被换下的活磁盘改名为 `workspace-before-restore-<ns>.ext4` 留在原目录，不删；还原失败回滚时把它们换回来；那台电脑的休眠快照属于旧磁盘，还原后丢弃（下次用时冷启动，开着的页面没了）。
5. **不静默的前提**：只在服务停止后复制磁盘。Worker 停止时 manager 会让每台电脑休眠，磁盘已落盘（`deploy/microvm/manager.py` 的 `hibernate_for_exit`，`main` 末尾）。复制前再确认两件事：没有 TOFI 容器还在跑；每个账户目录里的 `manager.lock`（manager 运行期间持有的 flock）能拿到。任何一件不成立就拒绝，不复制。

## 日期

2026-10-22（太平洋时间）。

## 谁拍板

owner 定的是优先级（“自托管第一，备份要完整、可验证”，协调者转述，原话没有逐字存档）。默认值（手动带、自动不带）、拷贝方式和验证项是实施者按下面的理由选的，**待 owner 确认**。

## 现状（改动前，`HEAD` = 61f4c3a，`deploy/self-host/tofi_host.py`）

- 备份内容：`data.tar.zst|gz`（`/var/lib/tofi/data`）、`etc.tar.gz`（`/etc/tofi`，不含安装日志）、`worker-meta.tar.gz`（Worker 的 `config`/`ledger`/`state` 下的小文件，64 MiB 以上和磁盘、快照一律跳过）、`meta.json`（版本、镜像、Guest、时间、原因、大小、sha256）。`backup_data` 1603–1654；跳过规则 1487–1492。镜像本身不在备份里，只记名字，还原时重新拉。
- 位置和保留：`/var/lib/tofi/backups/<UTC 时间>-<版本>/`，0700（`Paths.backups`，190）；自动备份留 3 份（`BACKUP_KEEP`，130；`prune_backups`，1723）；`manual` 不删（`remove_backup`，`tofi backups --remove`）。
- 电脑磁盘：不在里面。注释明说“never part of a backup”（1489–1490）。
- 命令：`tofi backup`、`tofi backups`、`tofi restore` 都已存在（解析 3729–3737，分派 3883–3901；`backup_now` 2590，`restore_backup` 2508，含运行检查 `wait_for_idle` 1376、更新前先做 `pre-restore` 备份、失败回滚）。`safe-updates.md` 把它们写成“计划”是过时的。
- 缺的：没有 `--verify`。校验只在还原前和回滚时做（`verify_backup`，1673：只比对 sha256，不读归档内容）。

## 理由

- 磁盘大小：manager 允许每台电脑 8–1024 GiB（`manager.py:324`、`:431`，默认 8），文件是稀疏的，实占远小于标称。本机（Mac）没有真实部署，**没有实测，只有上界**；真实大小要在安装测试 VM 上用 `du -sh /var/lib/tofi/worker/state/*/workspace.ext4`（实占）对比 `ls -l`（标称）。不论实占多少，每次更新都复制 N 台电脑的磁盘不合理，所以自动备份不带。
- 手动备份是 owner 想要“完整”的时刻，默认带；空间不够时，拒绝信息点名 `--no-disks`。空间检查把磁盘实占算进需要的空间（一份，不压缩）。
- 拷贝方式：最初用 `copy_sparse`（不压缩），owner 要求压缩后改为 zstd。zstd 要读完整个标称大小（零区间很快，但 1 TiB 的稀疏盘仍要读 1 TiB），停机时间随标称大小增长，**待真机测**。
- 不用“运行中快照”：磁盘一致性靠电脑休眠，`manager.lock` 加容器检查是证明它真的停了；既有休眠机制已经给出这个保证，不再造第二个。
- 被换下的磁盘不删：还原是破坏性的，owner 的原则是留得住的状态不静默丢。代价是占盘，输出里提示手动删。

## 影响

实施状态：分支 `night/s1-backup-verify`，未合并，**未在真机验证**。

- 带磁盘的备份只留最近 2 份，第 3 份会剥掉最旧那份的磁盘（不可恢复）。
- `meta.json` 仍是 `schema: 1`；`disks` 是可选字段，旧版本做的备份没有它，照常还原；旧版 tofi 读新备份会忽略 `disks`（只验归档）。
- 默认变了：`tofi backup` 现在比以前慢、占盘多（带磁盘时），停机时间随磁盘实占增长。想要旧行为用 `--no-disks`。
- 开着的页面：还原磁盘后该电脑冷启动。

## 相关代码与文档

- `deploy/self-host/tofi_host.py`：`backup_data`（`disks=`）、`account_disks`、`assert_disks_quiet`、`backup_disks`、`check_backup`、`verify_backups`、`restore_disks`、`undo_disk_swaps`、`backup_now`、`restore_backup`。
- 测试：`deploy/self-host/test_safe_updates.py` 的 `DiskBackupTests`（17 个）。
- 文档：`docs/agent-plan/safe-updates.md` A2、`docs/process.md` 7.4、`deploy/self-host/README.md`、`docs/releases.md` 未发布。
- 参考做法：OpenClaw 的 `backup create --verify` 与分阶段更新加回滚。

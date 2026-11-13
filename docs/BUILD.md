# CLI 构建与发布

发布线为 `main`，集成线为 `dev`；任务分支分别合入两条线。公开仓库同时推送 Gitee 和 GitHub，标签使用已确认的版本号，不加 `v`。

## 构建

从已提交、干净的源码执行，`VERSION` 必须使用用户确认的值：

```bash
cd go
make dist VERSION=<version>
```

输出到根目录 `dist/`，覆盖六个平台二进制。构建命令显式注入 `VERSION`；逐一核对 `go version -m` 的源码、平台和架构，并生成 `SHA256SUMS`；本机平台执行 `--version`。其他平台只有构建和格式检查时，不声明已经启动验证。

## 发布

1. 更新 README、版本说明和本页构建记录，审查本任务 diff，执行受影响的定向测试。
2. 在 `main` 显式核对 HEAD，再创建 annotated tag；推送两条线和该标签到 `origin` / `github`。
3. 两个平台 Release 都上传六个二进制和 `SHA256SUMS`，发布说明写清 cloud 端要求。
4. 回读两个远端的分支、标签和 Release；实际下载核对大小、SHA-256。
5. 证明任务分支已完整合入两条线后，清理本地任务分支。

CLI 更新源为 GitHub Release，访问失败时尝试 Gitee。附图功能还需 cloud 部署对应源码，客户端更新后重连 MCP 才会刷新工具参数。

## 验证范围

- 当前 CLI 代码完整回归为 85 项顶层测试通过，含 race 检查；WorkBuddy 隔离项目完成 63 次实际工具调用，正式更新检查连续两次复测通过。详细范围及历史构建记录见下文。
- 2026-11-12 发布时未执行真实 Agent 聊天附图上传；2026-11-13 已补充 WorkBuddy 桌面本地隔离验收，见下文。本次仍未部署 cloud，不能将源码发布或本地验收视为线上功能已生效。

## 最近构建 · 0.1.5（2026-11-13）

源码提交：`1200e757ca1fafecec8ee437c118c769794b3b02`（干净工作区，六个平台均为 `vcs.modified=false`）；Go 1.27.4，`CGO_ENABLED=0`，`make -C go dist VERSION=0.1.5`。

| 文件（位于 `dist/`） | 字节大小 | SHA-256 |
| --- | ---: | --- |
| `moo-cloud-darwin-amd64` | 7630144 | `8f427cba505eebad0c5eed8ec82d0353f6059c70ed5d8ec31b944c4f16b0fd73` |
| `moo-cloud-darwin-arm64` | 7045426 | `8d124aea77e4610b51bb43cb24cb971fdf6195004143bd6327aa804dfdc3ecc2` |
| `moo-cloud-linux-amd64` | 7483552 | `8988d4e695c7594454b2ddad2d2a7ebf04ea18a58e1c9dd6f4e691909e2b8e32` |
| `moo-cloud-linux-arm64` | 6881440 | `16d53e61aa5a16f3e15bb76f18d2e657bf14368151c04bacbbd81bca9a7b4aff` |
| `moo-cloud-windows-amd64.exe` | 7704576 | `0d49f5eee7106ce851ca8b5710309df5907fafd3fb644392bef2762fc9a3f7ca` |
| `moo-cloud-windows-arm64.exe` | 6977024 | `0ab670bf275df6c67ae0b238efa1e847a9a45f4680dd2024a4cd574840f8b813` |

- 六个平台的源码提交、架构、文件格式与产物中的版本字符串已核对；`SHA256SUMS` 按本表生成。
- macOS arm64 执行 `--version` 返回 `moo-cloud 0.1.5`；Go 链接器 ad-hoc 签名，未 Developer ID 签名或公证；其他平台未执行目标机启动验证。
- 已上传 GitHub 与 Gitee Release（标签 `0.1.5`）：六个二进制 + `SHA256SUMS`。从 GitHub 实际下载全部资产校验 SHA-256 一致，Gitee 的 `SHA256SUMS` 与本地逐字节一致；`check_cli_update` 返回 `latest_version=0.1.5` 且 `release_url` 指向 GitHub `0.1.5`。
- 流程偏差说明：本次先打 tag、后补构建记录，且 `0.1.5` 为 lightweight tag（`0.1.4` 为 annotated）。构建源码仍是 tag 指向的 `1200e75`；后续发布应先提交构建记录再创建 annotated tag。

## 最近构建 · 0.1.4（2026-11-13）

源码提交：`b212e2c088c88381e04c0c11f42fb43ea17afadc`（干净工作区，六个平台均为 `vcs.modified=false`）；Go 1.27.4，`CGO_ENABLED=0`，`make -C go dist VERSION=0.1.4`。标签同时包含本节构建记录，构建源码到标签之间仅有文档变化。

| 文件（位于 `dist/`） | 字节大小 | SHA-256 |
| --- | ---: | --- |
| `moo-cloud-darwin-amd64` | 7456080 | `18bac24ba9faeec0012716a7ee1f9722ffdce3afaf9d4d965df14696c27cfbb9` |
| `moo-cloud-darwin-arm64` | 6878274 | `9c6e647e9ff5da26781cac97111158a61f3a9b6cdf4705ee6e4c9f90428b2871` |
| `moo-cloud-linux-amd64` | 7315616 | `4e703896442aaeeec1c567f04a134fe17ced1225c062dfced528666bf0a482c6` |
| `moo-cloud-linux-arm64` | 6750368 | `ebe9fd3949d2a1ceee0f618bb8977bd2b905e17b2d712ddccc0ee2240f45f5f4` |
| `moo-cloud-windows-amd64.exe` | 7533056 | `eac10975a6d81b5c7b880dcf276eaececd534db6e47bd1b6b13a396523558f49` |
| `moo-cloud-windows-arm64.exe` | 6823424 | `c77f7ba72d0d43d40f28af54bbb15ad1a62fadd4317e66966928775398bb7e86` |

- 六个平台的源码提交、架构、文件格式及产物中的版本字符串已核对；`SHA256SUMS` 按本表生成。`-trimpath` 构建不在 `go version -m` 中保留链接参数，版本由明确的构建命令、产物字符串与本机执行共同核对。
- macOS arm64 执行 `--version` 返回 `moo-cloud 0.1.4`；Go 链接器 ad-hoc 签名且校验通过，未 Developer ID 签名或公证。macOS amd64 无签名；其他平台未执行额外签名或目标机启动验证。
- Go 业务源码沿用已完成的 85 项顶层测试及 race 检查；本轮文档修订没有改动 Go 业务源码。配套 cloud 定向 MCP 回归 70 项测试、710 个断言通过；未另行执行 E2E 或全量测试。
- 本次发布六平台下载文件与校验清单；cloud 配套版本为 `0.1.42`。不自动替换本机共享安装文件，不自动重启客户端 MCP 进程。

## 历史构建 · 0.1.3（2026-11-12）

源码提交：`c5bd97bb8b550270d74b54e2a723fd8294c26a07`（干净工作区，`vcs.modified=false`）；Go 1.27.4，`CGO_ENABLED=0`，`make dist VERSION=0.1.3`。发布标签 `0.1.3` 同时包含本次构建记录。

| 文件（位于 `dist/`） | 字节大小 | SHA-256 |
| --- | ---: | --- |
| `moo-cloud-darwin-amd64` | 7427216 | `f40e808aa63ee3fa51ae98cf0f0002fe866aa5beefdd55aef205afd06afaca8f` |
| `moo-cloud-darwin-arm64` | 6861570 | `45f8c19904d08aab40a333c215fd3f1acb935681c9cae6873515bff58f5e2b6b` |
| `moo-cloud-linux-amd64` | 7282848 | `5c0538e91f4fe4db655086a14ed779ce626fef68663373f4102bc0bd063ffbbe` |
| `moo-cloud-linux-arm64` | 6750368 | `0f3e02fddddb3131a4148d4aa6d82efa5e8cfc7fcbbb89039f0d5e3dc70a2dcf` |
| `moo-cloud-windows-amd64.exe` | 7501312 | `1fddb5388ee9920846152920a45cd574b8843b12fd7381b442da7d7466a08669` |
| `moo-cloud-windows-arm64.exe` | 6796800 | `7a1db32cd3daeda6eb37d80a0c1d9645d070f0958e505d007930a22b76b56e03` |

- 六个平台的源码提交、架构和文件格式已核对；`SHA256SUMS` 已按本表生成。
- macOS arm64 执行 `--version` 返回 `moo-cloud 0.1.3`；Go 链接器 ad-hoc 签名，未 Developer ID 签名或公证。macOS amd64 无签名；其他平台未执行额外签名或目标机启动验证。
- 本次只发布下载产物；本机共享安装文件与已运行 MCP 进程没有自动替换。

## 最近本地验收构建 · 2026-11-13（未发布）

- 源码：`3ea02f433faf7d75dee53a271fb26b429840302c`，工作区干净，`vcs.modified=false`；沿用 `0.1.3`，没有递增版本号。
- 命令：在 `go/` 执行 `CGO_ENABLED=0 go build -trimpath -ldflags='-s -w -X main.version=0.1.3' -o <本轮临时目录>/moo-cloud ./cmd/moo-cloud`，未覆盖 `dist/` 或本机共享安装文件。
- 产物：临时目录内 `moo-cloud`，macOS arm64，6,878,274 字节；SHA-256 为 `3b65bcc1518fc7faf9469c33eaff5954727fbb5fded9495cc83939f7738c0002`。Go 1.27.4；`--version` 返回 `moo-cloud 0.1.3`，Go 链接器 ad-hoc 签名，未 Developer ID 签名或公证。
- 本地隔离 cloud 的真实 HTTP 端点可发现全部 13 个云端工具；WorkBuddy 自带运行时连接本地 MCP 成功，但独立运行缺少桌面登录态，未完成业务调用。其进程虽返回 0，实际结果为 `error_during_execution`，不能判为通过。
- WorkBuddy 桌面窗口自动化在重连阶段持续超时，其他应用窗口也同样超时；未完成桌面会话、评论、附图和状态流转验收。已恢复原 MCP 配置、停止本轮本地服务并移除临时明文凭据。恢复窗口连接后继续验收，不能用接口或模拟测试替代。

### 补充：WorkBuddy 桌面验收已完成（2026-11-13）

- 窗口连接恢复后，在 WorkBuddy 5.11.2 的专用会话中使用上述 CLI 产物与 cloud `c321aed` 的本机隔离 SQLite/HTTP 环境；业务验收沿用同一产物，没有重新构建或递增版本号。本机共享安装的后续更新见下文。
- Hy4 在项目和列表查询阶段等待较长后取消；用户切到 DeepSeek-V4.1-Flash 后继续。只读查询轮 33 秒、评论轮 62 秒、附图预览 14 秒、确认建单与读图 21 秒、状态回写轮 61 秒；这些是客户端显示的每轮总耗时，包含模型推理，并非单次接口延迟。
- CLI 日志记录实际工具调用 63 次，覆盖 13 个 cloud 工具和 `check_cli_update`。中位耗时 27 ms，范围 20–436 ms；其中 5 次为预期业务拒绝，另一次 `limit=0` 被客户端 Schema 校验拦截，没有发往 cloud。未执行 `update_cli`。
- 四类异常列表/详情、AI markdown、软件空列表、待办详情与评论分页均通过；TODO 评论按 2+1 两页读出三条历史记录，无遗漏或重复。
- 五类目标（四类异常及 TODO）均实际发表评论；首尾全角空格归一化后重试命中原评论 ID，最终只新增五条。hold 策略、纯空白正文、无权项目及 key/id 混用明确拒绝，未产生额外评论。
- 通过聊天的「添加文件 → 本地文件」附加 PNG，先只预览，数据库仍为两条待办、零附件；明确确认后只新增一条待办、一张附件。同参重试返回同一待办，`images_added=0`；`get_todo_image` 可读。附件为 707 字节 PNG，落盘字节与原聊天图片 SHA-256 一致。
- 四类异常解决后立即重试均 `already_resolved=true`，首次解决时间保留。附图待办完成 open → in_progress → done，重试 `already_done=true`；时间线仅 created/started/completed 三条，附件保留。完成后仍可发表评论并查询；hold 待办状态不变、评论为零。
- 更新检查返回运行/安装/最新稳定版均为 `0.1.3`；仅检查，没有安装更新。所有写入限于本轮隔离项目，不涉及生产数据、真实成员通知或服务器部署。
- 验收后再次读取隔离数据库，确认评论共 13 条、待办共 3 条、附件共 1 张；hold 记录没有评论或状态事件。临时连接已从配置及 WorkBuddy 面板移除，原有配置保留；本轮测试 Token 已吊销、明文凭据已删除，本地服务已停止。

### 本机安装与重连（2026-11-13）

- 排查发现 WorkBuddy 原正式连接的共享 CLI 为 `0.1.2`，其 `create_todo` 未暴露 `image_paths`。本地验收中临时构建的版本检查不能代表共享安装版本。
- 经用户明确确认，将上述已验收的同一 `0.1.3` 产物原子替换到 `~/.local/bin/moo-cloud`；SHA-256 与本页构建记录一致，`--version` 和 ad-hoc 签名校验通过。按用户后续要求删除本次生成的 `0.1.2` 备份，没有保留旧安装文件。
- 正式端点只读工具发现返回 15 个工具，包含评论查询/发表及 `image_paths`；WorkBuddy 中停用再启用原 `moo-cloud` 连接以重启旧进程。正式环境只进行只读核对，不创建待办、不发表评论或流转业务数据。
- 同步修订 WorkBuddy 接入文档及源码中的配置完成提示：先在面板信任、启用并新建会话，只有连接或工具未刷新时才完整重启；7 项配置相关回归通过，含 race 检查。初次安装的验收产物来自 `3ea02f4`，后续更新检查修复构建见下文。

### 更新检查补修（2026-11-13）

- 正式连接的只读验收发现 `check_cli_update` 连续失败：GitHub 返回 403，实际响应的限流余额为 0；Gitee 返回 200 和有效的 `0.1.3` 发布，但附件元数据全部不含 `size`，原实现误判为「发布附件大小不合法」。本地业务验收轮当时成功，不能替代此次真实发布源故障验证。
- 修复 Gitee 未知附件大小的处理；实际下载仍有 64 MiB 上限、非空及 SHA-256 校验，已知大小仍要求匹配。发布源全失败时，MCP 错误回执保留本机版本与重连状态，不返回无法确认的最新版本；初始化说明明确要求告知用户检查未完成。
- 新增真实 Gitee 元数据格式、GitHub 403 回退、空文件/校验失败、双源失败版本保留与初始化提醒的回归。修复前新用例能复现原问题，修复后全套 85 项顶层测试通过，含 race 检查。
- 修复构建源码：`9913721c0dd80c2b266bc6921ef0ebae581daafd`，干净工作区，`vcs.modified=false`；Go 1.27.4，macOS arm64、`CGO_ENABLED=0`，沿用 `0.1.3` 及前述构建命令。产物为本轮临时目录内 `moo-cloud`，6,878,274 字节，SHA-256：`e05b29cfd7262565a609ef9ff6ba0c039e1147319df6b2afe21d97ddbbf9aefd`；版本执行与 ad-hoc 签名校验通过，未 Developer ID 签名或公证。
- 真实发布源的两次手动检查均通过 Gitee 返回 `0.1.3`。随后将修复产物原子安装到 `~/.local/bin/moo-cloud`，没有生成旧文件备份，并在 WorkBuddy 中停用再启用正式连接。
- WorkBuddy 正式连接连续两次重新调用 `check_cli_update` 成功，整轮显示 8 秒；两次运行/安装/最新版本均为 `0.1.3`，`update_available=false`、`restart_required=false`，`release_url` 为 Gitee 的 `0.1.3` 发布地址。没有调用安装更新工具或写入生产项目数据。
- 本轮只构建、安装本机修复产物并合入源码；没有覆盖 `dist/`、创建标签或发布 Release，其他电脑仍需后续发布并更新相应 CLI。

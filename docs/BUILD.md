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

- CLI 定向测试 14 组通过，含 race 检查；本次功能对应 cloud 接口与媒体回归 39 项、273 个断言通过。
- 本次没有执行真实 Agent 聊天附图上传，也没有部署 cloud；不能将源码发布视为线上附图功能已生效。

## 最近构建 · 0.1.3（2026-11-12）

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

# CLI 构建与发布

发布线为 `main`，集成线为 `dev`；任务分支分别合入两条线。公开仓库同时推送 Gitee 和 GitHub，标签使用已确认的版本号，不加 `v`。

## 构建

从已提交、干净的源码执行，`VERSION` 必须使用用户确认的值：

```bash
cd go
make dist VERSION=<version>
```

输出到根目录 `dist/`，覆盖六个平台二进制。逐一核对 `go version -m` 的版本注入、平台和架构，并生成 `SHA256SUMS`；本机平台执行 `--version`。其他平台只有构建和格式检查时，不声明已经启动验证。

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

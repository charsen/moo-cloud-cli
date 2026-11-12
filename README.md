# moo-cloud-cli

夜间修复流程的**独立**命令行工具，不依赖任何具体项目仓，可直接分享给同事。

它做两件事：

1. **身份校验**：扫描工作区里的 `.moo-cloud.json`，确认「本地目录 = 云端项目」，
   并把「工作区脏 / 在基础分支 / 云端 repo_url 未填」这类风险先标出来；
2. **拉取队列**：用账号级 MCP token 拉取未处理项（异常与待办是两条独立流程，默认只拉异常），
   输出一份可以直接喂给 AI 的队列文件。

> 是否自动改代码由调用方决定；本工具只负责「先验证，再取数」。

## 要求

- **不需要 PHP、Node 或任何运行时**：单个可执行文件
- 本机有 `git`（预检要读仓库状态）
- 一份能访问 cloud 的账号级 MCP token（`mcp:read` 即可拉取；要回写还需 `mcp:write`）

## 安装

从 `dist/` 取对应平台的文件，改名为 `moo-cloud` 并加执行权限：

| 平台 | 文件 |
|---|---|
| macOS Apple Silicon | `moo-cloud-darwin-arm64` |
| macOS Intel | `moo-cloud-darwin-amd64` |
| Linux x86_64 | `moo-cloud-linux-amd64` |
| Linux arm64 | `moo-cloud-linux-arm64` |
| Windows x86_64 | `moo-cloud-windows-amd64.exe` |
| Windows arm64 | `moo-cloud-windows-arm64.exe` |

```bash
cp dist/moo-cloud-darwin-arm64 ./moo-cloud && chmod +x ./moo-cloud
xattr -d com.apple.quarantine ./moo-cloud   # 仅 macOS 首次运行需要
```

从源码构建：`cd go && make dist`（需要 Go 1.22+）；测试：`cd go && go test ./...`。
`moo-cloud --version` 查看构建版本（由 git describe 注入）。

## 快速开始

```bash
# 1. 存 token（只读一次，权限 600）
mkdir -p ~/.config/moo-fleet
printf '%s' '<moo_mcp_...>' > ~/.config/moo-fleet/mcp-token
chmod 600 ~/.config/moo-fleet/mcp-token

# 2. 在工作区根目录执行
cd /path/to/workspace
./moo-cloud preflight --offline     # 先只做本地校验
./moo-cloud preflight --fetch       # 校验 + 拉异常队列（默认类型）
./moo-cloud preflight --fetch --types=todo \
  --out=moo-cloud-reports/$(date +%F)/todos        # 待办单独一条流
```

输出默认在 `<工作区>/moo-cloud-reports/<日期>/`：

| 文件 | 内容 |
|---|---|
| `_preflight.json` | 机器可读的校验结果 |
| `_preflight.md` | 人读摘要（按仓库） |
| `_queue.json` | 待处理问题（仅 `--fetch`） |

报告正文模板见 `templates/report.md`。

## 命令一览

| 命令 | 作用 |
|---|---|
| `moo-cloud preflight` | 身份校验 + 拉取异常/待办队列（主命令） |
| `moo-cloud mcp-server` | 本地 stdio MCP 桥：把云端 MCP 暴露给 Codex / WorkBuddy / Claude 等 |
| `moo-cloud agent-config` | 生成或写入各客户端的 MCP 配置 |

报告模板：异常用 `templates/report.md`，待办用 `templates/todo-report.md`。

## 在 Codex / WorkBuddy / Claude 里使用

CLI 自带一个本地 **stdio 桥** `moo-cloud mcp-server`：客户端只需要 `command + args`，
token 和云端地址由桥从本机读取（`~/.config/moo-fleet/mcp-token`），不用逐客户端配 URL / Bearer。

### 生成配置

| 客户端 | 命令 | 写入位置 |
|---|---|---|
| Codex | `./moo-cloud agent-config --target=codex --write` | `~/.codex/config.toml` |
| WorkBuddy | `./moo-cloud agent-config --target=workbuddy --write` | `~/.workbuddy/mcp.json` |
| Claude / Cursor / 通用 | `./moo-cloud agent-config --target=json` | 手动粘贴到各自配置 |

已有同名配置时默认不覆盖，更新时加 `--force`；`--home=<dir>` 可演练到临时目录。
写入的 `command` 是 **moo-cloud 自身的绝对路径**，所以移动二进制后要重新跑一次 `agent-config`。

生成的 Codex 片段（只读工具带自动批准）：

```toml
[mcp_servers.moo_cloud]
command = "/path/to/moo-cloud"
args = ["mcp-server"]

[mcp_servers.moo_cloud.tools.list_projects]
approval_mode = "approve"
```

WorkBuddy / Claude / Cursor 等通用 JSON：

```json
{
  "mcpServers": {
    "moo-cloud": {
      "type": "stdio",
      "command": "/path/to/moo-cloud",
      "args": ["mcp-server"]
    }
  }
}
```

### Codex：重启即生效

1. 写入配置：`./moo-cloud agent-config --target=codex --write`
2. 完全退出 Codex 再打开（新开会话）
3. 验证：

   ```bash
   codex mcp list | grep moo_cloud
   codex exec --skip-git-repo-check "调用 moo_cloud 的 list_projects 工具，只回答项目数量"
   ```

4. 审批：8 个只读工具自动批准；`resolve_issue` / `update_todo_status` 会请求人工批准。
   无人值守场景（如 CI）才用 `--dangerously-bypass-approvals-and-sandbox`，平时不要。

### WorkBuddy：写配置 → 完全退出 → 面板启用 → 新会话

> **关键**：WorkBuddy 不会在启动时自动加载 `mcp.json` 里新加的 server。
> 写完配置必须再在它的 MCP 面板里启用一次，并新建会话，否则会话里看不到工具。

1. 写入配置（已有条目时加 `--force` 覆盖）：

   ```bash
   ./moo-cloud agent-config --target=workbuddy --write --force
   ```

2. **完全退出 WorkBuddy**：用 `Cmd+Q`，不是只关窗口；确认进程真的退出：

   ```bash
   pgrep -f "/Applications/WorkBuddy.app/Contents/MacOS/Electron" || echo "已退出"
   ```

3. 重新打开 WorkBuddy。
4. 打开 **设置 → MCP**（部分版本叫「工具」或「连接器」），找到 `moo-cloud`，
   打开启用开关。第一次启用可能要求确认/批准该 MCP 配置，选择允许。
5. **新建一个会话**（旧会话不会自动获得新工具），让它「列出 MCP 工具」或
   「列出 Moo Cloud 的项目」。

验证 daemon 是否拉起了桥：

```bash
grep "custom-mcp:moo-cloud" ~/.workbuddy/logs/daemon.log | tail -3
```

正常会看到：

```
wb:mcp:setEnabled arg="custom-mcp:moo-cloud"
wb:mcp:connect arg="custom-mcp:moo-cloud"
MCP-AuthDiag stdio-connected {"id":"custom-mcp:moo-cloud","pidSpawned":...}
```

工具列表应恰好是 10 个：`list_projects`、`list_issues`、`get_issue`、`get_issue_markdown`、
`list_apps`、`list_todos`、`get_todo`、`get_todo_image`、`resolve_issue`、`update_todo_status`。

WorkBuddy 常见问题：

| 现象 | 处理 |
|---|---|
| MCP 面板里没有 `moo-cloud` | 确认 `~/.workbuddy/mcp.json` 有该条目；字段只能是 `type` / `command` / `args` / `env` / `cwd` / `url` / `headers` / `timeout`，带 `description` 会被整条拒绝；然后完全退出重开，或用 `--force` 重写 |
| 启用了但会话里没有工具 | 新建会话；旧会话不会热加载 |
| 日志有 `stdio-connected` 但工具为空 | 手工验证桥（见下），多半是 token 缺失 |
| 改了配置没变化 | 只关窗口不算重启，必须 `Cmd+Q` 完全退出 |

### 手工验证桥

```bash
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | ./moo-cloud mcp-server
```

正常返回一行 JSON；报“缺少 token”时检查 `~/.config/moo-fleet/mcp-token`。

### Claude / Cursor

把上面的通用 JSON 粘到对应客户端的 MCP 配置（Claude Desktop 是
`claude_desktop_config.json`），重启客户端即可。

桥的行为：stdin 每行一条 JSON-RPC，原样转发到云端 `/api/v1/mcp`；有 `id` 的写回一行，
通知不写 stdout；网络失败返回同 `id` 的 JSON-RPC error。云端若返回 SSE（`text/event-stream`），
桥会取最后一条 `data` 消息。写入配置前会备份原文件，
重复执行不会重复添加。

## 仓库标记：`.moo-cloud.json`

在每个待纳管的仓库**根目录**放一份。两种角色：

### 后端（默认）

```json
{
  "role": "backend",
  "root": "engine",
  "deployments": [
    { "project": "my-project", "env_file": ".env" },
    { "project": "my-project-prod", "env_file": ".env.prod" }
  ]
}
```

- `root`：Laravel 应用根相对仓库根的位置（如 `engine`，没有子目录就写 `.`）
- `deployments`：一个仓库可能服务多个云端项目（多环境/多客户），每项指定读取哪个 `.env*`
- 校验方式：用该 env 里的 `MOO_MONITOR_CLOUD_TOKEN` 调 cloud `/api/v1/summary`，
  返回的 `project.slug` 必须与 `project` 一致

### 软件（App / 桌面 / Web）

```json
{
  "role": "app",
  "root": ".",
  "product": "my-app",
  "project": "my-project",
  "platforms": ["android", "ios"],
  "identifiers": ["com.example.app"],
  "verify": [
    { "file": "android/app/build.gradle", "contains": "com.example.app" }
  ]
}
```

- `verify`：文件存在且包含指定字符串才算对上，用于防止认错仓
- `identifiers` / `platforms` 会与云端 `AppProduct` 的标识交叉核对

## 队列结构

`_queue.json` 按项目分组，每类单独一份：

```json
{
  "generated_at": "2026-11-11T18:00:00+08:00",
  "projects": {
    "my-project": {
      "runtime":  { "issues": [], "count": 0, "has_more": false },
      "todo":     { "issues": [], "count": 0, "pages": 1 }
    }
  }
}
```

- 单类拉取失败：该段是 `{"issues": [], "count": 0, "error": "..."}`，其他类不受影响；
- 服务端还没提供某个工具：该段带 `"skipped": "服务端未提供 list_todos"`；
- 待办行额外带 `ai_policy`：`auto` 可自动完成、`confirm` 只能认领/分析、`hold` 保留不动。

## MCP 处理范围（账号默认 + 我在项目里的范围）

不是所有类别都归你处理时，在 cloud 里配置范围，而不是每次改命令行：

- **账号默认**：「个人 Token」页 →「MCP 默认范围」（留空 = 全部）
- **我在项目里的范围**：项目卡片上的「MCP 范围」，只影响你自己，
  同项目的其他维护者不受影响；留空继承账号默认
- 有效范围 = 我在该项目的范围 ?? 账号默认 ?? 全部，由 MCP `list_projects` 返回
- 范围外的类别在队列里标 `"skipped": "不在处理范围"`，不拉取、不修复、不回写
- 服务端对 `resolve_issue` / `update_todo_status` 也做了范围校验：范围外返回 403
- 只影响 MCP（夜间自动或随时手动发起）；页面手动处理不受限制
- 拉取按项目并发（最多 4 个）；429 / 5xx 自动重试两次

## 常用参数

| 参数 | 说明 |
|---|---|
| `--root=<dir>` | 工作区根目录，默认当前目录 |
| `--date=YYYY-MM-DD` | 输出日期目录，默认上海当天 |
| `--out=<dir>` | 输出目录，默认 `<root>/moo-cloud-reports/<date>` |
| `--offline` | 跳过云端身份校验 |
| `--fetch` | 拉取待处理队列 |
| `--types=` | 逗号分隔，默认 `runtime,slow_query,frontend_error,app_error`；待办单独跑 `--types=todo` |
| `--token=` | 账号级 MCP token；默认读 `MOO_CLOUD_MCP_TOKEN` 或 `~/.config/moo-fleet/mcp-token` |
| `--cloud=` | cloud 地址，默认 `https://c.mooeen.com`（也接受 `MOO_CLOUD_URL`） |
| `--limit=` | 每项目每类最多拉取条数（1–50，默认 5）；frontend/app 会翻页补足后再截断 |
| `--concurrency=` | 项目间并发数（1–16，默认 4） |
| `--json` | 输出机器可读的 preflight 结果 |

取值参数必须带 `=`（裸写 `--root` 会被忽略并告警）；`--types=` 为空或全是未知类型会直接失败，
不会静默产出一份空队列。

## 退出码

- `0`：正常，或只有单仓/单类告警——失败仓记录在 `_preflight.json`，其余仓照常拉取
- `1`：无法继续（缺账号 token、没有通过身份校验的仓库、整体拉取失败、`--date` 非法等）

单仓身份不一致不会再让整晚中止：该仓被排除出拉取清单，报告里标红，其余健康仓继续。

## 故障排查

| 现象 | 处理 |
|---|---|
| `mcp-server: 缺少 token` | 确认 `~/.config/moo-fleet/mcp-token` 存在且权限 600；或显式传 `--token=` |
| 客户端里看不到 Moo Cloud 工具 | 重启客户端；确认 `command`/`args` 路径可执行；先用手工 `printf` 验证桥 |
| 生成的 `command` 路径不对 | 移动过二进制：重新跑一次 `moo-cloud agent-config --write --force` |
| macOS 提示“无法打开/已损坏” | `xattr -d com.apple.quarantine ./moo-cloud` |
| `云端不可达` / 401 | 检查网络与 token 是否被吊销；`--cloud` 是否指向正确实例 |
| 重复执行 `--write` 没变化 | 已有同名配置时默认跳过，需要覆盖加 `--force` |
| 报告目录日期不对 | 日期按 Asia/Shanghai；可用 `--date=YYYY-MM-DD` 指定 |
| 报“部署指向多个云端” | 各部署的 `MOO_MONITOR_CLOUD_URL` 不一致，用 `--cloud=` 显式指定拉取目标 |

## 安全

- host token 只从本地 `.env*` 读取，不打印、不写日志；
- 个人 MCP token 只读文件/环境变量；
- 不要把 token 提交进任何仓库；cloud 侧只存哈希。

## 为什么先校验再修

夜间自动改代码最大的风险不是「修不动」，而是**改错仓库**或**基于旧代码修**。
这个工具的目的就是把这两类风险在动手前挡掉：身份对不上直接硬失败，工作区脏/分支不对只告警，
由调用方决定今晚跳过哪个仓。

# moo-cloud-cli

夜间修复流程的**独立**命令行工具，不依赖任何具体项目仓，可直接分享给同事。

它做两件事：

1. **身份校验**：扫描工作区里的 `.moo-cloud.json`，确认「本地目录 = 云端项目」，
   并把「工作区脏 / 在基础分支 / 云端 repo_url 未填」这类风险先标出来；
2. **拉取队列**：用账号级 MCP token 拉取未处理项（异常与待办是两条独立流程，默认只拉异常），
   输出一份可以直接喂给 AI 的队列文件。

> 是否自动改代码由调用方决定；本工具只负责「先验证，再取数」。

## 要求

- PHP 8.2+，启用 `curl`、`json` 扩展
- 本机有 `git`
- 一份能访问 cloud 的账号级 MCP token（`mcp:read` 即可拉取；要回写还需 `mcp:write`）

## 快速开始

```bash
# 1. 放到任意位置
git clone <repo> ~/tools/moo-cloud-cli

# 2. 存 token（只读一次，权限 600）
mkdir -p ~/.config/moo-fleet
printf '%s' '<moo_pat_...>' > ~/.config/moo-fleet/mcp-token
chmod 600 ~/.config/moo-fleet/mcp-token

# 3. 在工作区根目录执行
cd /path/to/workspace
~/tools/moo-cloud-cli/bin/preflight --offline     # 先只做本地校验
~/tools/moo-cloud-cli/bin/preflight --fetch       # 校验 + 拉异常队列（默认类型）
~/tools/moo-cloud-cli/bin/preflight --fetch --types=todo \
  --out=moo-cloud-reports/$(date +%F)/todos        # 待办单独一条流
```

输出默认在 `<工作区>/moo-cloud-reports/<日期>/`：

| 文件 | 内容 |
|---|---|
| `_preflight.json` | 机器可读的校验结果 |
| `_preflight.md` | 人读摘要（按仓库） |
| `_queue.json` | 待处理问题（仅 `--fetch`） |

报告正文模板见 `templates/report.md`。

## 在 Codex / WorkBuddy / Claude 里使用

CLI 自带一个本地 **stdio 桥** `bin/mcp-server`：客户端只需要 `command + args`，
token 和云端地址由桥从本机读取（`~/.config/moo-fleet/mcp-token`），不用逐客户端配 URL / Bearer。

一键写入配置：

```bash
php bin/agent-config --target=codex --write      # ~/.codex/config.toml
php bin/agent-config --target=workbuddy --write  # ~/.workbuddy/mcp.json
php bin/agent-config --target=json               # 通用 {mcpServers:{...}}
```

只打印不落盘就去掉 `--write`；`--force` 覆盖同名项，`--home=<dir>` 可演练到临时目录，
`--php=<path>` 指定解释器（默认用稳定的 `php` 路径，避免 Homebrew Cellar 升级后失效）。

生成的 Codex 片段：

```toml
[mcp_servers.moo_cloud]
command = "/opt/homebrew/bin/php"
args = ["/path/to/moo-cloud-cli/bin/mcp-server"]
```

WorkBuddy / Claude / Cursor 等通用 JSON：

```json
{
  "mcpServers": {
    "moo-cloud": {
      "type": "stdio",
      "command": "/opt/homebrew/bin/php",
      "args": ["/path/to/moo-cloud-cli/bin/mcp-server"]
    }
  }
}
```

桥的行为：stdin 每行一条 JSON-RPC，原样转发到云端 `/api/v1/mcp`；有 `id` 的写回一行，
通知不写 stdout；网络失败返回同 `id` 的 JSON-RPC error。写入配置前会备份原文件，
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

## 退出码

- `0`：正常，或只有单仓/单类告警——失败仓记录在 `_preflight.json`，其余仓照常拉取
- `1`：无法继续（缺账号 token、没有通过身份校验的仓库、整体拉取失败、`--date` 非法等）

单仓身份不一致不会再让整晚中止：该仓被排除出拉取清单，报告里标红，其余健康仓继续。

## 安全

- host token 只从本地 `.env*` 读取，不打印、不写日志；
- 个人 MCP token 只读文件/环境变量；
- 不要把 token 提交进任何仓库；cloud 侧只存哈希。

## 为什么先校验再修

夜间自动改代码最大的风险不是「修不动」，而是**改错仓库**或**基于旧代码修**。
这个工具的目的就是把这两类风险在动手前挡掉：身份对不上直接硬失败，工作区脏/分支不对只告警，
由调用方决定今晚跳过哪个仓。

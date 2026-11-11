# moo-cloud-cli

夜间修复流程的**独立**命令行工具，不依赖任何具体项目仓，可直接分享给同事。

它做两件事：

1. **身份校验**：扫描工作区里的 `.moo-cloud.json`，确认「本地目录 = 云端项目」，
   并把「工作区脏 / 在基础分支 / 云端 repo_url 未填」这类风险先标出来；
2. **拉取队列**：用账号级 MCP token 拉取各项目未处理的 runtime / 慢 SQL / 前端错误 / App 错误，
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
~/tools/moo-cloud-cli/bin/preflight --fetch       # 校验 + 拉队列
```

输出默认在 `<工作区>/moo-cloud-reports/<日期>/`：

| 文件 | 内容 |
|---|---|
| `_preflight.json` | 机器可读的校验结果 |
| `_preflight.md` | 人读摘要（按仓库） |
| `_queue.json` | 待处理问题（仅 `--fetch`） |

报告正文模板见 `templates/report.md`。

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
| `--token=` | 账号级 MCP token；默认读 `MOO_CLOUD_MCP_TOKEN` 或 `~/.config/moo-fleet/mcp-token` |
| `--cloud=` | cloud 地址，默认 `https://c.mooeen.com`（也接受 `MOO_CLOUD_URL`） |
| `--limit=` | 每项目每类最多拉取条数（1–50，默认 5） |

## 退出码

- `0`：只有告警（工作区脏、在基础分支、repo_url 未填等），是否继续由调用方决定
- `1`：硬失败（身份不一致、目录不对、env 缺 token、云端不可达）

## 安全

- host token 只从本地 `.env*` 读取，不打印、不写日志；
- 个人 MCP token 只读文件/环境变量；
- 不要把 token 提交进任何仓库；cloud 侧只存哈希。

## 为什么先校验再修

夜间自动改代码最大的风险不是「修不动」，而是**改错仓库**或**基于旧代码修**。
这个工具的目的就是把这两类风险在动手前挡掉：身份对不上直接硬失败，工作区脏/分支不对只告警，
由调用方决定今晚跳过哪个仓。

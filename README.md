# moo-cloud-cli

把本地代码仓库和云端的项目问题、待办连起来，并给 Codex / WorkBuddy / DeepSeek Harness 等 AI 客户端提供 MCP 工具。

它解决三个具体问题：

1. **对不上号**：确认「这个本地目录」就是「云端那个项目」，防止 AI 改错仓库；
2. **取数**：把云端待处理的异常、慢 SQL、前端错误、App 错误、待办拉到本地，生成一份队列；
3. **给 AI 用**：把同一套云端能力包装成本地 MCP，让 AI 客户端直接调用。

> 它只负责「校验 + 取数 + 提供 MCP」，是否自动改代码由调用方决定。

---

## 5 分钟上手

如果你只想先跑起来：

1. 从 [GitHub Releases](https://github.com/charsen/moo-cloud-cli/releases/latest) 或 [Gitee Releases](https://gitee.com/charsen/moo-cloud-cli/releases) 下载对应平台的文件；
2. 改名为 `moo-cloud`（Windows 为 `moo-cloud.exe`），加执行权限；
3. 把账号 token 存到 `~/.config/moo-fleet/mcp-token`；
4. 在代码工作区执行 `moo-cloud preflight --offline`。

下面按步骤展开。

---

## 1. 它适合谁

- 你有一批**本地代码仓库**，云端有一批对应的**项目**，想把两边关联起来；
- 你希望 AI 能读取云端的问题/待办，但不想让它瞎猜「哪个仓库对应哪个项目」；
- 你希望夜间或手动触发时，只处理「归你负责的类别」，不误关别人的问题。

不适合：只想看云端页面、不打算在本地跑命令的人。

---

## 2. 准备什么

| 需要 | 说明 |
|---|---|
| 一台能运行命令的电脑 | macOS / Linux / Windows 都可以 |
| `git` | 预检要读仓库状态；`git --version` 能输出版本即可 |
| 一个 Moo Cloud 账号 | 默认地址 `https://c.mooeen.com`，自建实例可用 `--cloud` 指定 |
| 一个**账号级 token** | 登录 cloud → 「个人 Token」→「生成 Token」 |

token 有两种权限：

- `mcp:read`：只读，够拉取问题/待办；
- `mcp:write`：可回写（解决/认领）；会自动包含读。

第一次用建议先生成只读 token。

---

## 3. 安装

### 3.1 下载

| 平台 | 下载文件 |
|---|---|
| macOS Apple Silicon（M1/M2/M3…） | `moo-cloud-darwin-arm64` |
| macOS Intel | `moo-cloud-darwin-amd64` |
| Linux x86_64 | `moo-cloud-linux-amd64` |
| Linux arm64 | `moo-cloud-linux-arm64` |
| Windows x86_64 | `moo-cloud-windows-amd64.exe` |
| Windows arm64 | `moo-cloud-windows-arm64.exe` |

Release 页同时提供 `SHA256SUMS`，可以校验下载是否完整。

### 3.2 放到一个固定位置

建议放在用户目录下的固定位置，不要放在某个 git 仓库里（仓库重置/切分支可能把文件删掉）：

macOS / Linux：

```bash
mkdir -p ~/.local/bin
cp /path/to/downloaded/moo-cloud-darwin-arm64 ~/.local/bin/moo-cloud
chmod +x ~/.local/bin/moo-cloud

# 让当前 shell 找得到（写进 ~/.zshrc 或 ~/.bashrc 可持久）
export PATH="$HOME/.local/bin:$PATH"

# macOS 首次运行如果提示“无法打开/已损坏”
xattr -d com.apple.quarantine ~/.local/bin/moo-cloud
```

Windows（PowerShell）：

```powershell
New-Item -ItemType Directory -Force "$env:USERPROFILE\bin" | Out-Null
Copy-Item "$env:USERPROFILE\Downloads\moo-cloud-windows-amd64.exe" "$env:USERPROFILE\bin\moo-cloud.exe"

# 把 %USERPROFILE%\bin 加进 PATH（当前用户，一次即可）
[Environment]::SetEnvironmentVariable(
  "Path",
  [Environment]::GetEnvironmentVariable("Path", "User") + ";$env:USERPROFILE\bin",
  "User"
)
```

新开一个终端，确认：

```bash
moo-cloud --version
```

Windows 用 `moo-cloud.exe --version`（如果已在 PATH 里，`moo-cloud --version` 也可以）。
后文所有 `moo-cloud ...` 命令，在 Windows 上对应 `moo-cloud.exe ...`。

### 3.3 从源码构建（开发者）

```bash
git clone https://github.com/charsen/moo-cloud-cli.git
cd moo-cloud-cli/go
make dist          # 输出到 ../dist/
go test ./...      # 跑测试
```

需要 Go 1.22+。

---

## 4. 配置 token

token 只需要存一次，文件内容**只有 token 本身**。

macOS / Linux：

```bash
mkdir -p ~/.config/moo-fleet
printf '%s' '<moo_mcp_...>' > ~/.config/moo-fleet/mcp-token
chmod 600 ~/.config/moo-fleet/mcp-token
```

Windows（PowerShell）：

```powershell
New-Item -ItemType Directory -Force "$env:USERPROFILE\.config\moo-fleet" | Out-Null
Set-Content -NoNewline -Encoding ascii `
  -Path "$env:USERPROFILE\.config\moo-fleet\mcp-token" `
  -Value '<moo_mcp_...>'
```

也可以不用文件，改用环境变量 `MOO_CLOUD_MCP_TOKEN`。

> token 不要提交进任何仓库，也不要贴到聊天/日志里。

---

## 5. 第一次运行

先进入你的代码工作区（也就是放着若干仓库的目录），然后：

```bash
cd /path/to/workspace

# 1) 只做本地与身份校验，不拉取
moo-cloud preflight --offline

# 2) 校验 + 拉取异常队列
moo-cloud preflight --fetch

# 3) 待办单独拉（默认类型不含 todo）
moo-cloud preflight --fetch --types=todo --out=moo-cloud-reports/todos
```

成功后会在 `<工作区>/moo-cloud-reports/<日期>/` 生成：

| 文件 | 用途 |
|---|---|
| `_preflight.json` | 机器可读的校验结果 |
| `_preflight.md` | 人读摘要：每个仓库、分支、脏文件、身份是否匹配 |
| `_queue.json` | 待处理问题/待办（仅 `--fetch`） |
| `_metrics.json` | 本次运行的耗时与条数（仅 `--fetch`） |

下一步：打开 `_preflight.md` 看每个仓库是否对上；再按第 8 节把 MCP 接进 AI 客户端。

### 如果报「未找到任何 .moo-cloud.json」

说明你的仓库还没登记云端身份。每个要纳管的仓库根目录需要放一个 `.moo-cloud.json`，见第 7 节。

### 如果报「app root 不存在或缺 artisan」

后端仓库的 marker 里 `root` 写的是 Laravel 应用所在子目录（常见是 `engine`）。如果没有子目录，写 `.`。

---

## 6. 命令一览

| 命令 | 作用 |
|---|---|
| `moo-cloud preflight` | 身份校验 + 拉取问题/待办（主命令） |
| `moo-cloud mcp-server` | 本地 stdio MCP 桥，给 AI 客户端用 |
| `moo-cloud agent-config` | 生成/写入客户端的 MCP 配置 |
| `moo-cloud todo add` | 记录一条云端待办（可选从问题生成） |
| `moo-cloud --version` | 查看版本 |
| `moo-cloud --help` | 查看子命令 |

每个子命令也支持 `--help`，例如 `moo-cloud preflight --help`。

---

## 7. 核心概念

### 7.1 云端项目 vs 本地仓库

云端一个「项目」通常对应本地一个仓库；一个仓库也可能服务多个项目（多环境/多客户）。

`.moo-cloud.json` 就是这张对照表，放在**仓库根目录**。

### 7.2 后端仓库 marker

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

- `root`：Laravel 应用相对仓库根的目录；没有子目录写 `.`
- `deployments[].project`：云端项目 slug
- `deployments[].env_file`：从哪个 env 文件读 host token

校验方式：用该 env 里的 `MOO_MONITOR_CLOUD_TOKEN` 调云端 `/api/v1/summary`，返回的项目 slug 必须与 marker 一致。

### 7.3 软件仓库 marker（App / 桌面 / Web）

```json
{
  "role": "app",
  "root": ".",
  "product": "my-app",
  "project": "my-project",
  "platforms": ["android", "ios"],
  "verify": [
    { "file": "android/app/build.gradle", "contains": "com.example.app" }
  ]
}
```

`verify` 是包名指纹：文件存在且包含指定字符串才算对上，防止认错仓库。

### 7.4 token、范围、策略

- **token**：账号级，一枚覆盖你所有项目；`moo_mcp_` 开头
- **MCP 处理范围**：你在 cloud 里设置「哪些类别归我处理」；范围外的类别不会被自动拉取/回写
- **MCP 处理策略**：问题类可分 `auto`（自动解决）/ `confirm`（需人工确认）/ `hold`（保留不处理）；默认 `auto`
- **待办策略**：每条待办另有 `ai_policy`，同样是 `auto/confirm/hold`

范围与策略都在 cloud 页面设置，CLI 只读取并执行。

---

## 8. 接入 AI 客户端

`moo-cloud mcp-server` 是一个本地 stdio MCP 桥：客户端只需要 `command + args`，token 和云端地址由桥自己读取。

先生成配置（在目标电脑上执行，它会写入**当前二进制自身的绝对路径**）：

```bash
moo-cloud agent-config --target=codex --write
moo-cloud agent-config --target=workbuddy --write
moo-cloud agent-config --target=json        # 只打印，给 Claude/Cursor 等
```

### 8.1 Codex

1. 执行 `moo-cloud agent-config --target=codex --write`；
2. 完全退出 Codex 再打开；
3. 验证：

```bash
codex mcp list | grep moo_cloud
codex exec --skip-git-repo-check "调用 moo_cloud 的 list_projects 工具，只回答项目数量"
```

生成的配置会对 8 个只读工具自动批准；`resolve_issue` / `update_todo_status` 仍会请求人工批准。

### 8.2 WorkBuddy

> WorkBuddy 不会自动加载新 server，写完配置还要在它的面板里启用一次。

1. `moo-cloud agent-config --target=workbuddy --write --force`
2. 用 `Cmd+Q` 完全退出 WorkBuddy（不是关窗口），确认进程退出：

   ```bash
   pgrep -f "/Applications/WorkBuddy.app/Contents/MacOS/Electron" || echo "已退出"
   ```

3. 重新打开 WorkBuddy；
4. 设置 → MCP（部分版本叫「工具/连接器」），启用 `moo-cloud`；
5. 新建一个会话。

验证：

```bash
grep "custom-mcp:moo-cloud" ~/.workbuddy/logs/daemon.log | tail -3
```

应看到 `wb:mcp:setEnabled` / `wb:mcp:connect` / `stdio-connected`。

### 8.3 DeepSeek Harness（DSH）

DSH 通过 `@deepseek-ai/dsh-mcp-client` 插件加载，不使用 `mcp.json`。在
`~/.dsh/profiles/desktop/cordis.patch.yml` 末尾追加：

```yaml
- insert:
    - id: mcp-moo-cloud
      name: "@deepseek-ai/dsh-mcp-client"
      config:
        serverName: moo-cloud
        transport: stdio
        command: "/path/to/moo-cloud"
        args: ["mcp-server"]
        toolCallTimeoutMs: 120000
```

注意：**新增插件必须放在 `- insert:` 里**；直接写 `- id:` 只会覆盖已有条目，不会加载新插件。

保存后 DSH 会重载；工具会以 `mcp__moo-cloud__list_projects` 形式出现。

DSH 会清掉子进程里匹配 `KEY|PASSWORD|SECRET|TOKEN` 的环境变量；桥默认读 token 文件，不受影响。

### 8.4 Claude / Cursor

把 `moo-cloud agent-config --target=json` 的输出粘到对应客户端的 MCP 配置
（Claude Desktop 是 `claude_desktop_config.json`），重启客户端。

### 8.5 手工验证桥

```bash
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | moo-cloud mcp-server
```

正常返回一行 JSON；报「缺少 token」时检查第 4 节。

---

## 9. MCP 工具

| 工具 | 用途 |
|---|---|
| `list_projects` | 项目清单、待处理计数、有效处理范围、策略 |
| `list_issues` | 按项目/类型列问题 |
| `get_issue` | 单条问题完整上下文（可选 payload） |
| `get_issue_markdown` | 单条问题的 AI markdown |
| `list_apps` | 项目下的软件（AppProduct） |
| `list_todos` | 待办列表 |
| `get_todo` | 待办详情（含附件） |
| `get_todo_image` | 读取待办图片 |
| `create_todo` | 记录待办（需 `mcp:write`） |
| `resolve_issue` | 标记问题已解决（需 `mcp:write`） |
| `update_todo_status` | 认领/完成待办（需 `mcp:write`） |

写操作会校验「处理范围 + 策略」：范围外或 `confirm/hold` 会返回 403。

---

### 9.1 记录待办

除了拉取，也可以把发现的问题或后续事项记到云端项目：

```bash
# 手动写一条
moo-cloud todo add --project=colorfulhome --title="修复登录页报错"

# 从一条问题生成标题/描述，并带上来源
moo-cloud todo add --project=colorfulhome --from-issue=runtime:abcdef123456

# 指定类型/优先级/指派给项目成员，输出 JSON
moo-cloud todo add --project=colorfulhome --title="补充回归用例" \
  --category=task --priority=high --assignee=dev@example.com --json
```

规则：

- 需要 `mcp:write`
- 同项目、同标题且未完成（`open` / `in_progress`）时复用已有待办，返回 `deduped: true`
- 新待办默认 `ai_policy=auto`；`--assignee` 可填 `me`（默认）或项目成员邮箱
- 免费项目待办配额满时返回错误，不会淘汰已有待办
- `--from-issue` 支持 `runtime` / `slow_query` / `frontend_error` / `app_error`

## 10. 输出与可观测性

- `_metrics.json`：本次运行总耗时、每项目/类型的耗时与条数、跳过/错误
- `--metrics-log=<file>`：每次运行追加一行 JSON，用于趋势
- `mcp-server --log=<file>`：每次 MCP 调用一行（method/tool/ok/duration/error）

---

## 11. 常用参数

| 参数 | 说明 |
|---|---|
| `--root=<dir>` | 工作区根目录，默认当前目录 |
| `--date=YYYY-MM-DD` | 输出日期目录，默认上海当天 |
| `--out=<dir>` | 输出目录 |
| `--offline` | 跳过云端身份校验 |
| `--fetch` | 拉取队列 |
| `--types=` | 默认 `runtime,slow_query,frontend_error,app_error`；待办用 `todo` |
| `--token=` | 覆盖 token（默认文件或环境变量） |
| `--cloud=` | cloud 地址，默认 `https://c.mooeen.com` |
| `--limit=` | 每类最多条数（1–50，默认 5） |
| `--concurrency=` | 项目并发数（1–16，默认 4） |
| `--json` | 输出机器可读的 preflight 结果 |
| `--metrics-log=<file>` | 追加指标日志 |

退出码：`0` 正常（含单仓跳过）；`1` 无法继续（缺 token、没有通过校验的仓库、整体拉取失败）。

---

## 12. 常见问题

**Q：`moo-cloud: command not found`**
没放进 PATH。用绝对路径执行，或把所在目录加入 PATH。

**Q：`未找到任何 .moo-cloud.json`**
工作区里没有 marker。确认 `--root` 指向的目录下有仓库，并按第 7 节加 marker。

**Q：`app root 不存在或缺 artisan`**
marker 的 `root` 写错了；Laravel 在子目录时写子目录名，否则写 `.`。

**Q：`env 缺 MOO_MONITOR_CLOUD_TOKEN`**
该仓库的 `.env*` 里没有云端 host token；找项目管理员要，或检查 `env_file` 是否写对。

**Q：`slug 不一致`**
本地 marker 的 `project` 和云端 token 对应的项目对不上，说明目录/仓库认错了，先别继续。

**Q：拉取失败 / 401**
token 失效、被吊销或过期；重新生成。`--cloud` 指向的实例也要对。

**Q：WorkBuddy 里看不到工具**
只写配置不够：要 `Cmd+Q` 完全退出 → 重开 → MCP 面板启用 → 新建会话。

**Q：DSH 里 `server is disconnected`**
确认配置在 `- insert:` 下、`command` 指向真实存在的二进制；改完重启 DSH 或新建会话。

**Q：macOS 提示无法打开**
`xattr -d com.apple.quarantine /path/to/moo-cloud`。

**Q：Windows 上 token 读不到**
路径是 `%USERPROFILE%\.config\moo-fleet\mcp-token`，写入时用 ASCII 编码，避免 UTF-16。

**Q：移动了二进制后客户端断连**
`agent-config` 写的是绝对路径；移动后重新执行一次 `agent-config --write --force`。

---

## 13. 安全

- host token 只从本地 `.env*` 读取，不打印、不写日志
- 个人 MCP token 只读文件/环境变量；cloud 侧只存哈希
- 不要把 token 提交进仓库、贴到聊天或日志
- 写操作在服务端校验范围与策略，范围外返回 403

---

## 14. 开发

```bash
cd go
make dist      # 交叉编译 6 个平台
go test ./...  # 单元 + httptest 集成测试
gofmt -w cmd/moo-cloud/*.go
```

目录：

```
go/cmd/moo-cloud/   main.go / preflight.go / mcp.go / agentconfig.go
templates/          报告模板
```

欢迎提 issue / PR。

---

## License

[MIT](LICENSE)

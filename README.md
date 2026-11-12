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
4. 在软件仓库或代码工作区执行 `moo-cloud preflight --offline`。

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

需要 Go 1.22+。发布步骤和产物核对见 [构建与发布](docs/BUILD.md)。

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

可以直接进入含 `.moo-cloud.json` 的软件仓库，也可以进入放着若干仓库的工作区，然后：

```bash
cd /path/to/software-repo

# 1) 只做本地与身份校验，不拉取
moo-cloud preflight --offline

# 2) 校验 + 拉取异常队列
moo-cloud preflight --fetch

# 3) 待办单独拉（默认类型不含 todo）
moo-cloud preflight --fetch --types=todo --out=moo-cloud-reports/todos
```

也可不切换目录，执行 `moo-cloud preflight --root=/path/to/workspace --offline` 批量检查工作区。扫描范围是指定目录本身、下一层和下两层目录；不会向上寻找配置，也不会无限递归。指向同一实际目录的符号链接只检查一次。

默认会在 `<root>/moo-cloud-reports/<日期>/` 生成：

| 文件 | 用途 |
|---|---|
| `_preflight.json` | 机器可读的校验结果 |
| `_preflight.md` | 人读摘要：每个仓库、分支、脏文件、身份是否匹配 |
| `_queue.json` | 待处理问题/待办（仅 `--fetch`） |
| `_metrics.json` | 本次运行的耗时与条数（仅 `--fetch`） |

下一步：打开 `_preflight.md` 看每个仓库是否对上；再按第 8 节把 MCP 接进 AI 客户端。

### 如果报「未找到任何 .moo-cloud.json」

先确认当前目录或 `--root` 是否正确。扫描包含该目录本身及下两层，每个要纳管的仓库根目录需要放一个 `.moo-cloud.json`，见第 7 节。

### 配置错误怎么处理

报告会指出 JSON 格式错误的位置、类型不正确的字段、无效平台，或 `verify[0].file` / `verify[0].contains` 等具体指纹项。先检查字段拼写，再核对文件路径和包名；错误信息不回显指纹内容。

未识别字段只警告，不会单独阻止预检；缺少必需资料、字段值无效或指纹校验失败会记为硬失败，并返回非零退出码。已发现配置的校验失败仍会写入报告，并继续检查其余仓库；未找到配置或命令参数不合法时直接退出。现有配置不增加必填项，`platforms` 仍可省略。

App 的项目、身份校验结果及失败原因会同时显示在 Markdown 和 JSON 报告中；离线模式会明确提示未验证云端归属。后端的 Git 根目录与 marker 位置不匹配时，该仓库不再联网校验或进入队列；同仓库内只有某个环境失败时，其他通过校验的环境仍可拉取。失败环境不参与拉取目标的云端选择；多个已通过校验的云端仍需用 `--cloud` 指定目标。

报告中的本地 origin 和云端仓库 URL 会移除用户信息、查询串和片段，保留仓库位置供排错；比较仓库身份时同样忽略这些部分，不修改 Git 配置。

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
| `moo-cloud comment list` | 分页查询某条异常或 TODO 的讨论 |
| `moo-cloud comment add` | 发表文字评论，与网页讨论共用数据 |
| `moo-cloud --version` | 查看版本 |
| `moo-cloud --help` | 查看子命令 |

每个子命令也支持 `--help`，例如 `moo-cloud preflight --help`。

---

## 7. 核心概念

### 7.1 云端项目 vs 本地仓库

cloud 项目用于协作和数据隔离，一个项目可包含多个软件；软件是检查更新、反馈和客户端异常的身份。一个软件可以对应多个本地仓库，一个仓库也可能服务多个项目（多环境/多客户）。

`.moo-cloud.json` 就是这张对照表，放在**仓库根目录**，供 `preflight` 预检使用。MCP 连接由账号 Token 和客户端服务器配置建立；`mcp-server` 不读取此文件，文件也不改变账号权限或自动限定工具调用的项目。

预检命令的 `--root` 可指向当前仓库或包含多个仓库的工作区，例如 `moo-cloud preflight --root=/path/to/workspace --offline`；扫描该目录本身及下两层目录。不传 `--root` 则使用当前目录，可在软件仓库内直接运行。这个命令参数与后端 marker 内的 `root` 用途不同。

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
  "product": "my-app",
  "project": "my-project",
  "platforms": ["android", "ios"],
  "verify": [
    { "file": "android/app/build.gradle", "contains": "com.example.app" }
  ]
}
```

App 默认模板只保留五类字段：

| 字段 | 用途 |
| --- | --- |
| `role` | `app` 明确表示 App / 桌面 / Web 仓库 |
| `project` | cloud 项目 slug，确定协作和数据归属 |
| `product` | 该项目下的软件 slug，与客户端的 `app` 标识对应 |
| `platforms` | 本仓库负责的平台，供报告和 Agent 区分多仓库；不会限制 MCP 权限 |
| `verify` | 本地包名指纹：文件存在且包含指定字符串，防止认错仓库 |

同一软件可对应多个本地仓库：分别填写相同 `project`、`product`，用 `platforms` 和各自的 `verify` 区分。不要为了拆 Android / iOS 仓库而拆成两个软件。

App 分支不使用 `root`；本地 `identifiers` 也不参与 CLI 解析，因此默认模板不再填写。cloud 中已有的可选包名资料、仓库链接和现有配置仍保留；后端的 `root`、`deployments`、`env_file` 继续明确配置，不自动猜目录或环境文件。

维护已有 App marker 时，删除未使用的 `root`、`identifiers` 即可，保留原 `project`、`product`、`platforms` 和 `verify`。包名指纹仍通过 `verify[].contains` 校验；Cloud 软件的可选包名资料无需删除。此精简不修改后端 marker 的 `root`、`deployments` 或 `env_file`。

软件版本发布不由 marker 驱动。各仓保留自身 `docs/BUILD.md`，后台操作统一见 [Cloud 软件发版手册](../moo-scaffold-cloud/docs/RELEASE-PLAYBOOK.md)；不要向 marker 增加版本、文件、哈希、签名或发布 Token。

App marker 的 `project` 与 `product` 都必填。在线检查使用账号级 Token 调用 `list_apps`，确认软件属于该项目；通过后可独立拉取队列。`--offline` 仅检查本地指纹，不验证云端归属。

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

写入会保留其他客户端设置和 MCP 服务，并先备份原配置；配置文件是软链接时更新并备份真实目标，保留链接。断链或不可读的配置会报错，请先修复文件。

### 8.1 Codex

1. 执行 `moo-cloud agent-config --target=codex --write`；
2. 完全退出 Codex 再打开；
3. 验证：

```bash
codex mcp list | grep moo_cloud
codex exec --skip-git-repo-check "调用 moo_cloud 的 list_projects 工具，只回答项目数量"
```

生成的配置会对 9 个只读工具自动批准（含 `list_comments`）；`post_comment`、`resolve_issue`、`update_todo_status` 等写工具仍会请求人工批准。已有配置需重新生成并重连后才会更新白名单。

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
| `list_comments` | 分页查询异常或 TODO 的团队讨论 |
| `post_comment` | 发表文字评论（需 `mcp:write`，先取得用户同意） |
| `create_todo` | 记录待办并附图（需 `mcp:write`） |
| `resolve_issue` | 标记问题已解决（需 `mcp:write`） |
| `update_todo_status` | 认领/完成待办（需 `mcp:write`） |

写操作会校验「处理范围 + 策略」：范围外或 `confirm/hold` 会返回 403。

使用本地 `mcp-server` 桥时，还提供两个本机工具（不发送到 cloud）：

| 工具 | 用途 |
|---|---|
| `check_cli_update` | 检查运行版本、本机安装版本和最新稳定版，不安装 |
| `update_cli` | 用户明确同意目标版本后，下载、校验、备份并更新本机 CLI |

---

### 9.1 记录待办

除了拉取，也可以把发现的问题或后续事项记到云端项目：

```bash
# 手动写一条
moo-cloud todo add --project=my-project --title="修复登录页报错"

# 带截图，可重复 --image 指定多张
moo-cloud todo add --project=my-project --title="查询没有反应" \
  --image="/absolute/path/screenshot.png" --image="/absolute/path/error.png"

# 从一条问题生成标题/描述，并带上来源
moo-cloud todo add --project=my-project --from-issue=runtime:abcdef123456

# 指定类型/优先级/指派给项目成员，输出 JSON（自动化需 --yes）
moo-cloud todo add --project=my-project --title="补充回归用例" \
  --category=task --priority=high --assignee=dev@example.com --yes --json
```

规则：

- 首次创建调用只返回项目确认信息，**不写库**；CLI 会显示项目名并询问 `y/N`
- 非交互环境（脚本/AI）必须加 `--yes`，否则直接报错
- 需要 `mcp:write`
- 同项目、同标题且未完成（`open` / `in_progress`）时复用已有待办，返回 `deduped: true`；带图时追加新图，相同图片不重复保存
- 支持 PNG/JPEG/WebP/GIF，最多 5 张图片（含已有图片），每张原始文件不超过 8 MiB、1600 万像素；图片保存在 cloud 的私有附件区
- 图片无效或保存失败会报错，并回滚本次创建或补图；返回 `images_attached`（已处理的输入张数，含重复）、`images_added`（实际新增张数）和 `todo.attachments_count`
- CLI 收不到完整图片保存回执时会报错；若错误带有待办 ID，先检查该记录附件，重试请保留相同标题与图片
- 新待办默认 `ai_policy=auto`；`--assignee` 可填 `me`（默认）或项目成员邮箱
- 免费项目待办配额满时拒绝新建，不会淘汰已有待办；相同标题的未完成待办仍可复用
- `--from-issue` 支持 `runtime` / `slow_query` / `frontend_error` / `app_error`

Agent 通过本地 MCP 桥创建待办时，用 `image_paths` 传入截图的**绝对文件路径**，由 CLI 读取并上传：

```json
{
  "project": "my-project",
  "title": "查询没有反应",
  "description": "选择筛选条件后，点击查询，列表未刷新。",
  "image_paths": ["/absolute/path/screenshot.png"]
}
```

首次调用返回项目预览与 `confirm_token`；确认后第二次调用保留相同参数和图片，再加上该令牌。直接连接 cloud 的 HTTP MCP 时，使用 `images` 数组传 `data:image/png;base64,...`，与本地 `image_paths` 二选一。

聊天附件不会自动进入工具参数。Agent 应把用户附图一起传入；如果客户端未提供可读的原图文件，应明确说明图片尚未上传。需要同时使用支持附图的 cloud 和 CLI，更新后重新连接 MCP 才会刷新工具参数。

### 9.2 检查与更新 CLI

MCP 连接初始化时自动检查发布版本，成功结果缓存 24 小时。有新版或磁盘文件已更新、进程尚未重启时，桥会在 MCP 初始化说明中要求 AI 提醒一次。具体是否展示提醒取决于客户端；也可以直接说：“检查 moo-cloud CLI 是否有更新”，由 AI 调用 `check_cli_update`，手动检查会绕过缓存。

发现新版后，AI 应先说明目标版本并询问是否安装。用户同意后才调用：

```json
{"target_version":"0.1.2","confirm":true}
```

这里的版本号是示例，以检查返回的实际版本为准。`update_cli` 是本机写操作，不加入自动批准白名单；`confirm` 是客户端根据用户明确答复传入的确认参数，不能代替客户端的用户审批。检查新版不会自行触发更新。

更新读取本项目的 GitHub Release，访问失败时尝试 Gitee；不发送账号 MCP token。安装前会重新检查目标版本，校验官方 `SHA256SUMS`、文件大小和二进制版本，再在原路径旁保存旧文件备份并替换。多个客户端共用同一文件时，只需更新一次。开发构建不自动覆盖，也不降级。

发布信息或附件下载失败都会尝试备用源，备用源必须提供用户确认的同一版本。更新期间用系统锁防止多个客户端同时替换；进程异常结束后锁自动释放。如果期间被其他方式安装了新文件，本次更新会停止并要求重新检查。

完成后**重新连接 MCP 或重启客户端**，已运行的进程继续使用旧版本。若 Windows 锁住正在运行的程序，或安装目录不可写，会保留原文件并提示手动更新；不会强行退出客户端。

### 9.3 查询与发表评论

沿用现有账号 Token 和 cloud 地址，无需修改 `.moo-cloud.json`。异常使用原有 key，TODO 使用原有 ID：

```bash
moo-cloud comment list --project=my-project --type=runtime --key=abcdef123456
moo-cloud comment list --project=my-project --type=todo --id=01ARZ3NDEKTSV4RRFFQ69G5FAV --limit=20 --offset=0 --json
moo-cloud comment add --project=my-project --type=runtime --key=abcdef123456 --body="已补充复现步骤"
```

`--type` 支持 `runtime`、`slow_query`、`frontend_error`、`app_error`、`todo`；异常只传 `--key`，TODO 只传 `--id`。查询按时间正序分页，有后续评论时返回 `next_offset`；下一次设置 `--offset`。已有附图返回数量，可在网页讨论查看。

发表文字需 `mcp:write`、有效处理范围且目标策略为 `auto`，正文为 1–5000 字。命令会询问确认；自动化调用需先获用户同意，再加 `--yes`。同账号、同记录、相同正文在 2 分钟内复用已有评论，避免重试重复评论或通知。已完成/已解决记录仍可讨论，不改变状态；删除或无权访问的记录不可操作。

MCP 中调用 `list_comments` / `post_comment`，参数为 `project`、`type`、`key` 或 `id`，发表时再传 `body`。写评论不加入自动批准白名单；本次只支持文字发表。cloud 部署评论工具后重连 MCP 获取新工具定义；本地 `comment` 命令需使用包含本轮功能的 CLI。

## 10. 输出与可观测性

- 前端与 App 错误拉取会持续越过被过滤的开发/调试数据，直到达到 `--limit` 或读完；队列中的 `has_more / next_offset` 保留原始记录的续取位置，不能按过滤后的条数计算偏移量。
- `_metrics.json`：本次运行总耗时、每项目/类型的耗时与条数、跳过/错误
- `--metrics-log=<file>`：每次运行追加一行 JSON，用于趋势
- `mcp-server --log=<file>`：每次 MCP 调用一行（method/tool/ok/duration/error）

---

## 11. 常用参数

| 参数 | 说明 |
|---|---|
| `--root=<dir>` | 当前仓库或工作区目录，默认当前目录；扫描本身及下两层 |
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

退出码：`0` 校验与必要输出成功；`1` 参数无效、任意仓库存在硬失败、缺 token、没有通过校验的仓库、整体拉取失败或必要报告落盘失败。其他仓库或环境成功的结果仍会保留。部分类型拉取成功时保留结果并逐项记录 `error`；范围/策略正常跳过不算失败。客户端配置写入或备份失败同样返回非零，不会报告写入成功。

布尔开关（如 `--yes`、`--write`、`--force`、`--offline`、`--fetch`、`--json`）不带值时启用，显式传值时建议使用 `=true` 或 `=false`。也接受 `1/0`、`yes/no`、`on/off`、`t/f`，大小写不敏感；`--yes=0` 不会跳过确认。空值及其他非法值直接报错，不会执行后续写入或联网操作。

带值参数必须使用 `--选项=值`，例如 `--root=/path/to/workspace`；未知选项、缺少值或值为空都会报错，不会静默回退到默认目录、客户端或云端。可选参数不使用时直接省略。

---

## 12. 常见问题

**Q：`moo-cloud: command not found`**
没放进 PATH。用绝对路径执行，或把所在目录加入 PATH。

**Q：`未找到任何 .moo-cloud.json`**
确认当前目录或 `--root` 本身及下两层内存在 marker，并按第 7 节配置；更深的仓库请直接指定对应目录。

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

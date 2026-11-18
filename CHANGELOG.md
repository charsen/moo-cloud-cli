# 更新记录

## 0.1.9 — 2026-11-18

- 新增 `todo add --batch=<report.json>`：一次提交一轮测试的多条待办（顶层/条目 project、分类、优先级、图片路径），逐条创建并输出汇总；`--dry-run` 只校验不写云端；单条失败不打断其余。
- 新增 `todo show --project= --id= [--json]`：查看单条待办详情、正文摘要、附件清单与事件；正文声称有截图但无附件、或有附件正文未提及时会提示，缓解「截图未随待办上传」的图文错位。
- 修复 README 文档：旧 base64 通道开关应为 `MOO_CLOUD_TODO_IMAGE_MODE=legacy`（此前误写成不存在的 `MCP_TODO_IMAGES_LEGACY`）。
- 删除死代码 `loadTodoImages`（已被 `loadTodoImageFiles` + `todoImagesBase64` 取代）。
- 七牛多图直传部分失败时，错误信息报出已上传对象 key（依赖七牛 TTL 回收），不再静默丢弃或整体假成功。
- `issue list` / `audit` 的整数参数由 `fmt.Sscanf`（宽松、接受前缀）改为 `strconv.Atoi`（严格），拒绝 `--limit=20abc` 之类输入。
- 补充 `docs/BUILD.md` 的 0.1.6 / 0.1.7 / 0.1.8 构建记录；0.1.7 因未保留产物如实标注缺记录。
- 修复交互判断缺陷：原先用 `ModeCharDevice` 判断标准输入是否终端，会把 null 设备等字符设备误判为可交互，导致重定向/CI 下写操作绕过 `--yes` 前置检查。改为真正的 ioctl/GetConsoleMode 终端判定（新增 `stdinIsTerminal()`，覆盖 todo/issue/comment 全部 5 处写入口）。
- 文档/引导对齐最新能力：MCP 初始化注入给 Agent 的「本地桥补充提醒」由 4 条扩为 8 条（补批量建单 --dry-run、image_paths 直传图文核对、非交互必加 --yes、todo show 核对回执）；README「给 Agent 的操作规范」同步补第 7–9 点，工具表补 `create_todo_image_tickets`，工具数量改为随部署变化（以 tools/list 为准）。

## 0.1.8 — 2026-11-18

- 图片上传默认模式改为 `direct`：只走七牛直传，失败直接报错；`auto`/`legacy` 仅显式开启。
- 七牛上传校验补齐：HTTP 200 但返回 `error`、缺少 `key` 或 key 不一致都会失败，不再把失败当成功。
- 发布资产修正：`SHA256SUMS` 改为 basename；新增 `dist/BUILD-<version>.json` Go 元数据。
- 文档补充 `MOO_CLOUD_TODO_IMAGE_MODE` 与旧 cloud 兼容边界。

## 0.1.7 — 2026-11-17

- create_todo 图片改为七牛直传：本地桥/CLI 先申请 `create_todo_image_tickets` 票据并直传七牛，再以 `image_upload_ids` 创建待办；旧 `images`/`image_paths` base64 通道保留过渡。

- 文档：说明 `get_issue` / `get_issue_markdown` / `get_todo` 返回的 markdown 末尾会附团队讨论最近 20 条（时间正序、正文脱敏）；更早的评论用 `list_comments` 分页读取。

## 0.1.6 — 2026-11-14

- `issue list`：按类型/状态/分页/个人范围列出异常，新增 `--env` 按环境过滤（对齐云端 `list_issues`）。
- `audit`：新增 `--mine`（待办统计只看我负责的）、`--summary`（只取云端 `audit_summary` 汇总、支持 `--out`）；修复 `--summary --out` 被忽略；`--json` 与 `--out` 互斥。
- `todo list`：补齐云端对账筛选 `--missing-note` / `--done-by` / `--created-after|before` / `--done-after|before`（`YYYY-MM-DD`）。
- `agent-config`：Codex 只读工具白名单补齐 `audit_summary`（与云端 14 工具对齐，写工具不进自动批准）。
- `mcp-server`：初始化 Agent 提醒扩展（先领取、先看 audit_summary、多图逐张读、以回执为准）；`get_todo_image` 引导读全多图。
- 文档：命令一览/按场景速查/`preflight` 与 `audit` 分工；工具数与测试数以实际为准（随 cloud/迭代变化）。
- 构建/仓库：`.gitignore` 忽略子目录构建产物。

## 0.1.5 — 2026-11-13

- 新增命令行业务闭环：`todo list` / `todo start` / `todo done` 与 `issue resolve`，脚本和 CI 可脱离 MCP 直接受理、完成、关闭并写回结果；写操作非交互需 `--yes`，仍受云端处理范围与策略（auto/confirm/hold）校验。
- 新增只读 `audit` 命令：按项目扫描「已完成但无处理说明 / open 逾期 / in_progress 长期 / 疑似已修复未关闭」的待办与异常，输出 Markdown 或 JSON，不写云端、不改状态。
- 明确「能力全集」与「个人处理范围」是两个正交维度：命令与审计默认按能力全集，只有 `--respect-scope` 才收窄到「我的处理范围」；`frontend_error` 为可勾选类别，账号未勾选属个人设置而非能力缺失。
- `mcp-server` 初始化说明新增「开始处理先领取」提醒：受理（`status=in_progress`）记录领取状态与领取人（已有经办人保留），避免操作者未领取就静默处理；`todo start` 帮助同步说明。
- `audit` 强化：`--fail-on-findings`（有违规退出码 2）、省略 `--project` 时从 `.moo-cloud.json` 自动取项目、命中 `--max-pages` 显式告警不静默截断；对支持新接口的 cloud，done 扫描走 `missing_note` 并可用 `audit_summary` 取汇总（旧 cloud 自动回退）。
- 回执显示领取人：`todo list/start/done` 输出领取人（`assignee`），`issue resolve` 输出解决时间/解决人。
- 安全与健壮性加固：`--cloud` 强制 https（仅 localhost 例外）防 token 明文；预检对「某项目全类型失败」与 `git status/branch` 失败计硬失败，marker `root` 拒绝越界，`verify.file` 限 2MiB；agent-config 读改写加 mtime/size 守卫并用同一份内容备份、Codex 前缀探测防重复 TOML；自更新保留原权限、主备源 `SHA256SUMS` 交叉校验、备份只留最近 3 份、取锁短暂重试；非幂等写（create_todo/post_comment/resolve/update）网络错误不自动重试；含业务数据的 JSON 报告新建为 0600。
- 业务增强：`todo list --mine` 只看指派给我/我领取的待办；`agent-config --target=dsh` 打印 DSH cordis.patch 片段；`create_todo` 预览调用恢复可重试（仅确认写入不重试）。
- 业务增强（第三轮）：`todo list --assignee=<id>` 按负责人过滤；agent-config 读改写加跨进程文件锁（复用更新锁）；`audit` 报告附「修复命令速查」。
- 业务增强（第四轮）：新增 `issue list`（按类型/状态/分页/个人范围列出异常），补齐 CLI 与 `todo list` 对称的异常查看能力。
- 业务增强（第五轮）：`issue list` 新增 `--env` 按环境过滤（对齐云端 `list_issues` 已支持的 env 参数）。
- 文档：README 增加「给 Agent 的操作规范」（定位→读→领取→写回→策略），与 cloud `initialize` instructions 对齐。
- 业务增强（第六轮）：`audit --summary` 只取云端 `audit_summary` 汇总（不翻明细，快、省流）；需 cloud 已部署该工具。
- 业务增强（第七轮）：`todo list` 补齐云端对账筛选 `--missing-note` / `--done-by` / `--created-after|before` / `--done-after|before`（YYYY-MM-DD）。
- 补充 MCP 待办直接受理、完成结果保存、幂等回执和查询说明；修正 `confirm` 策略允许受理的文档。配套 cloud 改动部署并重连后生效，无 CLI 二进制变更。

## 0.1.4 — 2026-11-13

- 新增 `comment list|add` 和 MCP `list_comments` / `post_comment`，支持四类异常及 TODO 的团队讨论、稳定分页、真实作者和正文脱敏；同账号同目标同正文在两分钟内去重。
- 查询与发表沿用项目权限，发表还受 `mcp:write`、处理范围及 `auto` 策略限制；已完成待办、已解决异常仍可讨论。评论只接收文字，已有附图返回数量并在网页查看。
- 软件仓库 `.moo-cloud.json` 模板收敛为五类字段；支持在当前仓库直接预检，完善多环境隔离、身份失败诊断及报告脱敏，无需新增业务配置。
- MCP 本机工具严格校验 JSON-RPC 请求，非法 ID 或通知不能触发更新或图片读取。
- 修复 GitHub 限流后 Gitee 缺少附件大小导致检查更新失败；下载仍校验体积上限、非空、SHA-256 和二进制版本。检查失败保留已知本机版本，并在会话开始时要求 AI 明确提示，避免误报「已是最新」。
- 修订三类客户端接入与升级说明，明确安装文件更新后仍需分别重连 MCP；WorkBuddy 可在面板信任、启用及重新连接服务。

验证：CLI 85 项顶层测试通过，含 race；WorkBuddy 本地隔离项目完成 63 次实际工具调用，覆盖评论、聊天附图、去重、状态回写和预期拒绝；正式连接的更新检查连续两次复测通过。未执行生产业务写入，非本机平台只做构建与格式检查。

配套 cloud 0.1.42 源码提供评论及软件管理等能力；自建 cloud 仍需部署对应源码。CLI 发布、安装及客户端重连是三个独立步骤；检查更新不会自动安装。

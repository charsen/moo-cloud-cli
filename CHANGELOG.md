# 更新记录

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

package main

import (
	"fmt"
	"os"
)

// 构建时通过 -ldflags "-X main.version=..." 注入。
var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printHelp()
		os.Exit(1)
	}

	switch args[0] {
	case "preflight":
		os.Exit(runPreflight(args[1:]))
	case "mcp-server":
		os.Exit(runMcpServer(args[1:]))
	case "agent-config":
		os.Exit(runAgentConfig(args[1:]))
	case "todo":
		os.Exit(runTodo(args[1:]))
	case "version", "--version", "-v":
		fmt.Println("moo-cloud " + version)
		os.Exit(0)
	case "help", "-h", "--help":
		printHelp()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "未知子命令：%s\n\n", args[0])
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Print(`moo-cloud —— Moo Cloud 本地 CLI（无运行时依赖）

用法:
  moo-cloud preflight [选项]        身份校验 + 拉取异常/待办队列
  moo-cloud mcp-server [选项]       本地 stdio MCP 桥（给 Codex / WorkBuddy 等）
  moo-cloud agent-config [选项]     生成/写入各客户端的 MCP 配置
  moo-cloud todo add [选项]         记录一条云端待办

preflight:
  --root=<dir>      工作区根目录（默认当前目录）
  --date=<Y-m-d>    输出日期目录（默认上海当天）
  --out=<dir>       输出目录（默认 <root>/moo-cloud-reports/<date>）
  --offline         跳过云端身份校验
  --fetch           拉取待处理队列
  --types=<list>    runtime,slow_query,frontend_error,app_error；待办用 todo
  --token=          账号级 MCP token（默认 MOO_CLOUD_MCP_TOKEN 或 ~/.config/moo-fleet/mcp-token）
  --cloud=          cloud 地址（默认 https://c.mooeen.com）
  --limit=<n>       每类最多条数（1-50，默认 5）
  --concurrency=<n> 项目并发数（1-16，默认 4）
  --json            输出机器可读的 preflight 结果
  --metrics-log=<f> 追加一行 JSON 指标（趋势日志）

mcp-server:
  --token=<token>   --cloud=<url>   --log=<file>

todo add:
  --project=<slug>  目标项目（必填）
  --title=<text>    待办标题（或用 --from-issue 自动生成）
  --from-issue=runtime:<key>  从问题生成标题/描述并记录来源
  --json            输出机器可读 JSON

agent-config:
  --target=codex|workbuddy|json|claude
  --write           写入配置（默认只打印）
  --force           覆盖同名配置
  --home=<dir>      覆盖 HOME（演练用）
`)
}

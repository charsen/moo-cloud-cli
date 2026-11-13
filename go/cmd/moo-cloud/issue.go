package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// issue 子命令把云端「异常」的写回能力暴露到命令行，便于脚本/CI 关闭问题。
// 能力全集与个人处理范围是两个维度：这里默认按能力全集动作，是否放行由云端
// 依账号处理范围与类别策略（auto/confirm/hold）判定，CLI 不自行收窄。
type issueResolveOptions struct {
	project, typ, key, note, resolvedBy, token, cloud string
	json, yes                                         bool
}

func runIssue(args []string) int {
	if len(args) == 0 || contains(args, "--help") || contains(args, "-h") || args[0] == "help" {
		printIssueHelp()
		return 0
	}
	if args[0] != "resolve" {
		fmt.Fprintf(os.Stderr, "未知 issue 子命令：%s\n\n", args[0])
		printIssueHelp()
		return 1
	}

	opts, err := parseIssueResolveOptions(args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "参数错误："+err.Error())
		return 1
	}
	cloud, token, err := resolveCloud(opts.cloud, opts.token)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	note := strings.TrimSpace(opts.note)
	callArgs := map[string]any{
		"project": opts.project,
		"type":    opts.typ,
		"key":     strings.ToLower(opts.key),
		"note":    note,
	}
	if opts.resolvedBy != "" {
		callArgs["resolved_by"] = opts.resolvedBy
	}

	// 写操作必须用户显式同意；非交互（脚本/AI）要求 --yes。
	if !opts.yes {
		info, statErr := os.Stdin.Stat()
		if statErr != nil || info.Mode()&os.ModeCharDevice == 0 {
			return failConfirm("解决问题需要确认，请在终端确认或加 --yes")
		}
		fmt.Fprintf(os.Stderr, "将把 %s 问题 %s 标记为已解决\n", opts.typ, strings.ToLower(opts.key))
		fmt.Fprintf(os.Stderr, "说明：%s\n", note)
		fmt.Fprint(os.Stderr, "确认？[y/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if reply := strings.ToLower(strings.TrimSpace(line)); reply != "y" && reply != "yes" && reply != "是" {
			return failConfirm("已取消")
		}
	}

	data, err := mcpCall(cloud, token, "resolve_issue", callArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "解决问题失败："+err.Error())
		return 1
	}
	if opts.json {
		printJSONObject(data)
		return 0
	}
	issue, _ := data["issue"].(map[string]any)
	fmt.Printf("问题 %v 已标记为 %v（项目 %v）\n", issue["key"], issue["status"], data["project"])
	if resolvedAt, _ := issue["resolved_at"].(string); resolvedAt != "" {
		fmt.Printf("解决时间：%v\n", resolvedAt)
	}
	if resolvedBy, _ := issue["resolved_by"].(string); resolvedBy != "" {
		fmt.Printf("解决人：%v\n", resolvedBy)
	}
	if already, _ := issue["already_resolved"].(bool); already {
		fmt.Println("此前已解决，本次未重复写入。")
	}

	return 0
}

func printIssueHelp() {
	fmt.Print(`用法：moo-cloud issue resolve --project=<slug> --type=<类型> --key=<12位key> --note="..." [--yes]

  --type=runtime|slow_query|frontend_error|app_error  问题类别（能力全集）
  --key=<12位十六进制>  问题 key，来自 list_issues
  --note="..."         必填：根因 / 改动 / 分支 sha / 验证结果
  --resolved-by=...    可选，默认取当前账号名
  --yes                非交互确认（脚本/AI 必须显式加），仍受云端范围与策略校验
  --json               输出机器可读 JSON
  --token=... --cloud=...  沿用既有默认值

例：
  moo-cloud issue resolve --project=my-project --type=runtime --key=abcdef123456 \
    --note="根因 xxx；改动 yyy；fix/zzz abc1234；回归通过" --yes
`)
}

func parseIssueResolveOptions(args []string) (issueResolveOptions, error) {
	opts := issueResolveOptions{}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") {
			return opts, fmt.Errorf("请使用 --选项=值")
		}
		key, value, hasValue := strings.Cut(arg[2:], "=")
		if key == "json" || key == "yes" {
			enabled, err := parseBoolFlag(key, value, hasValue)
			if err != nil {
				return opts, err
			}
			if key == "json" {
				opts.json = enabled
			} else {
				opts.yes = enabled
			}
			continue
		}
		if !hasValue || strings.TrimSpace(value) == "" {
			return opts, fmt.Errorf("--%s 需要非空值", key)
		}
		switch key {
		case "project":
			opts.project = value
		case "type":
			opts.typ = value
		case "key":
			opts.key = value
		case "note":
			opts.note = value
		case "resolved-by":
			opts.resolvedBy = strings.TrimSpace(value)
		case "token":
			opts.token = value
		case "cloud":
			opts.cloud = value
		default:
			return opts, fmt.Errorf("未知参数 --%s", key)
		}
	}
	if strings.TrimSpace(opts.project) == "" {
		return opts, fmt.Errorf("--project 必填")
	}
	if !isIssueType(opts.typ) {
		return opts, fmt.Errorf("--type 只能是 runtime / slow_query / frontend_error / app_error")
	}
	if !isIssueKey(opts.key) {
		return opts, fmt.Errorf("--key 必须是 12 位十六进制 hash")
	}
	if strings.TrimSpace(opts.note) == "" {
		return opts, fmt.Errorf("--note 必填：写清根因 / 改动 / 分支 sha / 验证结果")
	}

	return opts, nil
}

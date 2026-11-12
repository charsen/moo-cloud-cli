package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// todoOptions 是 `moo-cloud todo add` 的参数。标题/描述都可由 --from-issue 反查后补齐，
// 因此这里只做“有没有显式传值”的区分，空字符串表示未传。
type todoOptions struct {
	project     string
	title       string
	description string
	category    string
	priority    string
	assignee    string
	fromIssue   string
	sourceType  string
	sourceKey   string
	token       string
	cloud       string
	json        bool
	yes         bool
}

func runTodo(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printTodoHelp()
		return 0
	}
	if args[0] != "add" {
		printTodoHelp()
		return 1
	}
	for _, arg := range args[1:] {
		if arg == "--help" || arg == "-h" || arg == "help" {
			printTodoHelp()
			return 0
		}
	}

	opts, err := parseTodoOptions(args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "参数错误："+err.Error())
		return 1
	}

	result, err := addTodo(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "记录待办失败："+err.Error())
		return 1
	}

	if opts.json {
		raw, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(raw))
		return 0
	}

	todo, _ := result["todo"].(map[string]any)
	title, _ := todo["title"].(string)
	id, _ := todo["id"].(string)
	status, _ := todo["status"].(string)
	action := "created"
	if deduped, _ := result["deduped"].(bool); deduped {
		action = "deduped"
	}
	fmt.Printf("%s todo %s · %s · %s\n", action, id, result["project"], status)
	if title != "" {
		fmt.Println(title)
	}

	return 0
}

func printTodoHelp() {
	fmt.Print(`用法：moo-cloud todo add --project=<slug> --title="..." [选项]

选项:
  --description=...   详细描述（--from-issue 时默认取问题 markdown）
  --category=bug|task 默认 bug
  --priority=low|normal|high 默认 normal
  --assignee=me|<成员邮箱> 默认 me
  --from-issue=runtime:<key>  从问题生成标题/描述，并写入来源
  --source-type=...   显式来源类型（配合 --source-key）
  --source-key=...    12 位问题 key
  --token=...         覆盖 token
  --cloud=...         覆盖 cloud 地址
  --json              输出机器可读 JSON
  --yes               跳过交互确认（仍会走两阶段确认令牌）

例:
  moo-cloud todo add --project=colorfulhome --title="修复登录页报错"
  moo-cloud todo add --project=colorfulhome --from-issue=runtime:abcdef123456
`)
}

func parseTodoOptions(args []string) (todoOptions, error) {
	opts := todoOptions{category: "bug", priority: "normal"}

	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") {
			return opts, fmt.Errorf("不支持非选项参数：%s", arg)
		}
		key, value, hasValue := strings.Cut(arg[2:], "=")
		if key == "json" || key == "yes" {
			enabled := true
			if hasValue && value == "false" {
				enabled = false
			}
			if key == "json" {
				opts.json = enabled
			} else {
				opts.yes = enabled
			}
			continue
		}
		if !hasValue {
			return opts, fmt.Errorf("--%s 缺少值", key)
		}
		switch key {
		case "project":
			opts.project = value
		case "title":
			opts.title = value
		case "description":
			opts.description = value
		case "category":
			opts.category = value
		case "priority":
			opts.priority = value
		case "assignee":
			opts.assignee = value
		case "from-issue":
			opts.fromIssue = value
		case "source-type":
			opts.sourceType = value
		case "source-key":
			opts.sourceKey = value
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
	if opts.category != "bug" && opts.category != "task" {
		return opts, fmt.Errorf("--category 只能是 bug / task")
	}
	if opts.priority != "low" && opts.priority != "normal" && opts.priority != "high" {
		return opts, fmt.Errorf("--priority 只能是 low / normal / high")
	}
	if opts.fromIssue != "" {
		if _, _, err := splitIssueRef(opts.fromIssue); err != nil {
			return opts, err
		}
	}
	if opts.fromIssue == "" && (opts.sourceType != "" || opts.sourceKey != "") {
		if opts.sourceType == "" || opts.sourceKey == "" {
			return opts, fmt.Errorf("--source-type 与 --source-key 必须成对出现")
		}
	}

	return opts, nil
}

// addTodo 先按需从问题反查标题/描述，再调云端 create_todo。
func addTodo(opts todoOptions) (map[string]any, error) {
	token := strings.TrimSpace(opts.token)
	if token == "" {
		token = initialToken()
	}
	if token == "" {
		return nil, fmt.Errorf("缺少 token（--token / MOO_CLOUD_MCP_TOKEN / ~/.config/moo-fleet/mcp-token）")
	}

	cloud := strings.TrimRight(strings.TrimSpace(opts.cloud), "/")
	if cloud == "" {
		cloud = strings.TrimRight(os.Getenv("MOO_CLOUD_URL"), "/")
	}
	if cloud == "" {
		cloud = "https://c.mooeen.com"
	}

	definitions, err := mcpDefinitions(cloud, token)
	if err != nil {
		return nil, err
	}
	supportsConfirmation := false
	for _, tool := range definitions {
		if tool.Name == "create_todo" {
			_, supportsConfirmation = tool.InputSchema.Properties["confirm_token"]
		}
	}
	if !supportsConfirmation {
		return nil, fmt.Errorf("云端未提供带项目确认的 create_todo，请先升级 cloud 或检查写权限")
	}

	title := strings.TrimSpace(opts.title)
	description := strings.TrimSpace(opts.description)
	source := map[string]any(nil)

	if opts.fromIssue != "" {
		typ, key, err := splitIssueRef(opts.fromIssue)
		if err != nil {
			return nil, err
		}
		detail, err := mcpCall(cloud, token, "get_issue", map[string]any{
			"project": opts.project, "type": typ, "key": key,
		})
		if err != nil {
			return nil, fmt.Errorf("读取问题失败：%w", err)
		}
		if title == "" {
			title = issueTitle(typ, detail)
		}
		if description == "" {
			if issue, ok := detail["issue"].(map[string]any); ok {
				if md, _ := issue["markdown"].(string); md != "" {
					description = clipRunes(md, 5000)
				}
			}
		}
		source = map[string]any{"type": typ, "key": key}
	} else if opts.sourceType != "" {
		if !isIssueType(opts.sourceType) {
			return nil, fmt.Errorf("--source-type 不支持：%s", opts.sourceType)
		}
		if !isIssueKey(opts.sourceKey) {
			return nil, fmt.Errorf("--source-key 必须是 12 位十六进制 hash")
		}
		source = map[string]any{"type": opts.sourceType, "key": strings.ToLower(opts.sourceKey)}
	}

	if title == "" {
		return nil, fmt.Errorf("--title 必填（或用 --from-issue 自动生成）")
	}

	category := opts.category
	if category == "" {
		category = "bug"
	}
	priority := opts.priority
	if priority == "" {
		priority = "normal"
	}

	args := map[string]any{
		"project":  opts.project,
		"title":    clipRunes(title, 200),
		"category": category,
		"priority": priority,
	}
	if description != "" {
		args["description"] = clipRunes(description, 5000)
	}
	if opts.assignee != "" {
		args["assignee"] = opts.assignee
	}
	if source != nil {
		args["source"] = source
	}

	preview, err := mcpCall(cloud, token, "create_todo", args)
	if err != nil {
		return nil, err
	}

	requires, _ := preview["requires_confirmation"].(bool)
	if !requires {
		return nil, fmt.Errorf("云端未启用项目确认（可能需要先升级 cloud）")
	}

	tokenValue, _ := preview["confirm_token"].(string)
	if tokenValue == "" {
		return nil, fmt.Errorf("云端要求确认项目，但没有返回 confirm_token")
	}

	project, _ := preview["project"].(map[string]any)
	name, _ := project["name"].(string)
	slug, _ := project["slug"].(string)

	if !opts.yes {
		info, statErr := os.Stdin.Stat()
		if statErr != nil || info.Mode()&os.ModeCharDevice == 0 {
			return nil, fmt.Errorf("需要确认项目「%s」（%s）：请在终端确认，或加 --yes", name, slug)
		}

		fmt.Fprintf(os.Stderr, "将写入项目「%s」（%s）\n", name, slug)
		fmt.Fprintf(os.Stderr, "待办：%v\n", args["title"])
		fmt.Fprint(os.Stderr, "确认？[y/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes", "是":
		default:
			return nil, fmt.Errorf("已取消")
		}
	}

	args["confirm_token"] = tokenValue

	return mcpCall(cloud, token, "create_todo", args)
}

func splitIssueRef(ref string) (string, string, error) {
	parts := strings.SplitN(strings.TrimSpace(ref), ":", 2)
	if len(parts) != 2 || !isIssueType(parts[0]) || !isIssueKey(parts[1]) {
		return "", "", fmt.Errorf("--from-issue 格式应为 runtime:<12 位 key>")
	}

	return parts[0], strings.ToLower(parts[1]), nil
}

func isIssueType(v string) bool {
	switch v {
	case "runtime", "slow_query", "frontend_error", "app_error":
		return true
	}

	return false
}

func isIssueKey(v string) bool {
	if len(v) != 12 {
		return false
	}
	for _, r := range v {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}

	return true
}

// issueTitle 只取足够定位问题的最短信息，避免把整段堆栈塞进待办标题。
func issueTitle(typ string, detail map[string]any) string {
	issue, _ := detail["issue"].(map[string]any)

	switch typ {
	case "runtime":
		exc, _ := issue["exception"].(map[string]any)
		class, _ := exc["class"].(string)
		msg, _ := exc["message"].(string)
		return clipRunes(oneLine("["+class+"] "+msg), 200)
	case "slow_query":
		sql, _ := issue["sql_last"].(string)
		return clipRunes(oneLine("[慢 SQL] "+sql), 200)
	case "frontend_error", "app_error":
		name, _ := issue["err_name"].(string)
		msg, _ := issue["err_message"].(string)
		return clipRunes(oneLine("["+name+"] "+msg), 200)
	}

	return ""
}

// oneLine 把异常消息里的换行/连续空白压成单行，避免待办标题在列表里被撑开。
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clipRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)

	return string(runes[:max])
}

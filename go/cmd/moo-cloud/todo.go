package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
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
	images      []string
}

func runTodo(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printTodoHelp()
		return 0
	}

	// 兼容旧写法：`todo --project=...`（无子命令）仍按 add 处理，避免打断既有脚本。
	action, rest := args[0], args[1:]
	if strings.HasPrefix(action, "--") {
		action, rest = "add", args
	}
	for _, arg := range rest {
		if arg == "--help" || arg == "-h" || arg == "help" {
			printTodoHelp()
			return 0
		}
	}

	switch action {
	case "add":
		return runTodoAdd(rest)
	case "list":
		return runTodoList(rest)
	case "start":
		return runTodoUpdate(rest, "in_progress")
	case "done":
		return runTodoUpdate(rest, "done")
	default:
		fmt.Fprintf(os.Stderr, "未知 todo 子命令：%s\n\n", action)
		printTodoHelp()
		return 1
	}
}

func runTodoAdd(args []string) int {
	opts, err := parseTodoOptions(args)
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
	if len(opts.images) > 0 {
		fmt.Printf("图片已保存：%v 张（新增 %v 张）\n", result["images_attached"], result["images_added"])
	}

	return 0
}

func printTodoHelp() {
	fmt.Print(`用法：
  moo-cloud todo add   --project=<slug> --title="..." [选项]
  moo-cloud todo list  --project=<slug> [--status=open|in_progress|done] [--limit --offset] [--respect-scope]
  moo-cloud todo start --project=<slug> --id=<TODO ID> [--note="..."]
  moo-cloud todo done  --project=<slug> --id=<TODO ID> --note="...（根因/改动/分支 sha/验证）"

add 选项:
  --image=<path>     附加图片，可重复指定，最多 5 张，每张不超过 8 MiB
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

list 选项:
  --status=...        可选 open / in_progress / done；默认 open+in_progress
  --limit=20 --offset=0  分页，limit 1–50
  --respect-scope     只列出「我在本项目处理范围」内的待办（默认按能力全集读）

start/done 选项:
  --id=<TODO ID>      待办 ID（ULID）
  --note="..."        done 必填，作为完成结果写入云端；start 选填
  --yes               非交互确认（脚本/AI 必须显式加）
  --json              输出机器可读 JSON

  说明：start 即领取——写入 in_progress 并记录领取人（已有经办人则保留），不要未领取就静默处理。

例:
  moo-cloud todo add  --project=my-project --title="修复登录页报错"
  moo-cloud todo list --project=my-project --status=done --limit=20
  moo-cloud todo done --project=my-project --id=01ARZ3NDEKTSV4RRFFQ69G5FAV --note="已修复，回归通过；fix/xxx abc1234" --yes
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
		if !hasValue {
			return opts, fmt.Errorf("--%s 缺少值", key)
		}
		switch key {
		case "project":
			opts.project = value
		case "title":
			opts.title = value
		case "image":
			opts.images = append(opts.images, value)
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

// addTodo 按当前 cloud 契约先获取项目预览，确认后携带短期令牌创建待办。
// 首次调用只预览，不写入；--yes 也必须完成这两步。
func addTodo(opts todoOptions) (map[string]any, error) {
	token := strings.TrimSpace(opts.token)
	if token == "" {
		token = initialToken()
	}
	if token == "" {
		return nil, fmt.Errorf("缺少 token（--token / MOO_CLOUD_MCP_TOKEN / ~/.config/moo-fleet/mcp-token）")
	}

	cloud, err := validateCloudURL(opts.cloud)
	if err != nil {
		return nil, err
	}
	if cloud == "" {
		cloud, err = validateCloudURL(os.Getenv("MOO_CLOUD_URL"))
		if err != nil {
			return nil, err
		}
	}
	if cloud == "" {
		cloud = "https://c.mooeen.com"
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

	images, err := loadTodoImages(opts.images, false)
	if err != nil {
		return nil, err
	}
	if len(images) > 0 {
		args["images"] = images
	}

	preview, err := mcpCallNoRetry(cloud, token, "create_todo", args)
	if err != nil {
		return nil, err
	}

	requires, _ := preview["requires_confirmation"].(bool)
	if !requires {
		return nil, fmt.Errorf("云端创建待办响应缺少项目确认信息")
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

	result, err := mcpCallNoRetry(cloud, token, "create_todo", args)
	if err != nil {
		return nil, err
	}
	if err := verifyTodoImages(result, len(images)); err != nil {
		return nil, err
	}
	return result, nil
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

// resolveCloud 统一解析 cloud 地址与 token：显式参数优先，其次环境变量/本地文件。
func resolveCloud(cloudFlag, tokenFlag string) (string, string, error) {
	token := strings.TrimSpace(tokenFlag)
	if token == "" {
		token = initialToken()
	}
	if token == "" {
		return "", "", fmt.Errorf("缺少 token（--token / MOO_CLOUD_MCP_TOKEN / ~/.config/moo-fleet/mcp-token）")
	}
	cloud, err := validateCloudURL(cloudFlag)
	if err != nil {
		return "", "", err
	}
	if cloud == "" {
		cloud, err = validateCloudURL(os.Getenv("MOO_CLOUD_URL"))
		if err != nil {
			return "", "", err
		}
	}
	if cloud == "" {
		cloud = "https://c.mooeen.com"
	}

	return cloud, token, nil
}

// printJSONObject 输出缩进 JSON，供各命令的 --json 分支复用。
func printJSONObject(v any) {
	raw, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(raw))
}

var todoIDPattern = regexp.MustCompile(`^[0-9A-Za-z]{20,32}$`)

func validTodoID(id string) bool {
	return todoIDPattern.MatchString(strings.TrimSpace(id))
}

// assigneeLabel 从待办对象里取领取人（assignee.name 或 id），无则空串。
func assigneeLabel(todo map[string]any) string {
	assignee, _ := todo["assignee"].(map[string]any)
	if name, _ := assignee["name"].(string); strings.TrimSpace(name) != "" {
		return name
	}
	if id, _ := assignee["id"].(string); strings.TrimSpace(id) != "" {
		return id
	}
	return ""
}

// ---------------------------------------------------------------- todo list

type todoListOptions struct {
	project, status, token, cloud string
	limit, offset                 int
	json, respectScope            bool
}

func runTodoList(args []string) int {
	opts, err := parseTodoListOptions(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "参数错误："+err.Error())
		return 1
	}
	cloud, token, err := resolveCloud(opts.cloud, opts.token)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	callArgs := map[string]any{"project": opts.project, "limit": opts.limit, "offset": opts.offset}
	if opts.status != "" {
		callArgs["status"] = opts.status
	}
	// 能力全集默认读；只有显式 --respect-scope 才收窄到「我在本项目的处理范围」。
	if opts.respectScope {
		callArgs["respect_scope"] = true
	}

	data, err := mcpCall(cloud, token, "list_todos", callArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "查询待办失败："+err.Error())
		return 1
	}
	if opts.json {
		printJSONObject(data)
		return 0
	}
	for _, item := range anySlice(data["todos"]) {
		todo, _ := item.(map[string]any)
		line := fmt.Sprintf("[%v] %v · %v · %v", todo["id"], todo["status"], todo["priority"], todo["title"])
		if name := assigneeLabel(todo); name != "" {
			line += " · 领取人 " + name
		}
		fmt.Println(line)
		if note, _ := todo["done_note"].(string); note != "" {
			fmt.Printf("    结果：%v\n", oneLine(note))
		}
	}
	fmt.Printf("本页待办：%v 条\n", data["count"])
	if data["has_more"] == true {
		fmt.Printf("下一页：--offset=%v\n", data["next_offset"])
	}

	return 0
}

func parseTodoListOptions(args []string) (todoListOptions, error) {
	opts := todoListOptions{limit: 20, offset: 0}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") {
			return opts, fmt.Errorf("请使用 --选项=值")
		}
		key, value, hasValue := strings.Cut(arg[2:], "=")
		if key == "json" || key == "respect-scope" {
			enabled, err := parseBoolFlag(key, value, hasValue)
			if err != nil {
				return opts, err
			}
			if key == "json" {
				opts.json = enabled
			} else {
				opts.respectScope = enabled
			}
			continue
		}
		if !hasValue || strings.TrimSpace(value) == "" {
			return opts, fmt.Errorf("--%s 需要非空值", key)
		}
		switch key {
		case "project":
			opts.project = value
		case "status":
			opts.status = value
		case "token":
			opts.token = value
		case "cloud":
			opts.cloud = value
		case "limit":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 50 {
				return opts, fmt.Errorf("--limit 需为 1–50")
			}
			opts.limit = n
		case "offset":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return opts, fmt.Errorf("--offset 需为非负整数")
			}
			opts.offset = n
		default:
			return opts, fmt.Errorf("未知参数 --%s", key)
		}
	}
	if strings.TrimSpace(opts.project) == "" {
		return opts, fmt.Errorf("--project 必填")
	}
	if opts.status != "" && opts.status != "open" && opts.status != "in_progress" && opts.status != "done" {
		return opts, fmt.Errorf("--status 只能是 open / in_progress / done")
	}

	return opts, nil
}

// ---------------------------------------------------------------- todo start / done

type todoUpdateOptions struct {
	project, id, note, token, cloud string
	json, yes                       bool
}

func runTodoUpdate(args []string, status string) int {
	opts, err := parseTodoUpdateOptions(args, status == "done")
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
	callArgs := map[string]any{"project": opts.project, "id": opts.id, "status": status}
	if note != "" {
		callArgs["note"] = note
	}

	// 写操作必须用户显式同意；脚本/AI 非交互场景要求 --yes。
	if !opts.yes {
		info, statErr := os.Stdin.Stat()
		if statErr != nil || info.Mode()&os.ModeCharDevice == 0 {
			return failConfirm("将待办标记为 " + status + " 需要确认，请在终端确认或加 --yes")
		}
		fmt.Fprintf(os.Stderr, "将把待办 %s 标记为 %s\n", opts.id, status)
		if note != "" {
			fmt.Fprintf(os.Stderr, "结果：%s\n", note)
		}
		fmt.Fprint(os.Stderr, "确认？[y/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if reply := strings.ToLower(strings.TrimSpace(line)); reply != "y" && reply != "yes" && reply != "是" {
			return failConfirm("已取消")
		}
	}

	data, err := mcpCallNoRetry(cloud, token, "update_todo_status", callArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "更新待办失败："+err.Error())
		return 1
	}
	if opts.json {
		printJSONObject(data)
		return 0
	}
	todo, _ := data["todo"].(map[string]any)
	fmt.Printf("待办 %v 已更新为 %v\n", todo["id"], todo["status"])
	if name := assigneeLabel(todo); name != "" {
		fmt.Printf("领取人：%v\n", name)
	}
	if saved, _ := todo["done_note"].(string); saved != "" {
		fmt.Printf("结果：%v\n", oneLine(saved))
	}

	return 0
}

// failConfirm 统一打印错误并返回失败退出码。
func failConfirm(msg string) int {
	fmt.Fprintln(os.Stderr, msg)
	return 1
}

func parseTodoUpdateOptions(args []string, requireNote bool) (todoUpdateOptions, error) {
	opts := todoUpdateOptions{}
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
		if !hasValue {
			return opts, fmt.Errorf("--%s 缺少值", key)
		}
		switch key {
		case "project":
			opts.project = value
		case "id":
			opts.id = value
		case "note":
			opts.note = value
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
	if !validTodoID(opts.id) {
		return opts, fmt.Errorf("--id 必须是有效的待办 ID（ULID）")
	}
	if requireNote && strings.TrimSpace(opts.note) == "" {
		return opts, fmt.Errorf("--note 必填：写清根因 / 改动 / 分支 sha / 验证，作为完成结果")
	}

	return opts, nil
}

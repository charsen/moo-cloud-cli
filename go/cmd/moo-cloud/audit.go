package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// audit 是只读的对账能力：按「能力全集」扫描项目，找出
//   1) 已完成但没写处理说明（status=done 且 done_note 为空）
//   2) 长期未处理的 open 待办
//   3) 长期卡住的 in_progress 待办
//   4) 疑似已修复但未关闭的异常（open 且 >N 天未再出现）
// 是否正确回写由使用者判断；命令本身不写云端，也不改变任何状态。
// 注意：能力全集与「个人处理范围」是两个维度，默认按全集扫描；只有 --respect-scope
// 才把读取收窄到「我在本项目处理范围」，两者不混用。

type auditOptions struct {
	projects     []string
	types        []string
	staleDays    int
	maxPages     int
	respectScope bool
	json         bool
	out          string
	token, cloud string
}

type auditItem struct {
	ID        string `json:"id,omitempty"`
	Title     string `json:"title,omitempty"`
	Status    string `json:"status,omitempty"`
	Priority  string `json:"priority,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	DoneAt    string `json:"done_at,omitempty"`
	DoneBy    string `json:"done_by,omitempty"`
	Type      string `json:"type,omitempty"`
	Key       string `json:"key,omitempty"`
	Summary   string `json:"summary,omitempty"`
	LastSeen  string `json:"last_seen,omitempty"`
	Env       string `json:"env,omitempty"`
}

type auditProject struct {
	Project         string                 `json:"project"`
	DoneWithoutNote []auditItem            `json:"done_without_note"`
	OpenStale       []auditItem            `json:"open_stale"`
	InProgressLong  []auditItem            `json:"in_progress_long"`
	IssuesStaleOpen map[string][]auditItem `json:"issues_stale_open"`
	IssueOpenCounts map[string]int         `json:"issue_open_counts"`
	Scanned         map[string]int         `json:"scanned"`
	Errors          []string               `json:"errors,omitempty"`
}

func runAudit(args []string) int {
	if contains(args, "--help") || contains(args, "-h") {
		printAuditHelp()
		return 0
	}
	opts, err := parseAuditOptions(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "参数错误："+err.Error())
		return 1
	}
	cloud, token, err := resolveCloud(opts.cloud, opts.token)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	now := time.Now()
	reports := make([]auditProject, 0, len(opts.projects))
	exitErr := false
	for _, project := range opts.projects {
		report := auditProject{
			Project:         project,
			IssuesStaleOpen: map[string][]auditItem{},
			IssueOpenCounts: map[string]int{},
			Scanned:         map[string]int{},
		}
		scanProject(cloud, token, project, opts, now, &report)
		if len(report.Errors) > 0 {
			exitErr = true
		}
		reports = append(reports, report)
	}

	if opts.json {
		printJSONObject(map[string]any{
			"generated_at": now.Format(time.RFC3339),
			"stale_days":   opts.staleDays,
			"scope":        scopeLabel(opts.respectScope),
			"projects":     reports,
		})
		if exitErr {
			return 1
		}
		return 0
	}

	md := renderAuditMarkdown(reports, opts, now)
	if opts.out != "" {
		if err := os.MkdirAll(dirOf(opts.out), 0o775); err != nil {
			fmt.Fprintln(os.Stderr, "创建目录失败："+err.Error())
			return 1
		}
		if err := os.WriteFile(opts.out, []byte(md), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "写入审计报告失败："+err.Error())
			return 1
		}
		fmt.Printf("审计报告已写入：%s\n", opts.out)
		fmt.Print(auditSummaryLine(reports))
	} else {
		fmt.Print(md)
	}

	if exitErr {
		return 1
	}
	return 0
}

func scanProject(cloud, token, project string, opts auditOptions, now time.Time, report *auditProject) {
	base := func(extra map[string]any) map[string]any {
		args := map[string]any{"project": project}
		// 只有显式开启时收窄到个人范围；默认按能力全集读。
		if opts.respectScope {
			args["respect_scope"] = true
		}
		for k, v := range extra {
			args[k] = v
		}
		return args
	}

	// open + in_progress 一次性拉取，再按状态分类。
	todos, err := fetchPages("list_todos", base(nil), cloud, token, opts.maxPages)
	if err != nil {
		report.Errors = append(report.Errors, "list_todos: "+err.Error())
	}
	for _, todo := range todos {
		switch status, _ := todo["status"].(string); status {
		case "open":
			report.Scanned["todos_open"]++
			if olderThanDays(str(todo["created_at"]), opts.staleDays, now) {
				report.OpenStale = append(report.OpenStale, todoAuditItem(todo))
			}
		case "in_progress":
			report.Scanned["todos_in_progress"]++
			if olderThanDays(str(todo["created_at"]), opts.staleDays, now) {
				report.InProgressLong = append(report.InProgressLong, todoAuditItem(todo))
			}
		}
	}

	// 已完成待办：找出没写 done_note 的。
	done, err := fetchPages("list_todos", base(map[string]any{"status": "done"}), cloud, token, opts.maxPages)
	if err != nil {
		report.Errors = append(report.Errors, "list_todos(done): "+err.Error())
	}
	for _, todo := range done {
		report.Scanned["todos_done"]++
		if strings.TrimSpace(str(todo["done_note"])) == "" {
			report.DoneWithoutNote = append(report.DoneWithoutNote, todoAuditItem(todo))
		}
	}

	// 各类 open 异常：统计未关闭数量，并挑出长期未再出现的（疑似已修复未关闭）。
	for _, typ := range opts.types {
		issues, err := fetchPages("list_issues", base(map[string]any{"type": typ, "status": "open"}), cloud, token, opts.maxPages)
		if err != nil {
			report.Errors = append(report.Errors, "list_issues("+typ+"): "+err.Error())
			continue
		}
		report.Scanned["issues_"+typ] = len(issues)
		for _, issue := range issues {
			report.IssueOpenCounts[typ]++
			if olderThanDays(str(issue["last_seen"]), opts.staleDays, now) {
				report.IssuesStaleOpen[typ] = append(report.IssuesStaleOpen[typ], issueAuditItem(typ, issue))
			}
		}
	}
}

// fetchPages 按服务端 next_offset 翻页拉全量；maxPages 兜底防止异常游标死循环。
func fetchPages(tool string, baseArgs map[string]any, cloud, token string, maxPages int) ([]map[string]any, error) {
	var all []map[string]any
	offset := 0
	key := "todos"
	if tool == "list_issues" {
		key = "issues"
	}
	for page := 0; page < maxPages; page++ {
		args := map[string]any{"limit": 50, "offset": offset}
		for k, v := range baseArgs {
			args[k] = v
		}
		data, err := mcpCall(cloud, token, tool, args)
		if err != nil {
			return all, err
		}
		for _, item := range anySlice(data[key]) {
			if row, ok := item.(map[string]any); ok {
				all = append(all, row)
			}
		}
		hasMore, _ := data["has_more"].(bool)
		if !hasMore {
			break
		}
		next, ok := data["next_offset"].(float64)
		if !ok || int(next) <= offset {
			break
		}
		offset = int(next)
	}
	return all, nil
}

func todoAuditItem(todo map[string]any) auditItem {
	return auditItem{
		ID:        str(todo["id"]),
		Title:     str(todo["title"]),
		Status:    str(todo["status"]),
		Priority:  str(todo["priority"]),
		CreatedAt: str(todo["created_at"]),
		DoneAt:    str(todo["done_at"]),
		DoneBy:    str(todo["done_by"]),
	}
}

func issueAuditItem(typ string, issue map[string]any) auditItem {
	return auditItem{
		Type:     typ,
		Key:      str(issue["key"]),
		Summary:  issueSummary(issue),
		LastSeen: str(issue["last_seen"]),
		Env:      str(issue["env"]),
	}
}

func issueSummary(issue map[string]any) string {
	for _, key := range []string{"exc_message", "sql_preview", "err_message", "err_name"} {
		if s := strings.TrimSpace(str(issue[key])); s != "" {
			return oneLine(s)
		}
	}
	return ""
}

func olderThanDays(iso string, days int, now time.Time) bool {
	if iso == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return false
	}
	return now.Sub(t) > time.Duration(days)*24*time.Hour
}

func renderAuditMarkdown(reports []auditProject, opts auditOptions, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# moo-cloud 审计报告\n\n")
	fmt.Fprintf(&b, "- 生成时间：%s\n", now.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "- 逾期阈值：%d 天\n", opts.staleDays)
	fmt.Fprintf(&b, "- 范围：%s\n", scopeLabel(opts.respectScope))
	fmt.Fprintf(&b, "- 项目：%s\n\n", strings.Join(opts.projects, "、"))

	for _, r := range reports {
		fmt.Fprintf(&b, "## 项目 %s\n\n", r.Project)
		for _, e := range r.Errors {
			fmt.Fprintf(&b, "> 读取异常：%s\n", e)
		}
		if len(r.Errors) > 0 {
			b.WriteString("\n")
		}

		fmt.Fprintf(&b, "### 已完成但无处理说明（%d）\n\n", len(r.DoneWithoutNote))
		if len(r.DoneWithoutNote) == 0 {
			b.WriteString("无。\n\n")
		} else {
			b.WriteString("| 待办 ID | 标题 | 完成人 | 完成时间 |\n|---|---|---|---|\n")
			for _, it := range r.DoneWithoutNote {
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", it.ID, cell(it.Title), it.DoneBy, it.DoneAt)
			}
			b.WriteString("\n")
		}

		fmt.Fprintf(&b, "### open 且超过 %d 天未处理（%d）\n\n", opts.staleDays, len(r.OpenStale))
		if len(r.OpenStale) == 0 {
			b.WriteString("无。\n\n")
		} else {
			b.WriteString("| 待办 ID | 标题 | 创建时间 |\n|---|---|---|\n")
			for _, it := range r.OpenStale {
				fmt.Fprintf(&b, "| %s | %s | %s |\n", it.ID, cell(it.Title), it.CreatedAt)
			}
			b.WriteString("\n")
		}

		fmt.Fprintf(&b, "### in_progress 且超过 %d 天未完成（%d）\n\n", opts.staleDays, len(r.InProgressLong))
		if len(r.InProgressLong) == 0 {
			b.WriteString("无。\n\n")
		} else {
			b.WriteString("| 待办 ID | 标题 | 创建时间 |\n|---|---|---|\n")
			for _, it := range r.InProgressLong {
				fmt.Fprintf(&b, "| %s | %s | %s |\n", it.ID, cell(it.Title), it.CreatedAt)
			}
			b.WriteString("\n")
		}

		staleCount := 0
		for _, typ := range opts.types {
			staleCount += len(r.IssuesStaleOpen[typ])
		}
		fmt.Fprintf(&b, "### 疑似已处理未关闭的异常（open 且 >%d 天未再出现，%d）\n\n", opts.staleDays, staleCount)
		if staleCount == 0 {
			b.WriteString("无。\n\n")
		} else {
			b.WriteString("| 类型 | key | 摘要 | 最近出现 |\n|---|---|---|---|\n")
			for _, typ := range opts.types {
				for _, it := range r.IssuesStaleOpen[typ] {
					fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", typ, it.Key, cell(it.Summary), it.LastSeen)
				}
			}
			b.WriteString("\n")
		}

		b.WriteString("### 各类未关闭异常数\n\n")
		for _, typ := range opts.types {
			fmt.Fprintf(&b, "- %s：%d\n", typ, r.IssueOpenCounts[typ])
		}
		b.WriteString("\n")
	}

	return b.String()
}

func auditSummaryLine(reports []auditProject) string {
	var b strings.Builder
	for _, r := range reports {
		stale := 0
		for _, items := range r.IssuesStaleOpen {
			stale += len(items)
		}
		fmt.Fprintf(&b, "%s：done 无说明 %d，open 逾期 %d，in_progress 长期 %d，疑似未关闭异常 %d\n",
			r.Project, len(r.DoneWithoutNote), len(r.OpenStale), len(r.InProgressLong), stale)
	}
	return b.String()
}

func printAuditHelp() {
	fmt.Print(`用法：moo-cloud audit --project=<slug>[,<slug>...] [选项]

只读对账：不写云端、不改状态。默认按「能力全集」扫描所有类别，不按个人处理范围过滤。

选项:
  --project=a,b       要审计的项目 slug，可用逗号分隔多个（必填）
  --types=runtime,slow_query,frontend_error,app_error  只扫指定异常类别（默认全部）
  --stale-days=7      「逾期/长期」阈值天数，默认 7
  --max-pages=40      每个列表最多翻页数，防异常游标死循环，默认 40
  --respect-scope     只看「我在本项目处理范围」内的数据（默认全集，两者是两个维度）
  --out=<path>        写入 Markdown 报告文件；不填则打印到标准输出
  --json              输出机器可读 JSON
  --token=... --cloud=...  沿用既有默认值

输出四类可疑项：已完成但无处理说明；open 且超期未处理；in_progress 且超期未完成；
open 且长期未再出现（疑似已修复未关闭）的异常。
`)
}

func scopeLabel(respectScope bool) string {
	if respectScope {
		return "仅我的处理范围（--respect-scope）"
	}
	return "能力全集（未按个人处理范围过滤）"
}

// cell 让单元格内容可安全放进 Markdown 表格。
func cell(s string) string {
	s = oneLine(s)
	return strings.ReplaceAll(s, "|", `\|`)
}

func dirOf(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[:i]
	}
	return "."
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func parseAuditOptions(args []string) (auditOptions, error) {
	opts := auditOptions{
		types:     []string{"runtime", "slow_query", "frontend_error", "app_error"},
		staleDays: 7,
		maxPages:  40,
	}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") {
			return opts, fmt.Errorf("请使用 --选项=值")
		}
		key, value, hasValue := strings.Cut(arg[2:], "=")
		switch key {
		case "json", "respect-scope":
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
			for _, slug := range strings.Split(value, ",") {
				if slug = strings.TrimSpace(slug); slug != "" {
					opts.projects = append(opts.projects, slug)
				}
			}
		case "types":
			opts.types = opts.types[:0]
			for _, typ := range strings.Split(value, ",") {
				typ = strings.TrimSpace(typ)
				if typ == "" {
					continue
				}
				if !isIssueType(typ) {
					return opts, fmt.Errorf("--types 含不支持的类别：%s", typ)
				}
				opts.types = append(opts.types, typ)
			}
			if len(opts.types) == 0 {
				return opts, fmt.Errorf("--types 不能为空")
			}
		case "stale-days":
			n, err := atoiRange(value, 0, 3650, "--stale-days")
			if err != nil {
				return opts, err
			}
			opts.staleDays = n
		case "max-pages":
			n, err := atoiRange(value, 1, 1000, "--max-pages")
			if err != nil {
				return opts, err
			}
			opts.maxPages = n
		case "out":
			opts.out = value
		case "token":
			opts.token = value
		case "cloud":
			opts.cloud = value
		default:
			return opts, fmt.Errorf("未知参数 --%s", key)
		}
	}
	if len(opts.projects) == 0 {
		return opts, fmt.Errorf("--project 必填（可用逗号分隔多个项目）")
	}

	return opts, nil
}

func atoiRange(value string, min, max int, name string) (int, error) {
	n := 0
	if _, err := fmt.Sscanf(value, "%d", &n); err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s 需为 %d–%d 的整数", name, min, max)
	}
	return n, nil
}

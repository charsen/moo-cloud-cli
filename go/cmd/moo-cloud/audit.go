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
	projects       []string
	types          []string
	staleDays      int
	maxPages       int
	respectScope   bool
	json           bool
	failOnFindings bool
	out            string
	root           string
	token, cloud   string
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
	Warnings        []string               `json:"warnings,omitempty"`
	Errors          []string               `json:"errors,omitempty"`
	Summary         map[string]any         `json:"summary,omitempty"`
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
	// 未显式给 --project 时，从当前目录及下两层的 .moo-cloud.json 自动取 slug。
	if len(opts.projects) == 0 {
		opts.projects = autoProjectSlugs(opts.root)
		if len(opts.projects) == 0 {
			fmt.Fprintln(os.Stderr, "未指定 --project，且未在 --root 内找到任何 .moo-cloud.json")
			return 1
		}
	}
	cloud, token, err := resolveCloud(opts.cloud, opts.token)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	now := time.Now()
	reports := make([]auditProject, 0, len(opts.projects))
	hasError := false
	for _, project := range opts.projects {
		report := auditProject{
			Project:         project,
			IssuesStaleOpen: map[string][]auditItem{},
			IssueOpenCounts: map[string]int{},
			Scanned:         map[string]int{},
		}
		scanProject(cloud, token, project, opts, now, &report)
		if len(report.Errors) > 0 {
			hasError = true
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
		return auditExitCode(opts, reports, hasError)
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

	return auditExitCode(opts, reports, hasError)
}

// auditExitCode：错误优先；--fail-on-findings 且有违规时返回 2（区别于参数/网络错误的 1）。
func auditExitCode(opts auditOptions, reports []auditProject, hasError bool) int {
	if hasError {
		return 1
	}
	if opts.failOnFindings && countFindings(reports) > 0 {
		return 2
	}
	return 0
}

func countFindings(reports []auditProject) int {
	n := 0
	for _, r := range reports {
		n += len(r.DoneWithoutNote) + len(r.OpenStale) + len(r.InProgressLong)
		for _, items := range r.IssuesStaleOpen {
			n += len(items)
		}
	}
	return n
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

	// 云端若支持 audit_summary，顺带取一份汇总概览（失败则跳过，不影响明细）。
	if summary, ok := tryAuditSummary(cloud, token, project, opts.staleDays); ok {
		report.Summary = summary
	}

	// open + in_progress 一次性拉取，再按状态分类。
	todos, truncated, err := fetchPages("list_todos", base(nil), cloud, token, opts.maxPages)
	if err != nil {
		report.Errors = append(report.Errors, "list_todos: "+err.Error())
	}
	if truncated {
		report.Warnings = append(report.Warnings, "open/in_progress 待办结果被 --max-pages 截断，可能不完整")
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

	// 已完成待办：优先用云端 missing_note 过滤只取缺说明的；旧云端会忽略该参数，客户端再兜底过滤。
	done, truncated, err := fetchPages("list_todos", base(map[string]any{"status": "done", "missing_note": true}), cloud, token, opts.maxPages)
	if err != nil {
		report.Errors = append(report.Errors, "list_todos(done): "+err.Error())
	}
	if truncated {
		report.Warnings = append(report.Warnings, "done 待办结果被 --max-pages 截断，可能漏报")
	}
	for _, todo := range done {
		report.Scanned["todos_done"]++
		if strings.TrimSpace(str(todo["done_note"])) == "" {
			report.DoneWithoutNote = append(report.DoneWithoutNote, todoAuditItem(todo))
		}
	}

	// 各类 open 异常：统计未关闭数量，并挑出长期未再出现的（疑似已修复未关闭）。
	for _, typ := range opts.types {
		issues, truncated, err := fetchPages("list_issues", base(map[string]any{"type": typ, "status": "open"}), cloud, token, opts.maxPages)
		if err != nil {
			report.Errors = append(report.Errors, "list_issues("+typ+"): "+err.Error())
			continue
		}
		if truncated {
			report.Warnings = append(report.Warnings, "异常("+typ+")结果被 --max-pages 截断，可能漏报")
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

// tryAuditSummary 尝试调用云端 audit_summary；旧云端无此工具时静默返回 false。
func tryAuditSummary(cloud, token, project string, staleDays int) (map[string]any, bool) {
	data, err := mcpCall(cloud, token, "audit_summary", map[string]any{"project": project, "stale_days": staleDays})
	if err != nil {
		return nil, false
	}
	if _, ok := data["todos"].(map[string]any); !ok {
		return nil, false
	}
	return data, true
}

// fetchPages 按服务端 next_offset 翻页拉全量；maxPages 兜底防止异常游标死循环。
// 返回 truncated=true 表示触达 maxPages 上限而可能仍有更多，调用方应告警而不是当作完整。
func fetchPages(tool string, baseArgs map[string]any, cloud, token string, maxPages int) ([]map[string]any, bool, error) {
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
			return all, false, err
		}
		for _, item := range anySlice(data[key]) {
			if row, ok := item.(map[string]any); ok {
				all = append(all, row)
			}
		}
		hasMore, _ := data["has_more"].(bool)
		if !hasMore {
			return all, false, nil
		}
		next, ok := data["next_offset"].(float64)
		if !ok || int(next) <= offset {
			return all, false, nil
		}
		offset = int(next)
		// 翻满 maxPages 但仍有更多数据 → 截断。
		if page == maxPages-1 {
			return all, true, nil
		}
	}
	return all, false, nil
}

// autoProjectSlugs 从 root 及下两层的 .moo-cloud.json 收集项目 slug，免手填 --project。
func autoProjectSlugs(root string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(slug string) {
		slug = strings.TrimSpace(slug)
		if slug != "" && !seen[slug] {
			seen[slug] = true
			out = append(out, slug)
		}
	}
	for _, path := range findMarkers(root) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		data, _, err := decodeMarker(raw)
		if err != nil {
			continue
		}
		add(data.Project)
		for _, d := range data.Deployments {
			add(d.Project)
		}
	}
	sortStrings(out)
	return out
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
		for _, w := range r.Warnings {
			fmt.Fprintf(&b, "> ⚠️ %s\n", w)
		}
		if len(r.Errors) > 0 || len(r.Warnings) > 0 {
			b.WriteString("\n")
		}

		if r.Summary != nil {
			todos, _ := r.Summary["todos"].(map[string]any)
			fmt.Fprintf(&b, "> 云端汇总(audit_summary)：done 无说明 %v，open 逾期 %v，in_progress 长期 %v\n\n",
				todos["done_without_note"], todos["open_stale"], todos["in_progress_long"])
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
	fmt.Print(`用法：moo-cloud audit [--project=<slug>[,<slug>...]] [选项]

只读对账：不写云端、不改状态。默认按「能力全集」扫描所有类别，不按个人处理范围过滤。
不给 --project 时，从 --root（默认当前目录）及下两层的 .moo-cloud.json 自动取项目 slug。

选项:
  --project=a,b       要审计的项目 slug，逗号分隔；省略则自动从 marker 取
  --root=<dir>        自动发现 marker 的根目录，默认当前目录
  --types=runtime,slow_query,frontend_error,app_error  只扫指定异常类别（默认全部）
  --stale-days=7      「逾期/长期」阈值天数，默认 7
  --max-pages=40      每个列表最多翻页数，防异常游标死循环；超限会告警而非静默截断
  --respect-scope     只看「我在本项目处理范围」内的数据（默认全集，两者是两个维度）
  --fail-on-findings  发现违规时退出码 2（便于 CI/流水线卡质量）
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
		root:      ".",
	}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") {
			return opts, fmt.Errorf("请使用 --选项=值")
		}
		key, value, hasValue := strings.Cut(arg[2:], "=")
		switch key {
		case "json", "respect-scope", "fail-on-findings":
			enabled, err := parseBoolFlag(key, value, hasValue)
			if err != nil {
				return opts, err
			}
			switch key {
			case "json":
				opts.json = enabled
			case "respect-scope":
				opts.respectScope = enabled
			default:
				opts.failOnFindings = enabled
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
		case "root":
			opts.root = value
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
	if opts.json && opts.out != "" {
		return opts, fmt.Errorf("--json 与 --out 不能同时使用（--json 输出到标准输出）")
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

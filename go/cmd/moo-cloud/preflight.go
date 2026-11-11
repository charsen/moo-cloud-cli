package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type pfOptions struct {
	root    string
	date    string
	out     string
	token   string
	cloud   string
	limit   string
	types   string
	offline bool
	fetch   bool
}

type markerFile struct {
	Role      string `json:"role"`
	Root      string `json:"root"`
	Product   string `json:"product"`
	Platforms []string
	Verify    []struct {
		File     string `json:"file"`
		Contains string `json:"contains"`
	} `json:"verify"`
	Deployments []struct {
		Project string `json:"project"`
		EnvFile string `json:"env_file"`
	} `json:"deployments"`
}

type deploymentReport struct {
	Project      string   `json:"project"`
	EnvFile      string   `json:"env_file"`
	Slug         *string  `json:"slug"`
	CloudRepoURL *string  `json:"cloud_repo_url"`
	Enabled      *bool    `json:"enabled"`
	OK           bool     `json:"ok"`
	Errors       []string `json:"errors"`
	Warnings     []string `json:"warnings"`
	Cloud        string   `json:"-"`
}

type repoReport struct {
	Repo         string             `json:"repo"`
	Marker       string             `json:"marker"`
	Role         *string            `json:"role"`
	AppRoot      *string            `json:"app_root"`
	Branch       *string            `json:"branch"`
	Dirty        *int               `json:"dirty"`
	Remote       *string            `json:"remote"`
	Deployments  []deploymentReport `json:"deployments"`
	Warnings     []string           `json:"warnings"`
	HardFailures int                `json:"hard_failures"`
	Product      string             `json:"product,omitempty"`
	Platforms    []string           `json:"platforms,omitempty"`
}

func runPreflight(args []string) int {
	if contains(args, "--help") || contains(args, "-h") {
		fmt.Print(`preflight：身份校验 + 拉取异常/待办队列

  --root=<dir>   --date=<Y-m-d>   --out=<dir>
  --offline      --fetch          --types=<list>
  --token=       --cloud=         --limit=<n>
`)

		return 0
	}

	opts := parsePfOptions(args)

	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(opts.date) {
		fmt.Fprintln(os.Stderr, "--date 需为 YYYY-MM-DD")

		return 1
	}

	markers := findMarkers(opts.root)
	if len(markers) == 0 {
		fmt.Fprintf(os.Stderr, "未找到任何 .moo-cloud.json（root=%s）\n", opts.root)

		return 1
	}

	reports := make([]repoReport, 0, len(markers))
	projectSet := []string{}
	seen := map[string]bool{}
	hardFailures := 0

	for _, markerPath := range markers {
		report := inspectRepo(markerPath, opts)
		reports = append(reports, report)
		hardFailures += report.HardFailures
		for _, dep := range report.Deployments {
			if dep.OK && !seen[dep.Project] {
				seen[dep.Project] = true
				projectSet = append(projectSet, dep.Project)
			}
		}
	}

	outDir := opts.out
	if outDir == "" {
		outDir = filepath.Join(opts.root, "moo-cloud-reports", opts.date)
	}
	if err := os.MkdirAll(outDir, 0o775); err != nil {
		fmt.Fprintf(os.Stderr, "无法创建输出目录：%s\n", outDir)

		return 1
	}

	var queue map[string]map[string]any
	queueError := ""
	fatal := 0
	if opts.fetch {
		switch {
		case opts.token == "":
			fmt.Fprintln(os.Stderr, "--fetch 需要账号级 token：--token=moo_pat_xxx 或环境变量 MOO_CLOUD_MCP_TOKEN")
			fatal++
			queueError = "缺少账号级 token"
		case len(projectSet) == 0:
			fmt.Fprintln(os.Stderr, "没有通过身份校验的仓库，无法拉取")
			fatal++
			queueError = "没有通过身份校验的仓库"
		default:
			cloud, err := resolveFetchCloud(opts, reports)
			if err == nil {
				queue, err = fetchQueue(cloud, opts, projectSet)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "拉取队列失败：%v\n", err)
				fatal++
				queueError = err.Error()
			}
		}
	}

	mode := "online"
	if opts.offline {
		mode = "offline"
	}
	var queueProjects any
	if queue != nil {
		keys := make([]string, 0, len(queue))
		for _, p := range projectSet {
			if _, ok := queue[p]; ok {
				keys = append(keys, p)
			}
		}
		queueProjects = keys
	}

	payload := map[string]any{
		"generated_at":   time.Now().In(cst).Format(time.RFC3339),
		"mode":           mode,
		"root":           opts.root,
		"hard_failures":  hardFailures,
		"fatal":          fatal,
		"repos":          reports,
		"queue_projects": queueProjects,
	}

	writeJSON(filepath.Join(outDir, "_preflight.json"), payload)
	writeFile(filepath.Join(outDir, "_preflight.md"), renderMarkdown(payload["generated_at"].(string), mode, reports, hardFailures, queue))

	if queue != nil {
		writeJSON(filepath.Join(outDir, "_queue.json"), map[string]any{
			"generated_at": time.Now().In(cst).Format(time.RFC3339),
			"projects":     queue,
		})
	} else if opts.fetch {
		writeJSON(filepath.Join(outDir, "_queue.json"), map[string]any{
			"generated_at": time.Now().In(cst).Format(time.RFC3339),
			"error":        queueError,
			"projects":     map[string]any{},
		})
	}

	suffix := ""
	if opts.fetch {
		suffix = " + fetch"
	}
	fmt.Printf("preflight: %d 个仓库, 硬失败 %d, 致命 %d, 模式 %s%s\n输出: %s\n",
		len(reports), hardFailures, fatal, mode, suffix, outDir)

	if fatal > 0 {
		return 1
	}

	return 0
}

func parsePfOptions(args []string) pfOptions {
	cwd, _ := os.Getwd()
	opts := pfOptions{
		root:  cwd,
		date:  time.Now().In(cst).Format("2006-01-02"),
		token: initialToken(),
		cloud: os.Getenv("MOO_CLOUD_URL"),
		limit: "5",
		types: "runtime,slow_query,frontend_error,app_error",
	}

	known := map[string]*string{
		"root": &opts.root, "date": &opts.date, "out": &opts.out,
		"token": &opts.token, "cloud": &opts.cloud, "limit": &opts.limit, "types": &opts.types,
	}

	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") {
			fmt.Fprintf(os.Stderr, "忽略非选项参数：%s\n", arg)

			continue
		}
		key, value, hasValue := strings.Cut(arg[2:], "=")
		if key == "offline" || key == "fetch" {
			enabled := true
			if hasValue {
				if parsed, err := strconv.ParseBool(value); err == nil {
					enabled = parsed
				}
			}
			if key == "offline" {
				opts.offline = enabled
			} else {
				opts.fetch = enabled
			}

			continue
		}
		target, ok := known[key]
		if !ok {
			fmt.Fprintf(os.Stderr, "未知参数：--%s\n", key)

			continue
		}
		if !hasValue {
			fmt.Fprintf(os.Stderr, "参数 --%s 缺少值，已忽略（保持默认）\n", key)

			continue
		}
		*target = value
	}

	return opts
}

func findMarkers(root string) []string {
	patterns := []string{filepath.Join(root, "*", ".moo-cloud.json"), filepath.Join(root, "*", "*", ".moo-cloud.json")}
	markers := []string{}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		markers = append(markers, matches...)
	}
	sortStrings(markers)

	return markers
}

func inspectRepo(markerPath string, opts pfOptions) repoReport {
	repoRoot := filepath.Dir(markerPath)
	report := repoReport{
		Repo: repoRoot, Marker: markerPath,
		Deployments: []deploymentReport{}, Warnings: []string{},
	}

	raw, err := os.ReadFile(markerPath)
	if err != nil {
		report.Warnings = append(report.Warnings, ".moo-cloud.json 读取失败")
		report.HardFailures++

		return report
	}
	var data markerFile
	if err := json.Unmarshal(raw, &data); err != nil {
		report.Warnings = append(report.Warnings, ".moo-cloud.json 解析失败或缺 deployments")
		report.HardFailures++

		return report
	}

	if data.Role == "app" {
		return inspectAppRepo(markerPath, repoRoot, data, report)
	}

	report.Role = strPtr("backend")
	if len(data.Deployments) == 0 {
		report.Warnings = append(report.Warnings, ".moo-cloud.json 解析失败或缺 deployments")
		report.HardFailures++

		return report
	}

	appRoot := filepath.Join(repoRoot, data.Root)
	if data.Root == "" {
		appRoot = repoRoot
	}
	report.AppRoot = strPtr(appRoot)
	if st, err := os.Stat(filepath.Join(appRoot, "artisan")); err != nil || st.IsDir() {
		report.Warnings = append(report.Warnings, "app root 不存在或缺 artisan："+appRoot)
		report.HardFailures++

		return report
	}

	applyGitState(appRoot, repoRoot, &report)

	for _, dep := range data.Deployments {
		report.Deployments = append(report.Deployments, inspectDeployment(appRoot, derefStr(report.Remote), dep.Project, dep.EnvFile, opts))
	}
	for _, dep := range report.Deployments {
		if !dep.OK {
			report.HardFailures++
		}
	}

	return report
}

func inspectAppRepo(markerPath, repoRoot string, data markerFile, report repoReport) repoReport {
	report.Role = strPtr("app")
	report.AppRoot = strPtr(repoRoot)
	report.Product = data.Product
	report.Platforms = data.Platforms
	if report.Platforms == nil {
		report.Platforms = []string{}
	}

	if data.Product == "" {
		report.Warnings = append(report.Warnings, "app marker 缺 product")
		report.HardFailures++

		return report
	}

	applyGitState(repoRoot, repoRoot, &report)

	if len(data.Verify) == 0 {
		report.Warnings = append(report.Warnings, "app marker 没有 verify，无法校验包名指纹")
		report.HardFailures++
	}
	for _, rule := range data.Verify {
		file := filepath.Join(repoRoot, rule.File)
		raw, err := os.ReadFile(file)
		if rule.Contains == "" || err != nil {
			report.Warnings = append(report.Warnings, "指纹文件不存在："+rule.File)
			report.HardFailures++

			continue
		}
		if !strings.Contains(string(raw), rule.Contains) {
			report.Warnings = append(report.Warnings, fmt.Sprintf("指纹不匹配：%s 不含 %s", rule.File, rule.Contains))
			report.HardFailures++
		}
	}

	return report
}

func applyGitState(appRoot, repoRoot string, report *repoReport) {
	if code, top := gitCmd(appRoot, "rev-parse", "--show-toplevel"); code != 0 || realpath(top) != realpath(repoRoot) {
		report.Warnings = append(report.Warnings, fmt.Sprintf("git 根目录与 marker 位置不一致（%s）", top))
		report.HardFailures++
	}

	if _, branch := gitCmd(appRoot, "rev-parse", "--abbrev-ref", "HEAD"); branch != "" {
		report.Branch = strPtr(branch)
	}

	statusCode, status := gitCmd(appRoot, "status", "--porcelain")
	if statusCode == 0 {
		dirty := 0
		markerUntracked := false
		for _, line := range strings.Split(status, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if strings.HasPrefix(line, "??") && strings.Contains(line, ".moo-cloud.json") {
				markerUntracked = true

				continue
			}
			dirty++
		}
		report.Dirty = intPtr(dirty)
		if markerUntracked {
			report.Warnings = append(report.Warnings, ".moo-cloud.json 尚未提交：未跟踪文件会阻断 pull.sh 部署，确认后需随任务分支提交")
		}
		if dirty > 0 {
			report.Warnings = append(report.Warnings, fmt.Sprintf("工作区不干净（%d 个文件）：夜间任务应跳过该仓库，避免把别人的改动一起提交", dirty))
		}
	}

	branch := derefStr(report.Branch)
	if branch == "dev" || branch == "main" || branch == "master" {
		report.Warnings = append(report.Warnings, "当前在基础分支 "+branch+"：修复必须先切 fix 分支")
	}

	if _, remote := gitCmd(appRoot, "remote", "get-url", "origin"); remote != "" {
		report.Remote = strPtr(remote)
	}
}

func inspectDeployment(appRoot, remote, project, envFile string, opts pfOptions) deploymentReport {
	entry := deploymentReport{
		Project: project, EnvFile: envFile,
		Errors: []string{}, Warnings: []string{},
	}

	if project == "" || envFile == "" {
		entry.Errors = append(entry.Errors, "deployment 缺 project 或 env_file")

		return entry
	}

	envPath := filepath.Join(appRoot, envFile)
	if _, err := os.Stat(envPath); err != nil {
		entry.Errors = append(entry.Errors, "env 文件不存在："+envFile)

		return entry
	}

	if raw, ok := envValue(envPath, "MOO_MONITOR_CLOUD_ENABLED"); ok {
		enabled := strings.EqualFold(raw, "true") || raw == "1"
		entry.Enabled = &enabled
		if !enabled {
			entry.Warnings = append(entry.Warnings, "该 env 的云端上报是关闭的（enabled=false），不会有新数据")
		}
	}

	token, _ := envValue(envPath, "MOO_MONITOR_CLOUD_TOKEN")
	if token == "" {
		entry.Errors = append(entry.Errors, "env 缺 MOO_MONITOR_CLOUD_TOKEN")

		return entry
	}

	cloud := opts.cloud
	if cloud == "" {
		cloud, _ = envValue(envPath, "MOO_MONITOR_CLOUD_URL")
		if cloud == "" {
			cloud = "https://c.mooeen.com"
		}
	}
	cloud = strings.TrimRight(cloud, "/")
	entry.Cloud = cloud

	if opts.offline {
		entry.OK = true
		entry.Warnings = append(entry.Warnings, "offline 模式：未反查云端 slug")

		return entry
	}

	status, body, err := httpJSON(cloud+"/api/v1/summary", map[string]any{"token": token}, nil, 20*time.Second)
	if err != nil {
		entry.Errors = append(entry.Errors, "云端不可达："+err.Error())

		return entry
	}
	if status != 200 {
		entry.Errors = append(entry.Errors, fmt.Sprintf("summary 返回 HTTP %d（token 失效或权限不对）", status))

		return entry
	}

	var summary struct {
		Project struct {
			Slug    string  `json:"slug"`
			RepoURL *string `json:"repo_url"`
		} `json:"project"`
	}
	if err := json.Unmarshal(body, &summary); err != nil {
		entry.Errors = append(entry.Errors, "summary 响应不是 JSON")

		return entry
	}

	slug := summary.Project.Slug
	entry.Slug = strPtr(slug)
	entry.CloudRepoURL = summary.Project.RepoURL

	if slug != project {
		entry.Errors = append(entry.Errors, fmt.Sprintf("slug 不一致：marker=%s，云端=%s", project, slug))

		return entry
	}

	if entry.CloudRepoURL == nil || *entry.CloudRepoURL == "" {
		entry.Warnings = append(entry.Warnings, "云端未填 repo_url（建议在项目设置里补上，增加一层防错）")
	} else if normalizeRemote(*entry.CloudRepoURL) != normalizeRemote(remote) {
		entry.Warnings = append(entry.Warnings, fmt.Sprintf("repo_url 与本地 origin 不一致：云端=%s，本地=%s", *entry.CloudRepoURL, remote))
	}

	entry.OK = true

	return entry
}

func resolveFetchCloud(opts pfOptions, reports []repoReport) (string, error) {
	if explicit := strings.TrimRight(opts.cloud, "/"); explicit != "" {
		return explicit, nil
	}

	clouds := []string{}
	for _, report := range reports {
		for _, dep := range report.Deployments {
			cloud := strings.TrimRight(dep.Cloud, "/")
			if cloud != "" && !contains(clouds, cloud) {
				clouds = append(clouds, cloud)
			}
		}
	}

	switch len(clouds) {
	case 0:
		return "https://c.mooeen.com", nil
	case 1:
		return clouds[0], nil
	default:
		return "", fmt.Errorf("部署指向多个云端（%s），请用 --cloud 指定拉取目标", strings.Join(clouds, "、"))
	}
}

func fetchQueue(cloud string, opts pfOptions, projects []string) (map[string]map[string]any, error) {
	limit := 5
	if n, err := strconv.Atoi(opts.limit); err == nil {
		limit = n
	} else {
		fmt.Fprintf(os.Stderr, "警告：--limit 不是数字（%s），使用默认 5\n", opts.limit)
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 50 {
		limit = 50
	}

	types := parseTypes(opts.types)
	if len(types) == 0 {
		return nil, fmt.Errorf("没有可拉取的类型（--types）")
	}

	knownResp, err := mcpCall(cloud, opts.token, "list_projects", map[string]any{})
	if err != nil {
		return nil, err
	}
	known := []string{}
	handled := map[string][]string{}
	for _, item := range anySlice(knownResp["projects"]) {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		slug, _ := m["slug"].(string)
		if slug == "" {
			continue
		}
		known = append(known, slug)
		if raw, ok := m["handled_types"].([]any); ok {
			scope := make([]string, 0, len(raw))
			for _, v := range raw {
				if typ, ok := v.(string); ok {
					scope = append(scope, typ)
				}
			}
			handled[slug] = scope
		}
	}

	available, err := mcpTools(cloud, opts.token)
	if err != nil {
		return nil, err
	}

	queue := map[string]map[string]any{}
	// 项目间并发（最多 4 个），每个项目内部仍按类型顺序拉取；结果写回由 mutex 保护。
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)

	for _, project := range projects {
		if !contains(known, project) {
			fmt.Fprintf(os.Stderr, "警告：%s 不在该账号的云端项目清单里，跳过拉取\n", project)

			continue
		}

		wg.Add(1)
		go func(project string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result := map[string]any{}
			for _, typ := range types {
				// 账号默认 + 项目覆盖后的范围；服务端旧版本没有该字段时按全部处理。
				if scope, ok := handled[project]; ok && !contains(scope, typ) {
					result[typ] = map[string]any{"issues": []any{}, "count": 0, "skipped": "不在处理范围"}

					continue
				}

				tool := "list_issues"
				if typ == "todo" {
					tool = "list_todos"
				}
				if !contains(available, tool) {
					fmt.Fprintf(os.Stderr, "警告：服务端未提供 %s，跳过 %s 的 %s\n", tool, project, typ)
					result[typ] = map[string]any{"issues": []any{}, "count": 0, "skipped": "服务端未提供 " + tool}

					continue
				}

				one, err := fetchOneType(cloud, opts.token, project, typ, limit)
				if err != nil {
					fmt.Fprintf(os.Stderr, "警告：%s 的 %s 拉取失败，跳过：%v\n", project, typ, err)
					result[typ] = map[string]any{"issues": []any{}, "count": 0, "error": err.Error()}

					continue
				}
				result[typ] = one
			}

			mu.Lock()
			queue[project] = result
			mu.Unlock()
		}(project)
	}

	wg.Wait()
	return queue, nil
}

func fetchOneType(cloud, token, project, typ string, limit int) (map[string]any, error) {
	if typ == "todo" {
		result, err := mcpCall(cloud, token, "list_todos", map[string]any{"project": project, "limit": limit})
		if err != nil {
			return nil, err
		}
		issues := anySlice(result["todos"])
		for _, item := range issues {
			if m, ok := item.(map[string]any); ok {
				if _, exists := m["ai_policy"]; !exists {
					m["ai_policy"] = "confirm"
				}
			}
		}
		result["issues"] = issues
		result["count"] = len(issues)

		return result, nil
	}

	if typ == "frontend_error" || typ == "app_error" {
		return fetchIssuesPaged(cloud, token, project, typ, limit)
	}

	args := map[string]any{"project": project, "type": typ, "limit": limit}
	if typ == "runtime" || typ == "slow_query" {
		args["env"] = "production"
	}
	result, err := mcpCall(cloud, token, "list_issues", args)
	if err != nil {
		return nil, err
	}
	issues := anySlice(result["issues"])
	result["issues"] = issues
	result["count"] = len(issues)

	return result, nil
}

func fetchIssuesPaged(cloud, token, project, typ string, limit int) (map[string]any, error) {
	collected := []any{}
	offset := 0
	pages := 0
	var last map[string]any

	for len(collected) < limit && pages < 10 {
		result, err := mcpCall(cloud, token, "list_issues", map[string]any{
			"project": project, "type": typ, "limit": limit, "offset": offset,
		})
		if err != nil {
			return nil, err
		}
		last = result
		rows := anySlice(result["issues"])
		collected = append(collected, filterIssues(typ, rows)...)
		pages++

		hasMore, _ := result["has_more"].(bool)
		if !hasMore || len(rows) == 0 {
			break
		}
		offset += len(rows)
	}

	if last == nil {
		last = map[string]any{}
	}
	if len(collected) > limit {
		collected = collected[:limit]
	}
	last["issues"] = collected
	last["count"] = len(collected)
	last["has_more"] = false
	last["pages"] = pages

	return last, nil
}

func filterIssues(typ string, rows []any) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			out = append(out, row)

			continue
		}
		env, _ := m["env"].(string)
		env = strings.ToLower(env)
		if typ == "frontend_error" && strings.HasPrefix(env, "dev") {
			continue
		}
		if typ == "app_error" && (env == "debug" || env == "verify") {
			continue
		}
		out = append(out, row)
	}

	return out
}

func parseTypes(raw string) []string {
	allowed := []string{"runtime", "slow_query", "frontend_error", "app_error", "todo"}
	out := []string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !contains(allowed, part) {
			fmt.Fprintf(os.Stderr, "忽略未知类型：%s\n", part)

			continue
		}
		out = append(out, part)
	}

	return out
}

func renderMarkdown(generatedAt, mode string, repos []repoReport, hardFailures int, queue map[string]map[string]any) string {
	var b strings.Builder
	if queue != nil {
		mode += " + fetch"
	}
	fmt.Fprintf(&b, "# Preflight %s\n\n", generatedAt)
	fmt.Fprintf(&b, "- 模式：%s\n- 仓库：%d · 硬失败：%d\n\n", mode, len(repos), hardFailures)

	for _, report := range repos {
		fmt.Fprintf(&b, "## %s\n\n", filepath.Base(report.Repo))
		fmt.Fprintf(&b, "- app root：`%s`\n", dash(report.AppRoot))
		fmt.Fprintf(&b, "- 分支：`%s` · 脏文件：%s · remote：`%s`\n", dash(report.Branch), dirtyLabel(report.Dirty), dash(report.Remote))
		if report.Role != nil && *report.Role == "app" {
			fmt.Fprintf(&b, "- 软件：`%s` · 平台：%s\n", report.Product, strings.Join(report.Platforms, "/"))
		}
		b.WriteString("\n")
		if report.Role == nil || *report.Role != "app" {
			b.WriteString("| env | 云端 slug | 结果 | 备注 |\n|---|---|---|---|\n")
			for _, dep := range report.Deployments {
				notes := append(append([]string{}, dep.Errors...), dep.Warnings...)
				okMark := "❌"
				if dep.OK {
					okMark = "✅"
				}
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", dep.EnvFile, dash(dep.Slug), okMark, strings.ReplaceAll(strings.Join(notes, "；"), "|", "\\|"))
			}
		}
		for _, warning := range report.Warnings {
			fmt.Fprintf(&b, "\n- ⚠️ %s\n", warning)
		}
		b.WriteString("\n")
	}

	if queue != nil {
		b.WriteString("## 待处理队列（每类按 --limit 截断）\n\n")
		b.WriteString("| 项目 | runtime | slow_query | frontend | app | todo |\n|---|---:|---:|---:|---:|---:|\n")
		projectNames := make([]string, 0, len(queue))
		for name := range queue {
			projectNames = append(projectNames, name)
		}
		sortStrings(projectNames)
		for _, project := range projectNames {
			types := queue[project]
			fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d |\n", project,
				countOf(types["runtime"]), countOf(types["slow_query"]), countOf(types["frontend_error"]),
				countOf(types["app_error"]), countOf(types["todo"]))
		}
	}

	return b.String()
}

func countOf(result any) int {
	m, ok := result.(map[string]any)
	if !ok {
		return 0
	}

	return len(anySlice(m["issues"]))
}

func dash(v *string) string {
	if v == nil || *v == "" {
		return "—"
	}

	return *v
}

func dirtyLabel(dirty *int) string {
	if dirty == nil {
		return "?"
	}

	return strconv.Itoa(*dirty)
}

func gitCmd(dir string, args ...string) (int, string) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.Output()
	if err != nil {
		return 1, strings.TrimSpace(string(out))
	}

	return 0, strings.TrimSpace(string(out))
}

func realpath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}

	return abs
}

func normalizeRemote(remote string) string {
	r := strings.TrimSpace(remote)
	for _, prefix := range []string{"git@", "ssh://", "https://", "http://"} {
		r = strings.TrimPrefix(r, prefix)
	}
	// ssh://git@host/... 去掉 scheme 后还会剩一个 git@。
	r = strings.TrimPrefix(r, "git@")
	r = strings.ReplaceAll(r, ":", "/")
	r = strings.TrimRight(r, "/")
	r = strings.TrimSuffix(r, ".git")

	return strings.ToLower(strings.TrimRight(r, "/"))
}

func writeJSON(path string, v any) {
	raw, err := json.MarshalIndent(v, "", "    ")
	if err != nil {
		return
	}
	writeFile(path, string(raw)+"\n")
}

func writeFile(path, content string) {
	_ = os.MkdirAll(filepath.Dir(path), 0o775)
	_ = os.WriteFile(path, []byte(content), 0o644)
}

func strPtr(v string) *string { return &v }
func intPtr(v int) *int       { return &v }
func derefStr(v *string) string {
	if v == nil {
		return ""
	}

	return *v
}

func anySlice(v any) []any {
	if list, ok := v.([]any); ok {
		return list
	}

	return []any{}
}

func sortStrings(list []string) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j] < list[j-1]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

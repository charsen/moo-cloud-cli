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

type typeMetric struct {
	Count      int    `json:"count"`
	Skipped    string `json:"skipped,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

type projectMetric struct {
	DurationMs int64                 `json:"duration_ms"`
	Types      map[string]typeMetric `json:"types"`
}

type fetchMetrics struct {
	DurationMs int64                    `json:"duration_ms"`
	Projects   map[string]projectMetric `json:"projects"`
}

type pfOptions struct {
	root        string
	date        string
	out         string
	token       string
	cloud       string
	limit       string
	types       string
	concurrency string
	metricsLog  string
	offline     bool
	fetch       bool
	json        bool
}

type markerFile struct {
	Role      string `json:"role"`
	Root      string `json:"root"`
	Product   string `json:"product"`
	Project   string `json:"project"`
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
  --token=       --cloud=         --limit=<n>   --concurrency=<n>   --json   --metrics-log=<file>

识别当前目录及下两层的 .moo-cloud.json；可在软件仓库内直接运行。
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
	var metrics *fetchMetrics
	queueError := ""
	fatal := 0
	if opts.fetch {
		switch {
		case opts.token == "":
			fmt.Fprintln(os.Stderr, "--fetch 需要账号级 token：--token=moo_mcp_xxx 或环境变量 MOO_CLOUD_MCP_TOKEN")
			fatal++
			queueError = "缺少账号级 token"
		case len(projectSet) == 0:
			fmt.Fprintln(os.Stderr, "没有通过身份校验的仓库，无法拉取")
			fatal++
			queueError = "没有通过身份校验的仓库"
		default:
			cloud, err := resolveFetchCloud(opts, reports)
			if err == nil {
				queue, metrics, err = fetchQueue(cloud, opts, projectSet)
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
		"metrics":        metrics,
	}

	writeFailed := false
	saveJSON := func(path string, value any) {
		if err := writeJSON(path, value); err != nil {
			fmt.Fprintf(os.Stderr, "写入 %s 失败：%v\n", path, err)
			writeFailed = true
		}
	}
	saveText := func(path, value string) {
		if err := writeFile(path, value); err != nil {
			fmt.Fprintf(os.Stderr, "写入 %s 失败：%v\n", path, err)
			writeFailed = true
		}
	}
	saveJSON(filepath.Join(outDir, "_preflight.json"), payload)
	saveText(filepath.Join(outDir, "_preflight.md"), renderMarkdown(payload["generated_at"].(string), mode, reports, hardFailures, queue))

	if queue != nil {
		saveJSON(filepath.Join(outDir, "_queue.json"), map[string]any{
			"generated_at": time.Now().In(cst).Format(time.RFC3339),
			"projects":     queue,
			"error":        queueError,
		})
	} else if opts.fetch {
		saveJSON(filepath.Join(outDir, "_queue.json"), map[string]any{
			"generated_at": time.Now().In(cst).Format(time.RFC3339),
			"error":        queueError,
			"projects":     map[string]any{},
		})
	}

	if opts.fetch {
		metricsPayload := map[string]any{
			"generated_at": time.Now().In(cst).Format(time.RFC3339),
			"mode":         mode,
			"metrics":      metrics,
			"error":        queueError,
		}
		saveJSON(filepath.Join(outDir, "_metrics.json"), metricsPayload)
		if opts.metricsLog != "" {
			appendJSONLine(opts.metricsLog, metricsPayload)
		}
	}

	if writeFailed {
		return 1
	}

	if opts.json {
		raw, _ := json.MarshalIndent(payload, "", "    ")
		fmt.Println(string(raw))
	} else {
		suffix := ""
		if opts.fetch {
			suffix = " + fetch"
		}
		fmt.Printf("preflight: %d 个仓库, 硬失败 %d, 致命 %d, 模式 %s%s\n输出: %s\n",
			len(reports), hardFailures, fatal, mode, suffix, outDir)
	}

	if fatal > 0 || hardFailures > 0 {
		return 1
	}

	return 0
}

func parsePfOptions(args []string) pfOptions {
	cwd, _ := os.Getwd()
	opts := pfOptions{
		root:        cwd,
		date:        time.Now().In(cst).Format("2006-01-02"),
		token:       initialToken(),
		cloud:       os.Getenv("MOO_CLOUD_URL"),
		limit:       "5",
		types:       "runtime,slow_query,frontend_error,app_error",
		concurrency: "4",
	}

	known := map[string]*string{
		"root": &opts.root, "date": &opts.date, "out": &opts.out,
		"token": &opts.token, "cloud": &opts.cloud, "limit": &opts.limit, "types": &opts.types,
		"concurrency": &opts.concurrency, "metrics-log": &opts.metricsLog,
	}

	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") {
			fmt.Fprintf(os.Stderr, "忽略非选项参数：%s\n", arg)

			continue
		}
		key, value, hasValue := strings.Cut(arg[2:], "=")
		if key == "offline" || key == "fetch" || key == "json" {
			enabled := true
			if hasValue {
				if parsed, err := strconv.ParseBool(value); err == nil {
					enabled = parsed
				}
			}
			switch key {
			case "offline":
				opts.offline = enabled
			case "fetch":
				opts.fetch = enabled
			case "json":
				opts.json = enabled
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
	markers := []string{}
	directories := []string{root}
	seen := map[string]bool{}
	// 同时支持仓库内直接运行与既有的两层工作区扫描；固定深度避免递归进入依赖树。
	// 按真实目录去重，工作区里指向同一仓库的符号链接不会重复拉取。
	for depth := 0; depth <= 2; depth++ {
		next := []string{}
		for _, directory := range directories {
			canonical := realpath(directory)
			if seen[canonical] {
				continue
			}
			seen[canonical] = true
			marker := filepath.Join(directory, ".moo-cloud.json")
			if info, err := os.Stat(marker); err == nil && !info.IsDir() {
				markers = append(markers, marker)
			}
			if depth == 2 {
				continue
			}
			entries, _ := os.ReadDir(directory)
			for _, entry := range entries {
				path := filepath.Join(directory, entry.Name())
				if info, err := os.Stat(path); err == nil && info.IsDir() {
					next = append(next, path)
				}
			}
		}
		directories = next
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
		report.Warnings = append(report.Warnings, ".moo-cloud.json 读取失败，请检查文件路径与读取权限")
		report.HardFailures++

		return report
	}
	data, warnings, err := decodeMarker(raw)
	report.Warnings = append(report.Warnings, warnings...)
	if err != nil {
		report.Warnings = append(report.Warnings, err.Error())
		report.HardFailures++

		return report
	}

	if data.Role == "app" {
		return inspectAppRepo(markerPath, repoRoot, data, report, opts)
	}

	report.Role = strPtr("backend")
	if len(data.Deployments) == 0 {
		report.Warnings = append(report.Warnings, "后端配置缺 deployments：请填写 project 与 env_file；软件仓库应设置 role=app")
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

func inspectAppRepo(markerPath, repoRoot string, data markerFile, report repoReport, opts pfOptions) repoReport {
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
	for index, rule := range data.Verify {
		field := fmt.Sprintf("verify[%d]", index)
		if strings.TrimSpace(rule.File) == "" {
			report.Warnings = append(report.Warnings, field+".file 不能为空，请填写仓库内的相对文件路径")
			report.HardFailures++
			continue
		}
		if rule.Contains == "" {
			report.Warnings = append(report.Warnings, field+".contains 不能为空，请填写文件应包含的包名或标识")
			report.HardFailures++
			continue
		}
		clean := filepath.Clean(rule.File)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			report.Warnings = append(report.Warnings, field+".file 必须是仓库内的相对文件路径")
			report.HardFailures++
			continue
		}
		file := filepath.Join(repoRoot, rule.File)
		info, statErr := os.Stat(file)
		if statErr != nil || info.IsDir() {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s.file 无法找到可读文件：%s", field, rule.File))
			report.HardFailures++
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			report.Warnings = append(report.Warnings, field+".file 读取失败，请检查文件权限："+rule.File)
			report.HardFailures++

			continue
		}
		if !strings.Contains(string(raw), rule.Contains) {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s.contains 指纹不匹配：%s 不含指定标识", field, rule.File))
			report.HardFailures++
		}
	}

	if strings.TrimSpace(data.Project) == "" {
		report.Warnings = append(report.Warnings, "app marker 缺 project，不能确定云端项目")
		report.HardFailures++
		return report
	}
	if report.HardFailures > 0 {
		return report
	}
	cloud := strings.TrimRight(opts.cloud, "/")
	if cloud == "" {
		cloud = "https://c.mooeen.com"
	}
	dep := deploymentReport{Project: data.Project, Cloud: cloud, Errors: []string{}, Warnings: []string{}}
	if opts.offline {
		dep.OK = true
		dep.Warnings = append(dep.Warnings, "offline 模式：未反查软件与项目归属")
	} else if opts.token == "" {
		dep.Errors = append(dep.Errors, "App 身份校验需要账号级 token")
	} else {
		// App 没有 Host .env token，用账号级接口核对 product 确实属于指定项目。
		result, err := mcpCall(cloud, opts.token, "list_apps", map[string]any{"project": data.Project})
		if err != nil {
			dep.Errors = append(dep.Errors, "软件身份校验失败："+err.Error())
		} else {
			for _, item := range anySlice(result["apps"]) {
				app, ok := item.(map[string]any)
				if ok && app["slug"] == data.Product {
					dep.OK = true
					break
				}
			}
			if !dep.OK {
				dep.Errors = append(dep.Errors, "云端项目下未找到软件："+data.Product)
			}
		}
	}
	report.Deployments = append(report.Deployments, dep)
	if !dep.OK {
		report.HardFailures++
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

func fetchQueue(cloud string, opts pfOptions, projects []string) (map[string]map[string]any, *fetchMetrics, error) {
	started := time.Now()

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
		return nil, nil, fmt.Errorf("没有可拉取的类型（--types）")
	}

	knownResp, err := mcpCall(cloud, opts.token, "list_projects", map[string]any{})
	if err != nil {
		return nil, nil, err
	}
	known := []string{}
	handled := map[string][]string{}
	policies := map[string]map[string]string{}
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
		if raw, ok := m["type_policies"].(map[string]any); ok {
			mapped := map[string]string{}
			for typ, v := range raw {
				if policy, ok := v.(string); ok {
					mapped[typ] = policy
				}
			}
			policies[slug] = mapped
		}
	}

	available, err := mcpTools(cloud, opts.token)
	if err != nil {
		return nil, nil, err
	}

	queue := map[string]map[string]any{}
	metrics := &fetchMetrics{Projects: map[string]projectMetric{}}
	var mu sync.Mutex
	var wg sync.WaitGroup

	concurrency := 4
	if n, err := strconv.Atoi(opts.concurrency); err == nil {
		concurrency = n
	}
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 16 {
		concurrency = 16
	}
	sem := make(chan struct{}, concurrency)

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

			pstart := time.Now()
			result := map[string]any{}
			pm := projectMetric{Types: map[string]typeMetric{}}

			for _, typ := range types {
				tstart := time.Now()
				// 策略：hold 完全不处理；confirm 仍拉取，但结果标注 policy。
				policy := "auto"
				if typ != "todo" {
					if mapped, ok := policies[project]; ok {
						if v, ok := mapped[typ]; ok && v != "" {
							policy = v
						}
					}
				}
				if policy == "hold" {
					result[typ] = map[string]any{"issues": []any{}, "count": 0, "skipped": "策略：保留不处理"}
					pm.Types[typ] = typeMetric{Skipped: "策略：保留不处理"}

					continue
				}

				if scope, ok := handled[project]; ok && !contains(scope, typ) {
					result[typ] = map[string]any{"issues": []any{}, "count": 0, "skipped": "不在处理范围"}
					pm.Types[typ] = typeMetric{Skipped: "不在处理范围"}

					continue
				}

				tool := "list_issues"
				if typ == "todo" {
					tool = "list_todos"
				}
				if !contains(available, tool) {
					fmt.Fprintf(os.Stderr, "警告：服务端未提供 %s，跳过 %s 的 %s\n", tool, project, typ)
					result[typ] = map[string]any{"issues": []any{}, "count": 0, "skipped": "服务端未提供 " + tool}
					pm.Types[typ] = typeMetric{Skipped: "服务端未提供 " + tool}

					continue
				}

				one, err := fetchOneType(cloud, opts.token, project, typ, limit)
				if err != nil {
					fmt.Fprintf(os.Stderr, "警告：%s 的 %s 拉取失败，跳过：%v\n", project, typ, err)
					result[typ] = map[string]any{"issues": []any{}, "count": 0, "error": err.Error()}
					pm.Types[typ] = typeMetric{Error: err.Error()}

					continue
				}
				one["policy"] = policy
				result[typ] = one
				pm.Types[typ] = typeMetric{Count: len(anySlice(one["issues"])), DurationMs: time.Since(tstart).Milliseconds()}
			}

			pm.DurationMs = time.Since(pstart).Milliseconds()
			mu.Lock()
			queue[project] = result
			metrics.Projects[project] = pm
			mu.Unlock()
		}(project)
	}

	wg.Wait()
	metrics.DurationMs = time.Since(started).Milliseconds()

	successes, failures := 0, 0
	for _, project := range metrics.Projects {
		for _, metric := range project.Types {
			if metric.Error != "" {
				failures++
			} else if metric.Skipped == "" {
				successes++
			}
		}
	}
	if failures > 0 && successes == 0 {
		return queue, metrics, fmt.Errorf("所有尝试拉取的类型均失败（%d 项），详见队列与指标中的 error", failures)
	}
	if len(queue) == 0 {
		return queue, metrics, fmt.Errorf("没有账号可访问的目标项目")
	}
	return queue, metrics, nil
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
	offset, pages := 0, 0
	hasMore := false
	var last map[string]any

	// 每次读最多 50 行，持续越过调试数据，不能因固定页数而漏掉后面的生产问题。
	// 返回的 next_offset 指向实际消费位置；页内达到条数上限时不能跳过余下记录。
	for len(collected) < limit {
		result, err := mcpCall(cloud, token, "list_issues", map[string]any{
			"project": project, "type": typ, "limit": 50, "offset": offset,
		})
		if err != nil {
			return nil, err
		}
		last = result
		rows := anySlice(result["issues"])
		serverHasMore, _ := result["has_more"].(bool)
		pages++
		if len(rows) == 0 {
			if serverHasMore {
				return nil, fmt.Errorf("分页响应为空却声明还有数据，无法继续拉取")
			}
			hasMore = false
			break
		}

		consumed := 0
		for _, row := range rows {
			consumed++
			if includeIssue(typ, row) {
				collected = append(collected, row)
			}
			if len(collected) == limit {
				break
			}
		}
		nextOffset := offset + consumed
		hasMore = consumed < len(rows) || serverHasMore
		if consumed == len(rows) && serverHasMore {
			// 旧服务端可省略 next_offset；新服务端给出时必须有效且向前，避免空转。
			if raw, exists := result["next_offset"]; exists {
				value, ok := raw.(float64)
				if !ok || value != float64(int(value)) || value < float64(nextOffset) {
					return nil, fmt.Errorf("服务端返回无效的 next_offset，无法继续拉取")
				}
				nextOffset = int(value)
			}
		}
		offset = nextOffset
		if !hasMore {
			break
		}
	}

	if last == nil {
		last = map[string]any{}
	}
	last["issues"] = collected
	last["count"] = len(collected)
	last["offset"] = 0
	last["has_more"] = hasMore
	last["next_offset"] = nil
	if hasMore {
		last["next_offset"] = offset
	}
	last["pages"] = pages
	return last, nil
}

func includeIssue(typ string, row any) bool {
	m, ok := row.(map[string]any)
	if !ok {
		return true
	}
	env, _ := m["env"].(string)
	env = strings.ToLower(env)
	if typ == "frontend_error" && strings.HasPrefix(env, "dev") {
		return false
	}
	return typ != "app_error" || (env != "debug" && env != "verify")
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

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "    ")
	if err != nil {
		return err
	}
	return writeFile(path, string(raw)+"\n")
}

// 同目录临时文件写完再替换，避免磁盘写失败时把旧配置/报告截断。
func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o775); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".moo-cloud-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
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

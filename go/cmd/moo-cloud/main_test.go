package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTypes(t *testing.T) {
	got := parseTypes("runtime, todo ,nonsense,,slow_query")
	want := []string{"runtime", "todo", "slow_query"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("parseTypes=%v want %v", got, want)
	}
	if len(parseTypes("")) != 0 {
		t.Fatal("空值应返回空列表")
	}
}

func TestNormalizeRemote(t *testing.T) {
	cases := map[string]string{
		"git@gitee.com:charsen/x.git":       "gitee.com/charsen/x",
		"https://gitee.com/charsen/x.git/":  "gitee.com/charsen/x",
		"ssh://git@gitee.com/charsen/x.git": "gitee.com/charsen/x",
		"https://GITEE.com/charsen/x":       "gitee.com/charsen/x",
	}
	for in, want := range cases {
		if got := normalizeRemote(in); got != want {
			t.Errorf("normalizeRemote(%q)=%q want %q", in, got, want)
		}
	}
}

func TestEnvValue(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, ".env")
	_ = os.WriteFile(file, []byte("# c\nKEY=plain # note\nQUOTED=\"a b\"\nEMPTY=\n"), 0o644)

	if v, _ := envValue(file, "KEY"); v != "plain" {
		t.Errorf("KEY=%q", v)
	}
	if v, _ := envValue(file, "QUOTED"); v != "a b" {
		t.Errorf("QUOTED=%q", v)
	}
	if v, ok := envValue(file, "EMPTY"); !ok || v != "" {
		t.Errorf("EMPTY=%q ok=%v", v, ok)
	}
	if _, ok := envValue(file, "MISSING"); ok {
		t.Error("MISSING 不应存在")
	}
}

func TestFilterIssues(t *testing.T) {
	rows := []any{
		map[string]any{"env": "dev-huijia"},
		map[string]any{"env": "prod-huijia"},
		map[string]any{"env": ""},
	}
	if got := len(filterIssues("frontend_error", rows)); got != 2 {
		t.Fatalf("frontend 应保留 2 条，得到 %d", got)
	}
	app := []any{
		map[string]any{"env": "debug"},
		map[string]any{"env": "verify"},
		map[string]any{"env": "release"},
	}
	if got := len(filterIssues("app_error", app)); got != 1 {
		t.Fatalf("app 应保留 1 条，得到 %d", got)
	}
	if got := len(filterIssues("runtime", rows)); got != 3 {
		t.Fatalf("其他类型不筛，得到 %d", got)
	}
}

func TestParsePfOptions(t *testing.T) {
	opts := parsePfOptions([]string{"--root=/tmp/x", "--fetch=false", "--offline", "--limit=9", "--root"})
	if opts.root != "/tmp/x" {
		t.Errorf("root=%q", opts.root)
	}
	if opts.fetch {
		t.Error("--fetch=false 不应开启")
	}
	if !opts.offline {
		t.Error("--offline 应开启")
	}
	if opts.limit != "9" {
		t.Errorf("limit=%q", opts.limit)
	}
}

func TestResolveFetchCloud(t *testing.T) {
	reports := []repoReport{
		{Deployments: []deploymentReport{{Cloud: "https://a.example"}}},
		{Deployments: []deploymentReport{{Cloud: "https://b.example"}}},
	}
	if _, err := resolveFetchCloud(pfOptions{}, reports); err == nil {
		t.Fatal("多云端应报错")
	}
	one := []repoReport{{Deployments: []deploymentReport{{Cloud: "https://a.example/"}}}}
	if cloud, err := resolveFetchCloud(pfOptions{}, one); err != nil || cloud != "https://a.example" {
		t.Fatalf("cloud=%q err=%v", cloud, err)
	}
	if cloud, _ := resolveFetchCloud(pfOptions{cloud: "https://c.example/"}, reports); cloud != "https://c.example" {
		t.Fatalf("显式 cloud=%q", cloud)
	}
}

func TestInspectDeploymentOffline(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	_ = os.WriteFile(env, []byte("MOO_MONITOR_CLOUD_TOKEN=tok\nMOO_MONITOR_CLOUD_URL=https://x.example/\n"), 0o644)

	entry := inspectDeployment(dir, "", "p1", ".env", pfOptions{offline: true})
	if !entry.OK || entry.Cloud != "https://x.example" {
		t.Fatalf("offline 应通过且记录 cloud：%+v", entry)
	}

	missing := inspectDeployment(dir, "", "p1", ".env.missing", pfOptions{offline: true})
	if missing.OK || len(missing.Errors) == 0 {
		t.Fatal("缺 env 应报错")
	}
}

func TestInspectAppRepoVerify(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "app.txt"), []byte("com.example.app"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".moo-cloud.json"), []byte(`{"role":"app","product":"p","verify":[{"file":"app.txt","contains":"com.example.app"}]}`), 0o644)

	if code, _ := gitCmd(dir, "init"); code != 0 {
		t.Fatal("git init 失败")
	}
	report := inspectRepo(filepath.Join(dir, ".moo-cloud.json"), pfOptions{offline: true})
	if report.HardFailures != 0 {
		t.Fatalf("指纹匹配不应硬失败：%+v", report.Warnings)
	}

	_ = os.WriteFile(filepath.Join(dir, "app.txt"), []byte("other"), 0o644)
	report = inspectRepo(filepath.Join(dir, ".moo-cloud.json"), pfOptions{offline: true})
	if report.HardFailures == 0 {
		t.Fatal("指纹不匹配应硬失败")
	}
}

func TestStripCodexBlock(t *testing.T) {
	in := "model = \"x\"\n\n[mcp_servers.moo_cloud]\ncommand = \"a\"\n\n[mcp_servers.moo_cloud.tools.list_projects]\napproval_mode = \"approve\"\n\n[mcp_servers.other]\ncommand = \"b\"\n"
	out := stripCodexBlock(in)
	if strings.Contains(out, "moo_cloud") {
		t.Fatalf("moo_cloud 段未清干净：%s", out)
	}
	if !strings.Contains(out, "[mcp_servers.other]") || !strings.Contains(out, "model") {
		t.Fatalf("其他配置被误删：%s", out)
	}
}

func TestAgentConfigWrite(t *testing.T) {
	home := t.TempDir()
	if code := emitCodex(home, "/bin/moo-cloud", true, false); code != 0 {
		t.Fatal("codex 写入失败")
	}
	config := filepath.Join(home, ".codex", "config.toml")
	raw, _ := os.ReadFile(config)
	if !strings.Contains(string(raw), "args = [\"mcp-server\"]") {
		t.Fatalf("配置缺 mcp-server：%s", raw)
	}
	if code := emitCodex(home, "/bin/moo-cloud", true, false); code != 0 {
		t.Fatal("重复写入应跳过而非失败")
	}
	if code := emitCodex(home, "/bin/moo-cloud", true, true); code != 0 {
		t.Fatal("force 写入失败")
	}
	if n := strings.Count(string(mustRead(t, config)), "[mcp_servers.moo_cloud]"); n != 1 {
		t.Fatalf("force 后应只有一段，得到 %d", n)
	}

	// WorkBuddy：保留已有 server，只增量合并。
	workbuddy := filepath.Join(home, ".workbuddy", "mcp.json")
	_ = os.MkdirAll(filepath.Dir(workbuddy), 0o755)
	_ = os.WriteFile(workbuddy, []byte(`{"mcpServers":{"getnote":{"type":"stdio","command":"node"}}}`), 0o644)
	if code := emitWorkbuddy(home, map[string]any{"type": "stdio", "command": "/bin/moo-cloud", "args": []string{"mcp-server"}}, true, false); code != 0 {
		t.Fatal("workbuddy 写入失败")
	}
	var parsed map[string]map[string]json.RawMessage
	_ = json.Unmarshal(mustRead(t, workbuddy), &parsed)
	if _, ok := parsed["mcpServers"]["getnote"]; !ok {
		t.Fatal("已有 getnote 被覆盖")
	}
	if _, ok := parsed["mcpServers"]["moo-cloud"]; !ok {
		t.Fatal("moo-cloud 未写入")
	}
}

func TestHandleMcpLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)

			return
		}
		// 故意不带 id，验证桥会补上请求 id。
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"result":  map[string]any{"ok": true},
		})
	}))
	defer srv.Close()

	var buf bytes.Buffer
	writer := bufio.NewWriter(&buf)
	handleMcpLine(srv.URL+"/api/v1/mcp", "t", `{"jsonrpc":"2.0","id":9,"method":"tools/list","params":{}}`, writer)
	writer.Flush()
	if !strings.Contains(buf.String(), `"id":9`) {
		t.Fatalf("响应未补 id：%s", buf.String())
	}

	buf.Reset()
	handleMcpLine(srv.URL+"/api/v1/mcp", "t", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, writer)
	writer.Flush()
	if buf.Len() != 0 {
		t.Fatalf("通知不应写 stdout：%s", buf.String())
	}
}

func TestFetchQueueViaMockMCP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		reply := func(data any) {
			text, _ := json.Marshal(data)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": string(text)}},
					"isError": false,
				},
			})
		}

		switch req.Method {
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"tools": []map[string]string{
					{"name": "list_projects"}, {"name": "list_issues"}, {"name": "list_todos"},
				}},
			})
		case "tools/call":
			switch req.Params.Name {
			case "list_projects":
				reply(map[string]any{"projects": []map[string]string{{"slug": "p1"}}})
			case "list_todos":
				reply(map[string]any{"todos": []map[string]any{{"id": "01", "title": "t", "status": "open"}}})
			case "list_issues":
				typ, _ := req.Params.Arguments["type"].(string)
				offset, _ := req.Params.Arguments["offset"].(float64)
				if typ == "frontend_error" {
					if offset == 0 {
						reply(map[string]any{"issues": []map[string]any{
							{"env": "dev-x"}, {"env": "dev-y"},
						}, "has_more": true})
					} else {
						reply(map[string]any{"issues": []map[string]any{
							{"env": "prod-x"}, {"env": "prod-y"},
						}, "has_more": false})
					}

					return
				}
				reply(map[string]any{"issues": []map[string]any{{"hash": "abc"}}, "has_more": false})
			}
		}
	}))
	defer srv.Close()

	queue, err := fetchQueue(srv.URL, pfOptions{token: "t", limit: "2", types: "frontend_error,todo"}, []string{"p1"})
	if err != nil {
		t.Fatal(err)
	}
	p1 := queue["p1"]
	frontend := p1["frontend_error"].(map[string]any)["issues"].([]any)
	if len(frontend) != 2 {
		t.Fatalf("翻页后应保留 2 条生产行，得到 %d", len(frontend))
	}
	todos := p1["todo"].(map[string]any)["issues"].([]any)
	if len(todos) != 1 || todos[0].(map[string]any)["ai_policy"] != "confirm" {
		t.Fatalf("待办应默认 confirm：%+v", todos)
	}
}

func TestFetchQueueSkipsTypesOutOfScope(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		reply := func(data any) {
			text, _ := json.Marshal(data)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}, "isError": false},
			})
		}
		switch req.Method {
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"tools": []map[string]string{
					{"name": "list_projects"}, {"name": "list_issues"},
				}},
			})
		case "tools/call":
			if req.Params.Name == "list_projects" {
				reply(map[string]any{"projects": []map[string]any{
					{"slug": "p1", "handled_types": []string{"runtime"}},
				}})

				return
			}
			calls++
			reply(map[string]any{"issues": []map[string]any{}, "has_more": false})
		}
	}))
	defer srv.Close()

	queue, err := fetchQueue(srv.URL, pfOptions{token: "t", limit: "5", types: "runtime,frontend_error"}, []string{"p1"})
	if err != nil {
		t.Fatal(err)
	}
	frontend := queue["p1"]["frontend_error"].(map[string]any)
	if frontend["skipped"] != "不在处理范围" {
		t.Fatalf("范围外类型应跳过：%+v", frontend)
	}
	if calls != 1 {
		t.Fatalf("只应为 runtime 调一次 list_issues，实际 %d", calls)
	}
}

func TestFetchQueueEmptyTypes(t *testing.T) {
	if _, err := fetchQueue("http://127.0.0.1:1", pfOptions{token: "t", types: ""}, []string{"p1"}); err == nil {
		t.Fatal("空类型应报错")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return raw
}

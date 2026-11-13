package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mcpStub 起一个假的云端 MCP，按工具名/参数返回结果，供命令与审计测试复用。
func mcpStub(t *testing.T, handler func(tool string, args map[string]any) map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		data := handler(req.Params.Name, req.Params.Arguments)
		text, _ := json.Marshal(data)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{
				"content": []any{map[string]any{"type": "text", "text": string(text)}},
				"isError": false,
			},
		})
	}))
}

func TestParseTodoListOptions(t *testing.T) {
	opts, err := parseTodoListOptions([]string{"--project=p", "--status=done", "--limit=5", "--offset=10", "--respect-scope"})
	if err != nil || opts.status != "done" || opts.limit != 5 || opts.offset != 10 || !opts.respectScope {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
	for _, bad := range [][]string{
		{"--project=p", "--status=bogus"},
		{"--project=p", "--limit=0"},
		{"--project=p", "--offset=-1"},
		{"--status=done"},
		{"--project=p", "--unknown=1"},
	} {
		if _, err := parseTodoListOptions(bad); err == nil {
			t.Fatalf("应拒绝：%v", bad)
		}
	}
}

func TestTodoListDefaultsToCapabilityNotScope(t *testing.T) {
	var captured map[string]any
	server := mcpStub(t, func(tool string, args map[string]any) map[string]any {
		captured = args
		return map[string]any{"project": "p", "todos": []any{}, "count": 0, "has_more": false}
	})
	defer server.Close()
	code := runTodoList([]string{"--project=p", "--token=fixture", "--cloud=" + server.URL})
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if _, ok := captured["respect_scope"]; ok {
		t.Fatalf("默认不应传 respect_scope：%+v", captured)
	}
	if captured["project"] != "p" || captured["limit"] != float64(20) {
		t.Fatalf("参数错误：%+v", captured)
	}

	runTodoList([]string{"--project=p", "--respect-scope", "--token=fixture", "--cloud=" + server.URL})
	if captured["respect_scope"] != true {
		t.Fatalf("--respect-scope 未透传：%+v", captured)
	}
}

func TestParseTodoUpdateOptionsRequiresNoteForDone(t *testing.T) {
	if _, err := parseTodoUpdateOptions([]string{"--project=p", "--id=01ARZ3NDEKTSV4RRFFQ69G5FAV"}, true); err == nil {
		t.Fatal("done 缺 note 应报错")
	}
	if _, err := parseTodoUpdateOptions([]string{"--project=p", "--id=bad"}, false); err == nil {
		t.Fatal("非法 id 应报错")
	}
	opts, err := parseTodoUpdateOptions([]string{"--project=p", "--id=01ARZ3NDEKTSV4RRFFQ69G5FAV", "--note=已受理"}, false)
	if err != nil || opts.note != "已受理" {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
}

func TestTodoUpdateSendsStatusAndNote(t *testing.T) {
	var tool string
	var captured map[string]any
	server := mcpStub(t, func(name string, args map[string]any) map[string]any {
		tool, captured = name, args
		return map[string]any{"todo": map[string]any{"id": args["id"], "status": args["status"], "done_note": args["note"]}}
	})
	defer server.Close()
	code := runTodoUpdate([]string{"--project=p", "--id=01ARZ3NDEKTSV4RRFFQ69G5FAV", "--note=修好了", "--yes", "--token=fixture", "--cloud=" + server.URL}, "done")
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if tool != "update_todo_status" || captured["status"] != "done" || captured["note"] != "修好了" {
		t.Fatalf("tool=%s args=%+v", tool, captured)
	}
}

func TestParseIssueResolveOptions(t *testing.T) {
	base := []string{"--project=p", "--type=runtime", "--key=ABCDEF123456"}
	if _, err := parseIssueResolveOptions(base); err == nil {
		t.Fatal("缺 note 应报错")
	}
	if _, err := parseIssueResolveOptions(append(base, "--note=x", "--type=unknown")); err == nil {
		t.Fatal("非法类型应报错")
	}
	opts, err := parseIssueResolveOptions(append(base, "--note=根因+改动"))
	if err != nil || opts.key != "ABCDEF123456" || opts.note != "根因+改动" {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
}

func TestIssueResolveSupportsAllTypes(t *testing.T) {
	for _, typ := range []string{"runtime", "slow_query", "frontend_error", "app_error"} {
		var tool string
		var captured map[string]any
		server := mcpStub(t, func(name string, args map[string]any) map[string]any {
			tool, captured = name, args
			return map[string]any{"project": "p", "issue": map[string]any{"key": args["key"], "status": "resolved", "already_resolved": false}}
		})
		code := runIssue([]string{"resolve", "--project=p", "--type=" + typ, "--key=abcdef123456", "--note=说明", "--yes", "--token=fixture", "--cloud=" + server.URL})
		server.Close()
		if code != 0 || tool != "resolve_issue" || captured["type"] != typ || captured["key"] != "abcdef123456" {
			t.Fatalf("type=%s code=%d tool=%s args=%+v", typ, code, tool, captured)
		}
	}
}

func TestParseAuditOptions(t *testing.T) {
	opts, err := parseAuditOptions([]string{"--project=a,b", "--stale-days=3", "--types=runtime,frontend_error"})
	if err != nil || len(opts.projects) != 2 || opts.staleDays != 3 || len(opts.types) != 2 {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
	for _, bad := range [][]string{
		{},
		{"--project=a", "--types=bogus"},
		{"--project=a", "--stale-days=-1"},
		{"--project=a", "--max-pages=0"},
	} {
		if _, err := parseAuditOptions(bad); err == nil {
			t.Fatalf("应拒绝：%v", bad)
		}
	}
}

func TestScanProjectDetectsViolations(t *testing.T) {
	old := time.Now().AddDate(0, 0, -30).Format(time.RFC3339)
	nowISO := time.Now().Format(time.RFC3339)
	server := mcpStub(t, func(tool string, args map[string]any) map[string]any {
		switch tool {
		case "list_todos":
			if args["status"] == "done" {
				return map[string]any{"todos": []any{
					map[string]any{"id": "D", "status": "done", "done_note": "", "done_by": "x", "done_at": old},
					map[string]any{"id": "E", "status": "done", "done_note": "已处理"},
				}, "count": 2, "has_more": false}
			}
			return map[string]any{"todos": []any{
				map[string]any{"id": "A", "status": "open", "created_at": old, "title": "old open"},
				map[string]any{"id": "B", "status": "open", "created_at": nowISO, "title": "new"},
				map[string]any{"id": "C", "status": "in_progress", "created_at": old, "title": "stuck"},
			}, "count": 3, "has_more": false}
		case "list_issues":
			if args["type"] == "runtime" {
				return map[string]any{"issues": []any{map[string]any{"key": "k1", "last_seen": old, "exc_message": "boom"}}, "count": 1, "has_more": false}
			}
			return map[string]any{"issues": []any{}, "count": 0, "has_more": false}
		}
		return map[string]any{}
	})
	defer server.Close()

	report := auditProject{Project: "p", IssuesStaleOpen: map[string][]auditItem{}, IssueOpenCounts: map[string]int{}, Scanned: map[string]int{}}
	scanProject(server.URL, "fixture", "p", auditOptions{types: []string{"runtime", "slow_query", "frontend_error", "app_error"}, staleDays: 7, maxPages: 40}, time.Now(), &report)

	if len(report.DoneWithoutNote) != 1 || report.DoneWithoutNote[0].ID != "D" {
		t.Fatalf("done_without_note=%+v", report.DoneWithoutNote)
	}
	if len(report.OpenStale) != 1 || report.OpenStale[0].ID != "A" {
		t.Fatalf("open_stale=%+v", report.OpenStale)
	}
	if len(report.InProgressLong) != 1 || report.InProgressLong[0].ID != "C" {
		t.Fatalf("in_progress_long=%+v", report.InProgressLong)
	}
	if len(report.IssuesStaleOpen["runtime"]) != 1 || report.IssuesStaleOpen["runtime"][0].Key != "k1" {
		t.Fatalf("issues_stale=%+v", report.IssuesStaleOpen)
	}
	if report.IssueOpenCounts["runtime"] != 1 || report.IssueOpenCounts["slow_query"] != 0 {
		t.Fatalf("open counts=%+v", report.IssueOpenCounts)
	}
}

func TestFetchPagesFollowsCursor(t *testing.T) {
	calls := 0
	server := mcpStub(t, func(tool string, args map[string]any) map[string]any {
		calls++
		offset := int(args["offset"].(float64))
		if offset == 0 {
			return map[string]any{"todos": []any{map[string]any{"id": "1"}}, "has_more": true, "next_offset": float64(1)}
		}
		return map[string]any{"todos": []any{map[string]any{"id": "2"}}, "has_more": false}
	})
	defer server.Close()
	rows, err := fetchPages("list_todos", map[string]any{"project": "p"}, server.URL, "fixture", 40)
	if err != nil || len(rows) != 2 || calls != 2 {
		t.Fatalf("rows=%d calls=%d err=%v", len(rows), calls, err)
	}
}

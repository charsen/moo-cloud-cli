package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// showMCP 模拟 get_todo 返回：有附件 / 无附件 / 错误 三种。
func showMCP(t *testing.T, payload map[string]any, isError bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		text, _ := json.Marshal(payload)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(text)}}, "isError": isError,
		}})
	}))
}

func TestTodoShowWithAttachments(t *testing.T) {
	srv := showMCP(t, map[string]any{"todo": map[string]any{
		"id": "01m7p27bygzg2t92wvd7zr5vfq", "title": "带图待办", "status": "open",
		"priority": "high", "category": "bug", "submitter": "louis",
		"description": "现象…证据：01-x.png", "attachments_count": float64(2),
		"attachments": []any{
			map[string]any{"id": "a1", "type": "image", "mime": "image/png", "size_bytes": float64(100), "ai_readable": true},
			map[string]any{"id": "a2", "type": "image", "mime": "image/png", "size_bytes": float64(200), "ai_readable": true},
		},
	}}, false)
	defer srv.Close()

	code := runTodoShow([]string{"--project=p", "--id=01m7p27bygzg2t92wvd7zr5vfq", "--cloud=" + srv.URL, "--token=t"})
	if code != 0 {
		t.Fatalf("show 应成功，退出码 %d", code)
	}
}

func TestTodoShowNoAttachmentButMentionsScreenshot(t *testing.T) {
	srv := showMCP(t, map[string]any{"todo": map[string]any{
		"id": "01m7p27bygzg2t92wvd7zr5vfq", "title": "无图待办", "status": "open",
		"description": "截图见附件.png", "attachments": []any{},
	}}, false)
	defer srv.Close()
	if code := runTodoShow([]string{"--project=p", "--id=01m7p27bygzg2t92wvd7zr5vfq", "--cloud=" + srv.URL, "--token=t"}); code != 0 {
		t.Fatalf("show 应成功，退出码 %d", code)
	}
}

func TestTodoShowJSON(t *testing.T) {
	srv := showMCP(t, map[string]any{"todo": map[string]any{"id": "01m7p27bygzg2t92wvd7zr5vfq", "title": "x"}}, false)
	defer srv.Close()
	if code := runTodoShow([]string{"--project=p", "--id=01m7p27bygzg2t92wvd7zr5vfq", "--cloud=" + srv.URL, "--token=t", "--json"}); code != 0 {
		t.Fatalf("show --json 应成功，退出码 %d", code)
	}
}

func TestTodoShowErrorReturnsNonzero(t *testing.T) {
	srv := showMCP(t, map[string]any{"error": "not found"}, true)
	defer srv.Close()
	if code := runTodoShow([]string{"--project=p", "--id=01m7p27bygzg2t92wvd7zr5vfq", "--cloud=" + srv.URL, "--token=t"}); code == 0 {
		t.Fatal("云端错误应返回非 0")
	}
}

func TestCountTodoAttachments(t *testing.T) {
	srv := showMCP(t, map[string]any{"todo": map[string]any{"id": "T", "attachments_count": float64(3)}}, false)
	defer srv.Close()
	if n := countTodoAttachments(srv.URL, "t", "p", "T"); n != 3 {
		t.Fatalf("应用 attachments_count 得到 3，实际 %d", n)
	}
	// 无 attachments_count 时退回 attachments 数组长度。
	srv2 := showMCP(t, map[string]any{"todo": map[string]any{"id": "T", "attachments": []any{map[string]any{"id": "a"}, map[string]any{"id": "b"}}}}, false)
	defer srv2.Close()
	if n := countTodoAttachments(srv2.URL, "t", "p", "T"); n != 2 {
		t.Fatalf("应从 attachments 数组得 2，实际 %d", n)
	}
	// 错误返回 0。
	srv3 := showMCP(t, map[string]any{"error": "x"}, true)
	defer srv3.Close()
	if n := countTodoAttachments(srv3.URL, "t", "p", "T"); n != 0 {
		t.Fatalf("错误应返回 0，实际 %d", n)
	}
	// 空 id / 空 token 直接 0，不发请求。
	if n := countTodoAttachments(srv.URL, "", "p", "T"); n != 0 {
		t.Fatalf("空 token 应返回 0，实际 %d", n)
	}
	if n := countTodoAttachments(srv.URL, "t", "p", ""); n != 0 {
		t.Fatalf("空 id 应返回 0，实际 %d", n)
	}
}

var _ = os.Stdout

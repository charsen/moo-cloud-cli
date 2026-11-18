package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// batchMCP 模拟云端：create_todo 两阶段、get_todo 返回附件数，记录收到的标题。
type batchMCP struct {
	created []string
}

func (b *batchMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     any    `json:"id"`
		Method string `json:"method"`
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	var payload any
	switch req.Params.Name {
	case "create_todo":
		if _, ok := req.Params.Arguments["confirm_token"]; !ok {
			payload = map[string]any{"requires_confirmation": true, "project": map[string]any{"slug": "p", "name": "P"}, "confirm_token": "tok"}
		} else {
			title, _ := req.Params.Arguments["title"].(string)
			b.created = append(b.created, title)
			payload = map[string]any{"project": "p", "todo": map[string]any{"id": "T" + title, "title": title, "status": "open"}}
		}
	case "get_todo":
		payload = map[string]any{"todo": map[string]any{"id": "T", "attachments_count": float64(0)}}
	default:
		payload = map[string]any{"error": "unknown"}
	}
	text, _ := json.Marshal(payload)
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(text)}}, "isError": false,
	}})
}

func writeBatchFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTodoBatchDryRunDoesNotWrite(t *testing.T) {
	mcp := &batchMCP{}
	srv := httptest.NewServer(mcp)
	defer srv.Close()
	file := writeBatchFile(t, `{"project":"p","items":[{"title":"甲","priority":"high"},{"title":"乙"}]}`)

	opts, err := parseTodoOptions([]string{"--batch=" + file, "--dry-run", "--cloud=" + srv.URL, "--token=t"})
	if err != nil {
		t.Fatal(err)
	}
	if code := runTodoBatch(opts); code != 0 {
		t.Fatalf("dry-run 应成功，退出码 %d", code)
	}
	if len(mcp.created) != 0 {
		t.Fatalf("dry-run 不应写云端，实际创建 %v", mcp.created)
	}
}

func TestTodoBatchCreatesAllItems(t *testing.T) {
	mcp := &batchMCP{}
	srv := httptest.NewServer(mcp)
	defer srv.Close()
	// 第二条用顶层 project + 默认分类/优先级
	file := writeBatchFile(t, `{"project":"p","items":[{"title":"甲","priority":"high","category":"task"},{"title":"乙"}]}`)

	opts, err := parseTodoOptions([]string{"--batch=" + file, "--yes", "--cloud=" + srv.URL, "--token=t"})
	if err != nil {
		t.Fatal(err)
	}
	if code := runTodoBatch(opts); code != 0 {
		t.Fatalf("批量建单应成功，退出码 %d", code)
	}
	if len(mcp.created) != 2 || mcp.created[0] != "甲" || mcp.created[1] != "乙" {
		t.Fatalf("应按顺序创建两条，实际 %v", mcp.created)
	}
}

func TestTodoBatchRejectsBadItem(t *testing.T) {
	file := writeBatchFile(t, `{"project":"p","items":[{"title":""}]}`)
	opts, err := parseTodoOptions([]string{"--batch=" + file, "--dry-run", "--cloud=http://localhost:1", "--token=t"})
	if err != nil {
		t.Fatal(err)
	}
	if code := runTodoBatch(opts); code == 0 {
		t.Fatal("缺 title 应拒绝")
	}
}

func TestTodoBatchRejectsUnknownFields(t *testing.T) {
	file := writeBatchFile(t, `{"project":"p","items":[{"title":"甲"}],"extra":1}`)
	opts, err := parseTodoOptions([]string{"--batch=" + file, "--dry-run", "--cloud=http://localhost:1", "--token=t"})
	if err != nil {
		t.Fatal(err)
	}
	if code := runTodoBatch(opts); code == 0 {
		t.Fatal("含未知字段应拒绝，避免拼写错误静默生效")
	}
}

func TestTodoBatchRejectsTrailingContent(t *testing.T) {
	file := writeBatchFile(t, `{"project":"p","items":[{"title":"甲"}]} trailing`)
	opts, err := parseTodoOptions([]string{"--batch=" + file, "--dry-run", "--cloud=http://localhost:1", "--token=t"})
	if err != nil {
		t.Fatal(err)
	}
	if code := runTodoBatch(opts); code == 0 {
		t.Fatal("末尾有多余内容应拒绝，不能把残片当合法报告")
	}
}

func TestTodoBatchRejectsImagePathAtDryRun(t *testing.T) {
	file := writeBatchFile(t, `{"project":"p","items":[{"title":"甲","images":["/nonexistent/x.png"]}]}`)
	opts, err := parseTodoOptions([]string{"--batch=" + file, "--dry-run", "--cloud=http://localhost:1", "--token=t"})
	if err != nil {
		t.Fatal(err)
	}
	if code := runTodoBatch(opts); code == 0 {
		t.Fatal("dry-run 应提前拒绝不存在的图片路径")
	}
}

func TestTodoBatchRejectsMixedSingleFlags(t *testing.T) {
	file := writeBatchFile(t, `{"project":"p","items":[{"title":"甲"}]}`)
	if _, err := parseTodoOptions([]string{"--batch=" + file, "--title=ignored"}); err == nil {
		t.Fatal("--batch 与 --title 混用应拒绝")
	}
	if _, err := parseTodoOptions([]string{"--batch=" + file, "--image=/x.png"}); err == nil {
		t.Fatal("--batch 与 --image 混用应拒绝")
	}
}

func TestTodoBatchRequiresYesWhenNotTTY(t *testing.T) {
	mcp := &batchMCP{}
	srv := httptest.NewServer(mcp)
	defer srv.Close()
	file := writeBatchFile(t, `{"project":"p","items":[{"title":"甲"}]}`)
	opts, err := parseTodoOptions([]string{"--batch=" + file, "--cloud=" + srv.URL, "--token=t"})
	if err != nil {
		t.Fatal(err)
	}
	if code := runTodoBatch(opts); code == 0 {
		t.Fatal("非 TTY 且无 --yes 应拒绝")
	}
	if len(mcp.created) != 0 {
		t.Fatalf("拒绝时不应写云端，实际 %v", mcp.created)
	}
}

func TestParseTodoShowOptions(t *testing.T) {
	if _, err := parseTodoShowOptions([]string{"--project=p", "--id=short"}); err == nil {
		t.Fatal("非法 id 应拒绝")
	}
	opts, err := parseTodoShowOptions([]string{"--project=p", "--id=01m7p27bygzg2t92wvd7zr5vfq", "--json"})
	if err != nil || !opts.json {
		t.Fatalf("合法参数应通过：%v %+v", err, opts)
	}
}

func TestMentionsScreenshot(t *testing.T) {
	if !mentionsScreenshot("证据：01-x.png") {
		t.Fatal("应识别 .png")
	}
	if mentionsScreenshot("纯文字描述") {
		t.Fatal("纯文字不应误判")
	}
}

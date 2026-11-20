package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// todoDirectMCP 模拟云端 MCP：签发票据、两阶段 create_todo，并记录收到的参数。
type todoDirectMCP struct {
	mu           sync.Mutex
	ticketCalls  int
	ticketError  string
	recovery     bool
	loseResponse bool
	receipt      map[string]any
	createCalls  []map[string]any
}

func (s *todoDirectMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     json.RawMessage `json:"id"`
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	switch req.Params.Name {
	case "create_todo_image_tickets":
		s.mu.Lock()
		s.ticketCalls++
		ticketError := s.ticketError
		s.mu.Unlock()
		if ticketError != "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"isError": true, "_meta": map[string]string{"code": ticketError},
					"content": []map[string]string{{"type": "text", "text": "ticket error"}},
				},
			})
			return
		}
		files, _ := req.Params.Arguments["files"].([]any)
		tickets := make([]map[string]any, 0, len(files))
		for i := range files {
			id := fmt.Sprintf("U%d", i+1)
			tickets = append(tickets, map[string]any{
				"upload_id": id, "key": "todo-attachments/temp/" + id + "/x.png",
				"region": "z0", "token": "tok-" + id, "business": "todo_images",
			})
		}
		todoDirectToolResult(w, req.ID, map[string]any{"project": "p", "business": "todo_images", "tickets": tickets}, false)
	case "create_todo":
		s.mu.Lock()
		s.createCalls = append(s.createCalls, req.Params.Arguments)
		s.mu.Unlock()
		if req.Params.Arguments["confirm_token"] == nil {
			if s.receipt != nil && req.Params.Arguments["submission_key"] != nil {
				todoDirectToolResult(w, req.ID, map[string]any{"submission_receipt": s.receipt}, false)
				return
			}
			todoDirectToolResult(w, req.ID, map[string]any{
				"requires_confirmation": true, "confirm_token": "tok", "submission_recovery": s.recovery,
				"project": map[string]any{"slug": "p", "name": "P"},
			}, false)
			return
		}
		n := 0
		if u, ok := req.Params.Arguments["image_upload_ids"].([]any); ok {
			n = len(u)
		}
		if im, ok := req.Params.Arguments["images"].([]any); ok {
			n = len(im)
		}
		result := map[string]any{"images_attached": n, "images_added": n, "todo": map[string]any{"id": "T1"}}
		if s.loseResponse {
			s.receipt = result
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		todoDirectToolResult(w, req.ID, result, false)
	default:
		todoDirectToolResult(w, req.ID, map[string]any{"error": "unknown tool"}, true)
	}
}

func todoDirectToolResult(w http.ResponseWriter, id json.RawMessage, data map[string]any, isError bool) {
	text, _ := json.Marshal(data)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"result": map[string]any{"isError": isError, "content": []map[string]any{{"type": "text", "text": string(text)}}},
	})
}

// qiniuFake 模拟七牛上传入口：解析 multipart(token/key/file)，记录收到的内容。
type qiniuFake struct {
	mu        sync.Mutex
	requested int
	keys      []string
	tokens    []string
	sizes     []int
	fail      bool
	failAt    int // 第 n 次请求（1-based）返回 500；0 表示不按次数失败
	bodyError bool
}

func (q *qiniuFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q.mu.Lock()
	q.requested++
	n := q.requested
	fail := q.fail || (q.failAt > 0 && n == q.failAt)
	bodyError := q.bodyError
	q.mu.Unlock()
	if fail {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	size := 0
	if err == nil {
		b, _ := io.ReadAll(file)
		size = len(b)
		file.Close()
	}
	q.mu.Lock()
	key := r.FormValue("key")
	q.keys = append(q.keys, key)
	q.tokens = append(q.tokens, r.FormValue("token"))
	q.sizes = append(q.sizes, size)
	q.mu.Unlock()
	w.WriteHeader(http.StatusOK)
	if bodyError {
		_, _ = w.Write([]byte(`{"error":"bad token"}`))
		return
	}
	fmt.Fprintf(w, "{\"key\":\"%s\",\"hash\":\"fake\"}", key)
}

func todoDirectSetup(t *testing.T, mcp *todoDirectMCP, qiniu *qiniuFake) *httptest.Server {
	t.Helper()
	msrv := httptest.NewServer(mcp)
	qsrv := httptest.NewServer(qiniu)
	old := qiniuUploadHosts["z0"]
	qiniuUploadHosts["z0"] = qsrv.URL
	t.Cleanup(func() {
		msrv.Close()
		qsrv.Close()
		qiniuUploadHosts["z0"] = old
	})

	return msrv
}

func TestDefaultTodoImageModeIsDirect(t *testing.T) {
	t.Setenv("MOO_CLOUD_TODO_IMAGE_MODE", "")
	if got := todoImageMode(); got != todoImageModeDirect {
		t.Fatalf("默认图片模式应为 direct，实际 %s", got)
	}
}

func TestQiniuHTTP200ErrorIsRejected(t *testing.T) {
	t.Setenv("MOO_CLOUD_TODO_IMAGE_MODE", todoImageModeDirect)
	path, _ := todoTestImage(t)
	mcp := &todoDirectMCP{}
	qiniu := &qiniuFake{bodyError: true}
	msrv := todoDirectSetup(t, mcp, qiniu)

	opts, _ := parseTodoOptions([]string{"--project=p", "--title=七牛错误", "--image=" + path, "--yes"})
	opts.token, opts.cloud = "test-token", msrv.URL
	if _, err := addTodo(opts); err == nil {
		t.Fatal("七牛 HTTP 200 + error 必须报错，不能当成功")
	}
	if len(mcp.createCalls) != 1 || mcp.createCalls[0]["confirm_token"] != nil {
		t.Fatalf("七牛错误后只能有首次项目预览，实际 %v", mcp.createCalls)
	}
}

func TestAddTodoDirectUploadsAndSendsUploadIDs(t *testing.T) {
	t.Setenv("MOO_CLOUD_TODO_IMAGE_MODE", todoImageModeDirect)
	path, _ := todoTestImage(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mcp := &todoDirectMCP{}
	qiniu := &qiniuFake{}
	msrv := todoDirectSetup(t, mcp, qiniu)

	opts, err := parseTodoOptions([]string{"--project=p", "--title=直传截图", "--image=" + path, "--image=" + path, "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	opts.token, opts.cloud = "test-token", msrv.URL
	if _, err := addTodo(opts); err != nil {
		t.Fatalf("直传失败：%v", err)
	}

	if mcp.ticketCalls != 1 {
		t.Fatalf("票据只应申请一次，实际 %d", mcp.ticketCalls)
	}
	if len(mcp.createCalls) != 3 || mcp.createCalls[0]["images"] != nil || mcp.createCalls[0]["image_upload_ids"] != nil {
		t.Fatalf("应先无图片预览，再绑定上传票据并确认，实际 %v", mcp.createCalls)
	}
	for i, call := range mcp.createCalls[1:] {
		ids, _ := call["image_upload_ids"].([]any)
		if len(ids) != 2 {
			t.Fatalf("第 %d 次 create_todo 应携带 2 个 image_upload_ids，实际 %v", i+1, call["image_upload_ids"])
		}
		if call["images"] != nil {
			t.Fatalf("直传路径不应再发送 base64 images")
		}
	}
	if mcp.createCalls[2]["confirm_token"] != "tok" {
		t.Fatal("确认阶段未带 confirm_token")
	}
	if qiniu.requested != 2 {
		t.Fatalf("两张图应各直传一次，实际 %d", qiniu.requested)
	}
	if qiniu.sizes[0] != int(info.Size()) {
		t.Fatalf("直传文件大小不符：收到 %d，期望 %d", qiniu.sizes[0], info.Size())
	}
	if qiniu.keys[0] != "todo-attachments/temp/U1/x.png" || qiniu.tokens[0] != "tok-U1" {
		t.Fatalf("直传票据字段错误：key=%q token=%q", qiniu.keys[0], qiniu.tokens[0])
	}
}

// 多图直传时第 2 张失败：必须返回错误，并在错误信息里带出第 1 张已上传的 key，
// 便于人工排查（依赖七牛 TTL 回收）；不能静默丢弃也不能当作整体成功。
func TestDirectUploadReportsAlreadyUploadedKeysOnPartialFailure(t *testing.T) {
	t.Setenv("MOO_CLOUD_TODO_IMAGE_MODE", todoImageModeDirect)
	path, _ := todoTestImage(t)
	mcp := &todoDirectMCP{}
	qiniu := &qiniuFake{failAt: 2}
	msrv := todoDirectSetup(t, mcp, qiniu)

	files, err := loadTodoImageFiles([]string{path, path}, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = directUploadTodoImages(msrv.URL, "test-token", "p", files)
	if err == nil {
		t.Fatal("第 2 张失败必须返回错误")
	}
	// 第 1 张的 key 应出现在错误信息里（fake 回显 key，形如 todo-attachments/temp/U1/x.png）
	if !strings.Contains(err.Error(), "todo-attachments/temp/U1/x.png") {
		t.Fatalf("错误信息应报出已上传 key，实际：%v", err)
	}
	if len(mcp.createCalls) != 0 {
		t.Fatalf("部分失败后不应创建待办，实际 %d", len(mcp.createCalls))
	}
}

func TestAddTodoAutoFallsBackToLegacyWhenTicketsUnavailable(t *testing.T) {
	t.Setenv("MOO_CLOUD_TODO_IMAGE_MODE", todoImageModeAuto)
	path, _ := todoTestImage(t)
	mcp := &todoDirectMCP{ticketError: "todo_direct_upload_unavailable"}
	qiniu := &qiniuFake{}
	msrv := todoDirectSetup(t, mcp, qiniu)

	opts, _ := parseTodoOptions([]string{"--project=p", "--title=回退旧图", "--image=" + path, "--yes"})
	opts.token, opts.cloud = "test-token", msrv.URL
	if _, err := addTodo(opts); err != nil {
		t.Fatalf("回退后应成功：%v", err)
	}

	if mcp.ticketCalls != 1 {
		t.Fatalf("应先尝试一次票据，实际 %d", mcp.ticketCalls)
	}
	if len(mcp.createCalls) != 3 || mcp.createCalls[0]["images"] != nil {
		t.Fatalf("应先无图片预览，再确认图片，实际 %v", mcp.createCalls)
	}
	images, _ := mcp.createCalls[1]["images"].([]any)
	if len(images) != 1 || mcp.createCalls[1]["image_upload_ids"] != nil {
		t.Fatalf("回退路径应发送 base64 images，实际 %v", mcp.createCalls[1]["images"])
	}
	if qiniu.requested != 0 {
		t.Fatalf("回退后不应直传七牛，实际 %d", qiniu.requested)
	}
}

func TestAddTodoDirectUploadFailureIsReportedNotSwallowed(t *testing.T) {
	t.Setenv("MOO_CLOUD_TODO_IMAGE_MODE", todoImageModeDirect)
	path, _ := todoTestImage(t)
	mcp := &todoDirectMCP{}
	qiniu := &qiniuFake{fail: true}
	msrv := todoDirectSetup(t, mcp, qiniu)

	opts, _ := parseTodoOptions([]string{"--project=p", "--title=直传失败", "--image=" + path, "--yes"})
	opts.token, opts.cloud = "test-token", msrv.URL
	if _, err := addTodo(opts); err == nil {
		t.Fatal("七牛直传失败必须报错，不能当作成功")
	}
	if len(mcp.createCalls) != 1 || mcp.createCalls[0]["confirm_token"] != nil {
		t.Fatalf("直传失败后只能有首次项目预览，实际 %v", mcp.createCalls)
	}
}

// 两阶段桥接：项目预览不上传，确认后只上传一次。
func TestTodoImageBridgeDirectReusesUploadAcrossPhases(t *testing.T) {
	t.Setenv("MOO_CLOUD_TODO_IMAGE_MODE", todoImageModeDirect)
	path, _ := todoTestImage(t)
	mcp := &todoDirectMCP{}
	qiniu := &qiniuFake{}
	msrv := todoDirectSetup(t, mcp, qiniu)
	endpoint := msrv.URL + "/api/v1/mcp"

	previewArgs := map[string]any{"project": "p", "title": "桥直传", "image_paths": []string{path}}
	if ok, _ := todoImageBridgeCall(t, endpoint, todoImageRequest(previewArgs)); !ok {
		t.Fatal("预览阶段失败")
	}
	if mcp.ticketCalls != 0 || qiniu.requested != 0 {
		t.Fatal("用户确认前不得申请票据或传输文件")
	}
	confirmArgs := map[string]any{"project": "p", "title": "桥直传", "image_paths": []string{path}, "confirm_token": "invalid"}
	if ok, _ := todoImageBridgeCall(t, endpoint, todoImageRequest(confirmArgs)); ok || mcp.ticketCalls != 0 {
		t.Fatal("无效确认不得触发上传")
	}
	confirmArgs["confirm_token"] = "tok"
	if ok, _ := todoImageBridgeCall(t, endpoint, todoImageRequest(confirmArgs)); !ok {
		t.Fatal("确认阶段失败")
	}

	if mcp.ticketCalls != 1 || qiniu.requested != 1 {
		t.Fatalf("应只上传一次：ticket=%d qiniu=%d", mcp.ticketCalls, qiniu.requested)
	}
	if len(mcp.createCalls) != 3 || mcp.createCalls[0]["images"] != nil || mcp.createCalls[0]["image_upload_ids"] != nil {
		t.Fatalf("应先无图片预览，再绑定上传票据并确认，实际 %v", mcp.createCalls)
	}
	for i, call := range mcp.createCalls[1:] {
		ids, _ := call["image_upload_ids"].([]any)
		if len(ids) != 1 || ids[0] != "U1" {
			t.Fatalf("第 %d 次应复用同一 upload_id U1，实际 %v", i+1, call["image_upload_ids"])
		}
		if call["images"] != nil {
			t.Fatalf("直传桥接不应发送 base64 images")
		}
	}
}

func TestAddTodoImageConfirmationAndRecovery(t *testing.T) {
	t.Setenv("MOO_CLOUD_TODO_IMAGE_MODE", todoImageModeDirect)
	path, _ := todoTestImage(t)
	mcp := &todoDirectMCP{recovery: true, loseResponse: true}
	qiniu := &qiniuFake{}
	server := todoDirectSetup(t, mcp, qiniu)
	opts, _ := parseTodoOptions([]string{"--project=p", "--title=回执", "--image=" + path})
	opts.token, opts.cloud = "test-token", server.URL

	if _, err := addTodo(opts); err == nil || mcp.ticketCalls != 0 || qiniu.requested != 0 {
		t.Fatalf("未确认前不得传图：%v", err)
	}
	opts.yes = true
	for range 2 {
		result, err := addTodo(opts)
		if err != nil || result["todo"].(map[string]any)["id"] != "T1" {
			t.Fatalf("确认响应丢失后应恢复原回执：%v %v", result, err)
		}
	}
	if mcp.ticketCalls != 1 || qiniu.requested != 1 {
		t.Fatalf("重试不得上传第二批：票据 %d，直传 %d", mcp.ticketCalls, qiniu.requested)
	}
}

func TestAddTodoAutoDoesNotFallbackOnOtherErrors(t *testing.T) {
	t.Setenv("MOO_CLOUD_TODO_IMAGE_MODE", todoImageModeAuto)
	path, _ := todoTestImage(t)
	for _, code := range []string{"forbidden", "invalid_receipt"} {
		mcp := &todoDirectMCP{ticketError: code}
		qiniu := &qiniuFake{}
		server := todoDirectSetup(t, mcp, qiniu)
		opts, _ := parseTodoOptions([]string{"--project=p", "--title=不回退", "--image=" + path, "--yes"})
		opts.token, opts.cloud = "test-token", server.URL
		if _, err := addTodo(opts); err == nil || len(mcp.createCalls) != 1 || qiniu.requested != 0 {
			t.Fatalf("权限或回执错误不得改传原图：%v", err)
		}
	}
}

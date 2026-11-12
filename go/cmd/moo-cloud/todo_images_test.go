package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func todoTestImage(t *testing.T) (string, string) {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "截图.png")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path, "data:image/png;base64," + base64.StdEncoding.EncodeToString(data.Bytes())
}

func TestTodoImageFilesValidation(t *testing.T) {
	path, expected := todoTestImage(t)
	images, err := loadTodoImages([]string{path, path}, true)
	if err != nil || len(images) != 2 || images[0] != expected || images[1] != expected {
		t.Fatalf("文件读取错误：%v", err)
	}
	bad := filepath.Join(t.TempDir(), "fake.png")
	if err := os.WriteFile(bad, []byte("<svg/>"), 0600); err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(t.TempDir(), "large.png")
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(maxTodoImageBytes + 1)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	for name, paths := range map[string][]string{
		"missing": {path + "missing"}, "empty": {""}, "directory": {filepath.Dir(path)},
		"relative": {"a.png"}, "fake": {bad}, "oversize": {large},
		"too-many": {path, path, path, path, path, path},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadTodoImages(paths, true); err == nil {
				t.Fatal("应拒绝无效图片")
			}
		})
	}
}

func todoImageBridgeCall(t *testing.T, endpoint string, request map[string]any) (bool, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := bufio.NewWriter(&output)
	ok, _ := handleMcpLine(endpoint, "test-token", string(raw), writer, newCLIUpdater())
	writer.Flush()
	if output.Len() == 0 {
		return ok, nil
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return ok, response
}

func todoImageRequest(args map[string]any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": json.RawMessage("9007199254740993"), "method": "tools/call",
		"params": map[string]any{"name": "create_todo", "arguments": args}}
}

func todoImageReply(w http.ResponseWriter, id any, data map[string]any, isError bool) {
	text, _ := json.Marshal(data)
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
		"isError": isError, "content": []map[string]any{{"type": "text", "text": string(text)}},
	}})
}

func TestTodoImageBridgeUploadsAndPreservesConfirmation(t *testing.T) {
	path, expected := todoTestImage(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			ID     json.RawMessage `json:"id"`
			Params struct {
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if string(req.ID) != "9007199254740993" {
			t.Error("大整数请求 id 被改变")
		}
		args := req.Params.Arguments
		images, _ := args["images"].([]any)
		if len(images) != 1 || images[0] != expected || args["image_paths"] != nil {
			t.Error("图片字节未上传或本地路径泄露到远端")
		}
		if args["confirm_token"] == nil {
			todoImageReply(w, req.ID, map[string]any{"requires_confirmation": true, "confirm_token": "tok"}, false)
		} else {
			if args["confirm_token"] != "tok" {
				t.Error("确认令牌丢失")
			}
			todoImageReply(w, req.ID, map[string]any{"images_attached": 1, "images_added": 1, "todo": map[string]any{"id": "T1"}}, false)
		}
	}))
	defer srv.Close()
	args := map[string]any{"project": "p", "title": "截图问题", "image_paths": []string{path}}
	for _, confirmed := range []bool{false, true} {
		if confirmed {
			args["confirm_token"] = "tok"
		}
		ok, _ := todoImageBridgeCall(t, srv.URL, todoImageRequest(args))
		if !ok {
			t.Fatalf("图片请求失败，confirmed=%v", confirmed)
		}
	}
	if calls != 2 {
		t.Fatalf("应只调用预览、确认两次，实际 %d", calls)
	}
}

func TestTodoImageBridgeRejectsInvalidCallsBeforeForwarding(t *testing.T) {
	path, _ := todoTestImage(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	for _, args := range []map[string]any{
		{"image_paths": "bad"}, {"image_paths": nil}, {"image_paths": []string{path + "missing"}},
		{"image_paths": []string{path}, "images": []string{}}, {"image_paths": []string{"relative.png"}},
	} {
		ok, response := todoImageBridgeCall(t, srv.URL, todoImageRequest(args))
		if ok || response["result"].(map[string]any)["isError"] != true {
			t.Fatal("应返回工具错误")
		}
	}
	for _, field := range []string{"id", "jsonrpc"} {
		req := todoImageRequest(map[string]any{"image_paths": []string{path}})
		delete(req, field)
		ok, _ := todoImageBridgeCall(t, srv.URL, req)
		if ok {
			t.Fatal("非法协议或通知不应读图")
		}
	}
	if calls != 0 {
		t.Fatal("无效请求不应访问 cloud")
	}
}

func TestTodoImageBridgeRequiresUploadReceipt(t *testing.T) {
	_, imageURL := todoTestImage(t)
	for name, data := range map[string]map[string]any{
		"missing": {"todo": map[string]any{"id": "T1"}},
		"partial": {"images_attached": 0, "todo": map[string]any{"id": "T1"}},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { todoImageReply(w, 1, data, false) }))
			defer srv.Close()
			ok, response := todoImageBridgeCall(t, srv.URL, todoImageRequest(map[string]any{"images": []string{imageURL}}))
			raw, _ := json.Marshal(response)
			if ok || !strings.Contains(string(raw), "T1") || response["result"].(map[string]any)["isError"] != true {
				t.Fatal("不能把仅创建文字的响应当作图片已上传")
			}
		})
	}
}

func TestTodoImageSchemaOnEveryToolPage(t *testing.T) {
	for _, cursor := range []string{"", "next-page"} {
		t.Run(cursor, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"tools": []any{
					map[string]any{"name": "create_todo", "description": "创建", "inputSchema": map[string]any{"properties": map[string]any{"images": map[string]any{"type": "array"}}}},
				}}})
			}))
			defer srv.Close()
			ok, result := todoImageBridgeCall(t, srv.URL, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]any{"cursor": cursor}})
			if !ok {
				t.Fatal("tools/list 失败")
			}
			tools := result["result"].(map[string]any)["tools"].([]any)
			tool := tools[0].(map[string]any)
			properties := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
			if properties["image_paths"] == nil {
				t.Fatal("创建工具缺少本地图片参数")
			}
			if cursor != "" && len(tools) != 1 {
				t.Fatal("后续页不应重复追加更新工具")
			}
		})
	}
}

func TestAddTodoWithImages(t *testing.T) {
	path, expected := todoTestImage(t)
	for _, receipt := range []bool{true, false} {
		t.Run(map[bool]string{true: "saved", false: "missing-receipt"}[receipt], func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var req struct {
					Params struct {
						Name      string         `json:"name"`
						Arguments map[string]any `json:"arguments"`
					} `json:"params"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				args := req.Params.Arguments
				images, _ := args["images"].([]any)
				if req.Params.Name != "create_todo" || len(images) != 2 || images[0] != expected || images[1] != expected {
					t.Error("图片未随两阶段请求传入")
				}
				data := map[string]any{"todo": map[string]any{"id": "T1"}}
				if args["confirm_token"] == nil {
					data = map[string]any{"requires_confirmation": true, "confirm_token": "tok", "project": map[string]any{"slug": "p", "name": "P"}}
				} else if receipt {
					data["images_attached"] = 2
					data["images_added"] = 1
				}
				todoImageReply(w, 1, data, false)
			}))
			defer srv.Close()
			opts, err := parseTodoOptions([]string{"--project=p", "--title=截图", "--image=" + path, "--image=" + path, "--yes"})
			if err != nil {
				t.Fatal(err)
			}
			opts.token, opts.cloud = "test-token", srv.URL
			_, err = addTodo(opts)
			if (err == nil) != receipt {
				t.Fatalf("回执验证错误：%v", err)
			}
			if calls != 2 {
				t.Fatalf("应仅调用两阶段创建，实际 %d", calls)
			}
		})
	}
}

func TestTodoImageBridgePreservesErrorsAndRejectsMalformedReceipt(t *testing.T) {
	path, _ := todoTestImage(t)
	for _, mode := range []string{"tool-error", "rpc-error", "malformed-result"} {
		t.Run(mode, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "tool-error":
					todoImageReply(w, 1, map[string]any{"error": "storage failed"}, true)
				case "rpc-error":
					_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"storage failed"}}`))
				case "malformed-result":
					_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"invalid"}`))
				}
			}))
			defer srv.Close()
			ok, response := todoImageBridgeCall(t, srv.URL, todoImageRequest(map[string]any{"image_paths": []string{path}}))
			raw, _ := json.Marshal(response)
			if ok {
				t.Fatal("图片错误不能返回成功")
			}
			if mode != "malformed-result" && !strings.Contains(string(raw), "storage failed") {
				t.Fatal("不应覆盖云端的具体错误")
			}
		})
	}
}

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

func TestMcpBridgeNormalizesHttpAndProtocolErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"auth", 401, `{"ok":false,"error":"token expired"}`},
		{"forbidden", 403, `{"ok":false,"error":"forbidden"}`},
		{"limited", 429, `{"message":"too many requests"}`},
		{"invalid rpc", 200, `{"ok":false,"error":"bad"}`},
		{"rpc error", 200, `{"jsonrpc":"2.0","id":7,"error":{"code":-32602,"message":"bad params"}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(c.status); _, _ = w.Write([]byte(c.body)) }))
			defer server.Close()
			var out bytes.Buffer
			writer := bufio.NewWriter(&out)
			ok, _ := handleMcpLine(server.URL, "fake", `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`, writer)
			_ = writer.Flush()
			var response struct {
				Version string `json:"jsonrpc"`
				ID      int    `json:"id"`
				Error   struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(out.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if ok || response.Version != "2.0" || response.ID != 7 || response.Error.Code == 0 || response.Error.Message == "" {
				t.Fatalf("ok=%v response=%s", ok, out.String())
			}
		})
	}
}

func appRepo(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "app")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if code, _ := gitCmd(dir, "init"); code != 0 {
		t.Fatal("git init failed")
	}
	if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte("com.example.app"), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, ".moo-cloud.json")
	if err := os.WriteFile(marker, []byte(`{"role":"app","project":"p","product":"product","verify":[{"file":"app.txt","contains":"com.example.app"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	return marker
}

func queueServer(t *testing.T, failTypes map[string]bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		if req.Method == "tools/list" {
			result = map[string]any{"tools": []any{map[string]any{"name": "list_issues"}}}
		} else {
			var data any
			switch req.Params.Name {
			case "list_projects":
				data = map[string]any{"projects": []any{map[string]any{"slug": "p"}}}
			case "list_apps":
				data = map[string]any{"apps": []any{map[string]any{"slug": "product"}}}
			default:
				data = map[string]any{"issues": []any{}, "count": 0}
			}
			typ, _ := req.Params.Arguments["type"].(string)
			raw, _ := json.Marshal(data)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(raw)}}}
			if failTypes[typ] {
				result = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "temporary failure"}}}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	}))
}

func TestAppMarkerValidatesCloudProductAndFetchesStandalone(t *testing.T) {
	root := t.TempDir()
	marker := appRepo(t, root)
	server := queueServer(t, nil)
	defer server.Close()
	report := inspectRepo(marker, pfOptions{token: "fake", cloud: server.URL})
	if report.HardFailures != 0 || len(report.Deployments) != 1 || !report.Deployments[0].OK {
		t.Fatalf("%+v", report)
	}
	out := t.TempDir()
	if code := runPreflight([]string{"--root=" + root, "--out=" + out, "--fetch", "--token=fake", "--cloud=" + server.URL, "--types=runtime"}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	var queue struct {
		Projects map[string]any `json:"projects"`
	}
	_ = json.Unmarshal(mustRead(t, filepath.Join(out, "_queue.json")), &queue)
	if _, ok := queue.Projects["p"]; !ok {
		t.Fatalf("missing App project: %+v", queue)
	}
	raw := strings.Replace(string(mustRead(t, marker)), `"product":"product"`, `"product":"wrong"`, 1)
	_ = os.WriteFile(marker, []byte(raw), 0600)
	report = inspectRepo(marker, pfOptions{token: "fake", cloud: server.URL})
	if report.HardFailures == 0 || report.Deployments[0].OK {
		t.Fatal("wrong product accepted")
	}
}

func TestFetchQueueDistinguishesTotalAndPartialFailure(t *testing.T) {
	for _, all := range []bool{true, false} {
		failures := map[string]bool{"runtime": true}
		if all {
			failures["slow_query"] = true
		}
		server := queueServer(t, failures)
		queue, _, err := fetchQueue(server.URL, pfOptions{token: "fake", types: "runtime,slow_query"}, []string{"p"})
		server.Close()
		if (err != nil) != all {
			t.Fatalf("all=%v err=%v", all, err)
		}
		if queue["p"]["runtime"].(map[string]any)["error"] == nil {
			t.Fatal("missing partial result")
		}
	}
}

func TestReportAndConfigWriteFailureReturnsNonzero(t *testing.T) {
	root := t.TempDir()
	appRepo(t, root)
	out := t.TempDir()
	_ = os.MkdirAll(filepath.Join(out, "_preflight.json"), 0700)
	if code := runPreflight([]string{"--offline", "--root=" + root, "--out=" + out}); code == 0 {
		t.Fatal("unwritable report returned success")
	}
	for _, target := range []string{"codex", "workbuddy"} {
		home := t.TempDir()
		name := ".codex"
		file := "config.toml"
		if target == "workbuddy" {
			name = ".workbuddy"
			file = "mcp.json"
		}
		_ = os.MkdirAll(filepath.Join(home, name, file), 0700)
		if code := runAgentConfig([]string{"--target=" + target, "--write", "--home=" + home}); code == 0 {
			t.Fatal("unwritable config returned success")
		}
	}
}

func TestConfigRejectsMalformedServerMapWithoutOverwriting(t *testing.T) {
	for _, input := range []string{`null`, `{"mcpServers":null}`, `{"mcpServers":[]}`} {
		home := t.TempDir()
		file := filepath.Join(home, ".workbuddy", "mcp.json")
		_ = os.MkdirAll(filepath.Dir(file), 0700)
		_ = os.WriteFile(file, []byte(input), 0600)
		if emitWorkbuddy(home, map[string]any{"command": "moo-cloud"}, true, false) == 0 {
			t.Fatal("invalid config accepted")
		}
		if string(mustRead(t, file)) != input {
			t.Fatal("invalid config was overwritten")
		}
	}
}

func TestPreflightAllReadFailuresReturnNonzeroAndKeepDiagnostics(t *testing.T) {
	root := t.TempDir()
	appRepo(t, root)
	server := queueServer(t, map[string]bool{"runtime": true})
	defer server.Close()
	out := t.TempDir()
	code := runPreflight([]string{"--root=" + root, "--out=" + out, "--fetch", "--token=fake", "--cloud=" + server.URL, "--types=runtime"})
	if code == 0 {
		t.Fatal("total read failure returned success")
	}
	var queue struct {
		Error    string         `json:"error"`
		Projects map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(mustRead(t, filepath.Join(out, "_queue.json")), &queue); err != nil {
		t.Fatal(err)
	}
	if queue.Error == "" || len(queue.Projects) != 1 {
		t.Fatalf("missing failure diagnostics: %+v", queue)
	}
}

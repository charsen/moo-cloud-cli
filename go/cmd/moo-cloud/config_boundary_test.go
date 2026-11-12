package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentConfigRejectsIncompleteOptionsBeforeWriting(t *testing.T) {
	for _, arg := range []string{"write", "--wriet", "--target", "--target=", "--home", "--home="} {
		t.Run(arg, func(t *testing.T) {
			home := t.TempDir()
			// 即使旧解析器把空 home 当作未传，也只会写到这个隔离目录。
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			code := runAgentConfig([]string{"--target=workbuddy", "--home=" + home, "--write", arg})
			if code == 0 {
				t.Fatal("incomplete option was silently accepted")
			}
			files, err := os.ReadDir(home)
			if err != nil || len(files) != 0 {
				t.Fatal("invalid options created configuration files", err)
			}
		})
	}
}

func TestPreflightRejectsIncompleteOptions(t *testing.T) {
	t.Setenv("MOO_CLOUD_MCP_TOKEN", "fixture")
	for _, arg := range []string{"root=/tmp", "--rot=/tmp", "--root", "--root=", "--cloud", "--cloud=", "--token", "--token="} {
		if _, err := parsePfOptions([]string{arg}); err == nil {
			t.Fatal("incomplete option was silently accepted")
		}
	}
}

func TestMcpServerRejectsIncompleteOptions(t *testing.T) {
	// EOF 防止旧解析器静默忽略参数后进入 stdio 等待；不输入业务请求。
	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() { os.Stdin = original; _ = stdin.Close() })
	for _, arg := range []string{"token=fixture", "--clodu=http://example.test", "--cloud", "--cloud=", "--token", "--token=", "--log", "--log="} {
		if code := runMcpServer([]string{"--token=fixture", arg}); code == 0 {
			t.Fatal("incomplete MCP option was silently accepted")
		}
	}
}

func TestMcpServerValidOptionsKeepStdioAndLogBehavior(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/mcp" || r.Header.Get("Authorization") != "Bearer fixture-mcp-token" {
			t.Error("configured endpoint or token was not used")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 77, "result": map[string]any{"tools": []any{}}})
	}))
	defer server.Close()
	dir := t.TempDir()
	stdin, err := os.CreateTemp(dir, "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if _, err := stdin.WriteString("{\"jsonrpc\":\"2.0\",\"id\":77,\"method\":\"tools/list\",\"params\":{}}\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	stdout, err := os.CreateTemp(dir, "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	originalIn, originalOut := os.Stdin, os.Stdout
	t.Cleanup(func() { os.Stdin, os.Stdout = originalIn, originalOut })
	os.Stdin, os.Stdout = stdin, stdout
	log := filepath.Join(dir, "mcp.log")
	code := runMcpServer([]string{"--token=fixture-mcp-token", "--cloud=" + server.URL, "--log=" + log})
	os.Stdin, os.Stdout = originalIn, originalOut
	if code != 0 {
		t.Fatal("valid MCP startup failed")
	}
	var response struct {
		ID     int `json:"id"`
		Result struct {
			Tools []struct{ Name string } `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(mustRead(t, stdout.Name()), &response); err != nil || response.ID != 77 || len(response.Result.Tools) != 2 {
		t.Fatal("valid stdio response lost its ID or local update tools", err)
	}
	raw := string(mustRead(t, log))
	if !strings.Contains(raw, "tools/list") || strings.Contains(raw, "fixture-mcp-token") {
		t.Fatal("bridge log lost request metadata or leaked a fixture credential")
	}
}

func TestClientConfigWritesPreserveSymlinksAndBackupTarget(t *testing.T) {
	for _, target := range []string{"codex", "workbuddy"} {
		t.Run(target, func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(home, ".codex", "config.toml")
			content := "model = \"keep-model\"\n[mcp_servers.other]\ncommand = \"keep-other\"\n"
			if target == "workbuddy" {
				config = filepath.Join(home, ".workbuddy", "mcp.json")
				content = `{"mcpServers":{"other":{"command":"keep-other"}},"extra":"keep-extra"}`
			}
			if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
				t.Fatal(err)
			}
			actual := filepath.Join(home, "managed-config")
			if err := os.WriteFile(actual, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join("..", "managed-config"), config); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if code := runAgentConfig([]string{"--target=" + target, "--home=" + home, "--write"}); code != 0 {
				t.Fatal("configuration write failed")
			}
			info, err := os.Lstat(config)
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatal("configuration symlink was replaced", err)
			}
			raw := string(mustRead(t, actual))
			if !strings.Contains(raw, "keep-other") || !strings.Contains(raw, "mcp-server") {
				t.Fatal("managed target was not updated or unrelated server was lost")
			}
			if target == "codex" && !strings.Contains(raw, "keep-model") || target == "workbuddy" && !strings.Contains(raw, "keep-extra") {
				t.Fatal("unrelated configuration was lost")
			}
			backups, err := filepath.Glob(actual + ".bak-*")
			if err != nil || len(backups) != 1 || string(mustRead(t, backups[0])) != content {
				t.Fatal("original managed target was not backed up", err)
			}
		})
	}
}

func TestClientConfigRejectsBrokenSymlinks(t *testing.T) {
	for _, target := range []string{"codex", "workbuddy"} {
		t.Run(target, func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(home, ".codex", "config.toml")
			if target == "workbuddy" {
				config = filepath.Join(home, ".workbuddy", "mcp.json")
			}
			if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("missing-config", config); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if code := runAgentConfig([]string{"--target=" + target, "--home=" + home, "--write"}); code == 0 {
				t.Fatal("broken symlink was overwritten")
			}
			info, err := os.Lstat(config)
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatal("broken symlink should remain intact", err)
			}
		})
	}
}

func TestFailedCloudDoesNotBlockValidEnvironmentFetch(t *testing.T) {
	t.Setenv("MOO_CLOUD_URL", "")
	queue := queueServer(t, nil)
	defer queue.Close()
	valid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/summary" {
			_ = json.NewEncoder(w).Encode(map[string]any{"project": map[string]any{"slug": "p"}})
			return
		}
		queue.Config.Handler.ServeHTTP(w, r)
	}))
	defer valid.Close()
	invalid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer invalid.Close()
	workspace := t.TempDir()
	marker := backendRepo(t, workspace, ".")
	for file, cloud := range map[string]string{".env": valid.URL, ".env.invalid": invalid.URL} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(marker), file), []byte("MOO_MONITOR_CLOUD_TOKEN=fixture\nMOO_MONITOR_CLOUD_URL="+cloud+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(marker, []byte(`{"role":"backend","root":".","deployments":[{"project":"p","env_file":".env"},{"project":"unavailable","env_file":".env.invalid"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if code := runPreflight([]string{"--root=" + workspace, "--out=" + out, "--fetch", "--types=runtime", "--token=fixture"}); code == 0 {
		t.Fatal("failed environment must remain a hard failure")
	}
	var result struct {
		Error    string         `json:"error"`
		Projects map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(mustRead(t, filepath.Join(out, "_queue.json")), &result); err != nil || result.Error != "" || len(result.Projects) != 1 || result.Projects["p"] == nil {
		t.Fatal("failed cloud prevented fetching the verified environment", err)
	}
}

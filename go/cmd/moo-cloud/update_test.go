package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type updateFixture struct {
	u              *cliUpdater
	path           string
	requests       []string
	version        string
	payload        []byte
	checksum       []byte
	githubStatus   int
	giteeOmitsSize bool
	badAsset       bool
	badSize        bool
}

// 真实临时目录覆盖下载、暂存、备份与替换；仅隔离发布 HTTP 和二进制执行。
func newUpdateFixture(t *testing.T) *updateFixture {
	t.Helper()
	dir := t.TempDir()
	f := &updateFixture{path: filepath.Join(dir, "moo-cloud"), version: "0.1.2", payload: []byte("0.1.2"), githubStatus: 200}
	if err := os.WriteFile(f.path, []byte("0.1.1"), 0755); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(f.payload)
	f.checksum = []byte(hex.EncodeToString(digest[:]) + "  moo-cloud-darwin-arm64\n")
	f.u = newCLIUpdater()
	f.u.running, f.u.goos, f.u.goarch = "0.1.1", "darwin", "arm64"
	f.u.cachePath = filepath.Join(dir, "cache", "cli-update.json")
	f.u.executable = func() (string, error) { return f.path, nil }
	f.u.binaryVersion = func(ctx context.Context, path string) (string, error) {
		data, err := os.ReadFile(path)
		return string(data), err
	}
	f.u.client = &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		f.requests = append(f.requests, r.URL.String())
		if r.Header.Get("Authorization") != "" {
			t.Error("公开发布请求不得携带 MCP token")
		}
		status := 200
		var data []byte
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			source := "github"
			if r.URL.Host == "gitee.com" {
				source = "gitee"
			} else {
				status = f.githubStatus
			}
			base := "https://" + source + ".com/charsen/moo-cloud-cli/releases/download/" + f.version + "/"
			r := cliRelease{Tag: f.version, Assets: []cliAsset{
				{Name: "moo-cloud-darwin-arm64", URL: base + "moo-cloud-darwin-arm64", Size: int64(len(f.payload))},
				{Name: "SHA256SUMS", URL: base + "SHA256SUMS", Size: int64(len(f.checksum))},
			}}
			if f.badAsset {
				r.Assets[0].URL = "https://other.example/untrusted"
			}
			if f.badSize {
				r.Assets[0].Size = maxCLIBytes + 1
			}
			data, _ = json.Marshal(r)
			if source == "gitee" && f.giteeOmitsSize {
				// 真实 Gitee Release API 不提供附件 size；不能让 GitHub 同形夹具掩盖差异。
				var raw map[string]any
				_ = json.Unmarshal(data, &raw)
				for _, item := range raw["assets"].([]any) {
					delete(item.(map[string]any), "size")
				}
				data, _ = json.Marshal(raw)
			}
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			data = f.checksum
		case strings.HasSuffix(r.URL.Path, "/moo-cloud-darwin-arm64"):
			data = f.payload
		default:
			t.Errorf("意外网络请求 %s", r.URL)
			status = 404
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}, nil
	})}
	return f
}

func TestCLISemverStableComparison(t *testing.T) {
	for _, s := range []string{"dev", "0.1.0-1-gabc", "0.2.0-rc1", "01.2.3", "1.2", "1.2.3.4", "1.2.+3", "1.2.99999999999999999"} {
		if _, ok := cliSemver(s); ok {
			t.Fatalf("非法稳定版本被接受：%s", s)
		}
	}
	a, ok := cliSemver("v0.1.10")
	b, _ := cliSemver("0.1.9")
	if !ok || !cliNewer(a, b) || cliNewer(b, a) || cliNewer(a, a) {
		t.Fatal("应按数字比较版本，并支持既有 v 标签")
	}
}

func TestCLICheckCacheFallbackAndInstalledVersion(t *testing.T) {
	f := newUpdateFixture(t)
	f.githubStatus = 429
	first, err := f.u.check(context.Background(), true)
	if err != nil || !first.UpdateAvailable || !strings.Contains(first.ReleaseURL, "gitee.com") {
		t.Fatalf("GitHub 失败时应使用 Gitee: %+v %v", first, err)
	}
	requests := len(f.requests)
	if _, err := f.u.check(context.Background(), true); err != nil || len(f.requests) != requests {
		t.Fatal("自动检查没有复用日缓存", err)
	}
	if _, err := f.u.check(context.Background(), false); err != nil || len(f.requests) != requests+2 {
		t.Fatal("手动检查应绕过缓存", err)
	}
	if err := os.WriteFile(f.path, []byte("0.1.2"), 0755); err != nil {
		t.Fatal(err)
	}
	check, err := f.u.check(context.Background(), true)
	if err != nil || check.UpdateAvailable || !check.RestartRequired || check.InstalledVersion != "0.1.2" || check.RunningVersion != "0.1.1" {
		t.Fatalf("必须区分运行进程与磁盘版本：%+v %v", check, err)
	}
}

func TestCLIUpdateRequiresConfirmationBeforeIO(t *testing.T) {
	for _, args := range []string{`{}`, `{"target_version":"0.1.2"}`, `{"target_version":"0.1.2","confirm":false}`, `{"target_version":"dev","confirm":true}`, `{"target_version":"0.1.2","confirm":"true"}`, `{"target_version":"0.1.2","confirm":true,"url":"https://other.example"}`, `null`} {
		f := newUpdateFixture(t)
		if _, err := f.u.callTool(context.Background(), "update_cli", json.RawMessage(args)); err == nil {
			t.Fatal("不应接受更新参数", args)
		}
		if len(f.requests) != 0 || string(mustRead(t, f.path)) != "0.1.1" {
			t.Fatal("未确认不得联网更新或修改原文件")
		}
	}
}

func TestCLIUpdateInstallsVerifiedFileAndKeepsBackup(t *testing.T) {
	f := newUpdateFixture(t)
	result, err := f.u.update(context.Background(), "0.1.2", true)
	if err != nil {
		t.Fatal(err)
	}
	if result["updated"] != true || result["restart_required"] != true || string(mustRead(t, f.path)) != "0.1.2" {
		t.Fatalf("更新结果错误：%+v", result)
	}
	backup := result["backup_path"].(string)
	if string(mustRead(t, backup)) != "0.1.1" {
		t.Fatal("备份内容错误")
	}
	unlock, err := acquireCLIUpdateLock(f.path + ".update.lock")
	if err != nil {
		t.Fatal("更新锁未释放", err)
	}
	unlock()
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(f.path), ".moo-cloud-update-*"))
	if len(leftovers) != 0 {
		t.Fatal("暂存文件未清理", leftovers)
	}
	requests := len(f.requests)
	result, err = f.u.update(context.Background(), "0.1.2", true)
	if err != nil || result["updated"] != false || len(f.requests) != requests {
		t.Fatalf("共享文件已更新时应返回重连提示，不重复下载安装：%+v %v", result, err)
	}
}

func TestCLIUpdateFailureKeepsOriginal(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*updateFixture)
	}{
		{"checksum", func(f *updateFixture) { f.checksum = []byte(strings.Repeat("0", 64) + "  moo-cloud-darwin-arm64\n") }},
		{"duplicate checksum", func(f *updateFixture) { f.checksum = append(f.checksum, f.checksum...) }},
		{"version changed after consent", func(f *updateFixture) { f.version = "0.1.3" }},
		{"untrusted URL", func(f *updateFixture) { f.badAsset = true }},
		{"oversized asset", func(f *updateFixture) { f.badSize = true }},
		{"wrong binary version", func(f *updateFixture) {
			f.payload = []byte("0.1.9")
			digest := sha256.Sum256(f.payload)
			f.checksum = []byte(hex.EncodeToString(digest[:]) + "  moo-cloud-darwin-arm64\n")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newUpdateFixture(t)
			c.setup(f)
			if _, err := f.u.update(context.Background(), "0.1.2", true); err == nil {
				t.Fatal("应拒绝不安全更新")
			}
			if string(mustRead(t, f.path)) != "0.1.1" {
				t.Fatal("失败覆盖了原文件")
			}
		})
	}
	f := newUpdateFixture(t)
	for _, installed := range []string{"dev", "0.1.3"} {
		_ = os.WriteFile(f.path, []byte(installed), 0755)
		if _, err := f.u.update(context.Background(), "0.1.2", true); err == nil || len(f.requests) != 0 {
			t.Fatal("不得覆盖开发构建或降级")
		}
	}
}

func TestCLIMcpLocalToolsAndReminder(t *testing.T) {
	f := newUpdateFixture(t)
	cloudCalls := 0
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloudCalls++
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		result := map[string]any{"tools": []any{map[string]any{"name": "list_projects"}}, "nextCursor": "next"}
		if req.Method == "initialize" {
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "instructions": "cloud 原有说明"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer cloud.Close()
	invoke := func(input string) string {
		t.Helper()
		var out bytes.Buffer
		writer := bufio.NewWriter(&out)
		handleMcpLine(cloud.URL, "fake", input, writer, f.u)
		_ = writer.Flush()
		return out.String()
	}
	init := invoke(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if !strings.Contains(init, "cloud 原有说明") || !strings.Contains(init, "0.1.2") || !strings.Contains(init, "提醒一次") {
		t.Fatal("初始化应保留 cloud 说明并提醒新版", init)
	}
	list := invoke(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if !strings.Contains(list, "list_projects") || !strings.Contains(list, "check_cli_update") || !strings.Contains(list, "update_cli") || !strings.Contains(list, `"nextCursor":"next"`) || !strings.Contains(list, `"destructiveHint":true`) {
		t.Fatal("应保留云端工具和分页并追加本地工具", list)
	}
	next := invoke(`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"cursor":"next"}}`)
	if strings.Contains(next, "check_cli_update") {
		t.Fatal("后续分页不应重复本地工具", next)
	}
	before := cloudCalls
	checked := invoke(`{"jsonrpc":"2.0","id":"check","method":"tools/call","params":{"name":"check_cli_update","arguments":{}}}`)
	if cloudCalls != before || !strings.Contains(checked, `"id":"check"`) || !strings.Contains(checked, `"isError":false`) {
		t.Fatal("本地工具不能转发 cloud", checked)
	}
	rejected := invoke(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"update_cli","arguments":{"target_version":"0.1.2","confirm":false}}}`)
	if !strings.Contains(rejected, `"isError":true`) || cloudCalls != before || string(mustRead(t, f.path)) != "0.1.1" {
		t.Fatal("未确认更新应返回工具错误", rejected)
	}
	requests := len(f.requests)
	notification := invoke(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"update_cli","arguments":{"target_version":"0.1.2","confirm":true}}}`)
	if notification != "" || len(f.requests) != requests || string(mustRead(t, f.path)) != "0.1.1" {
		t.Fatal("通知不能触发更新", notification)
	}
	for _, input := range []string{
		`{"jsonrpc":"1.0","id":6,"method":"tools/call","params":{"name":"update_cli","arguments":{"target_version":"0.1.2","confirm":true}}}`,
		`{"jsonrpc":"2.0","id":true,"method":"tools/call","params":{"name":"update_cli","arguments":{"target_version":"0.1.2","confirm":true}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"update_cli","arguments":{"target_version":"0.1.2","confirm":true},"cursor":42}}`,
	} {
		response := invoke(input)
		if !strings.Contains(response, `"code":-32600`) || len(f.requests) != requests || string(mustRead(t, f.path)) != "0.1.1" {
			t.Fatal("非法协议请求不能绕过 cloud 验证触发本地更新", response)
		}
	}
}

func TestCLIAutomaticCheckHonorsDeadline(t *testing.T) {
	f := newUpdateFixture(t)
	f.u.client.Transport = updateTransport(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := f.u.check(ctx, true)
	if err == nil || time.Since(start) > time.Second {
		t.Fatal(fmt.Sprintf("检查应遵守超时：%v", err))
	}
}

func TestCLIReleaseTimeoutReservesFallbackBudget(t *testing.T) {
	f := newUpdateFixture(t)
	original := f.u.client.Transport
	f.u.client.Transport = updateTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.github.com" {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		return original.RoundTrip(r)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	release, err := f.u.release(ctx, false)
	if err != nil || release.Source != "gitee" {
		t.Fatalf("主源超时仍应给备用源留出时间：%+v %v", release, err)
	}
}

func TestCLIUpdateAttachmentFallback(t *testing.T) {
	f := newUpdateFixture(t)
	original := f.u.client.Transport
	f.u.client.Transport = updateTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "github.com" {
			return nil, fmt.Errorf("附件 CDN 不可达")
		}
		return original.RoundTrip(r)
	})
	result, err := f.u.update(context.Background(), "0.1.2", true)
	if err != nil || result["updated"] != true || string(mustRead(t, f.path)) != "0.1.2" {
		t.Fatalf("主源附件下载失败应尝试同版本的备用源：%+v %v", result, err)
	}
}

func TestCLIUpdateDoesNotOverwriteConcurrentInstallation(t *testing.T) {
	f := newUpdateFixture(t)
	original := f.u.binaryVersion
	f.u.binaryVersion = func(ctx context.Context, path string) (string, error) {
		if strings.HasPrefix(filepath.Base(path), ".moo-cloud-update-") {
			if err := os.WriteFile(f.path, []byte("0.1.3"), 0755); err != nil {
				t.Fatal(err)
			}
		}
		return original(ctx, path)
	}
	if _, err := f.u.update(context.Background(), "0.1.2", true); err == nil || string(mustRead(t, f.path)) != "0.1.3" {
		t.Fatalf("下载期间本机已更新为更高版本，必须保留：%v", err)
	}
}

func TestCLIUpdateAcceptsUnownedLockFile(t *testing.T) {
	f := newUpdateFixture(t)
	if err := os.WriteFile(f.path+".update.lock", nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.u.update(context.Background(), "0.1.2", true); err != nil {
		t.Fatalf("没有进程持有的遗留锁文件不应阻止更新：%v", err)
	}
}

func TestCLIUpdateActiveLockRejectsUpdate(t *testing.T) {
	f := newUpdateFixture(t)
	unlock, err := acquireCLIUpdateLock(f.path + ".update.lock")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := f.u.update(context.Background(), "0.1.2", true); err == nil || len(f.requests) != 0 || string(mustRead(t, f.path)) != "0.1.1" {
		t.Fatal("另一个更新者持锁时不得下载或替换文件", err)
	}
}

func TestCLIUpdateLockProcessChild(t *testing.T) {
	path := os.Getenv("MOO_CLI_UPDATE_LOCK_TEST_PATH")
	if path == "" {
		return
	}
	unlock, err := acquireCLIUpdateLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	fmt.Println("LOCKED")
	for {
		time.Sleep(time.Hour)
	}
}

func TestCLIUpdateLockRecoversAfterProcessCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.lock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCLIUpdateLockProcessChild$")
	cmd.Env = append(os.Environ(), "MOO_CLI_UPDATE_LOCK_TEST_PATH="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "LOCKED" {
		t.Fatalf("子进程未持锁：%q %v", line, err)
	}
	if unlock, err := acquireCLIUpdateLock(path); err == nil {
		unlock()
		t.Fatal("并发进程不应取得锁")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	unlock, err := acquireCLIUpdateLock(path)
	if err != nil {
		t.Fatalf("进程被强制结束后必须自动释放锁：%v", err)
	}
	unlock()
}

func TestCLIUpdateCancellationBeforeReplace(t *testing.T) {
	f := newUpdateFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	original := f.u.binaryVersion
	f.u.binaryVersion = func(ctx context.Context, path string) (string, error) {
		if strings.HasPrefix(filepath.Base(path), ".moo-cloud-update-") {
			cancel()
		}
		return original(ctx, path)
	}
	if _, err := f.u.update(ctx, "0.1.2", true); err == nil || string(mustRead(t, f.path)) != "0.1.1" {
		t.Fatal("请求取消后不得进入替换步骤", err)
	}
}

func TestCLIGiteeMissingAssetSizeFallback(t *testing.T) {
	f := newUpdateFixture(t)
	f.githubStatus, f.giteeOmitsSize = http.StatusForbidden, true
	checked, err := f.u.check(context.Background(), false)
	if err != nil || !checked.UpdateAvailable || checked.LatestVersion != "0.1.2" || !strings.Contains(checked.ReleaseURL, "gitee.com") {
		t.Fatalf("GitHub 限流后应使用真实 Gitee 元数据检查新版：%+v %v", checked, err)
	}
	result, err := f.u.update(context.Background(), "0.1.2", true)
	if err != nil || result["updated"] != true || string(mustRead(t, f.path)) != "0.1.2" {
		t.Fatalf("Gitee 未提供大小时仍应在下载校验后完成更新：%+v %v", result, err)
	}
}

func TestCLIGiteeUnknownSizeStillRejectsEmptyAndCorruptDownloads(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty=%v", empty), func(t *testing.T) {
			f := newUpdateFixture(t)
			f.githubStatus, f.giteeOmitsSize = http.StatusForbidden, true
			if empty {
				f.payload = nil
				digest := sha256.Sum256(nil)
				f.checksum = []byte(hex.EncodeToString(digest[:]) + "  moo-cloud-darwin-arm64\n")
			} else {
				f.checksum = []byte(strings.Repeat("0", 64) + "  moo-cloud-darwin-arm64\n")
			}
			release, err := f.u.release(context.Background(), false)
			if err != nil {
				t.Fatalf("缺少元数据大小不应阻断发布发现：%v", err)
			}
			if _, err := f.u.download(context.Background(), release); err == nil {
				t.Fatal("缺少大小不允许下载空文件或绕过 SHA-256")
			}
			if string(mustRead(t, f.path)) != "0.1.1" {
				t.Fatal("失败不应改动已安装文件")
			}
		})
	}
}

func TestCLICheckFailureKeepsLocalVersionsAndInitializationNotice(t *testing.T) {
	f := newUpdateFixture(t)
	f.githubStatus = http.StatusServiceUnavailable
	original := f.u.client.Transport
	f.u.client.Transport = updateTransport(func(r *http.Request) (*http.Response, error) {
		resp, err := original.RoundTrip(r)
		if err == nil && r.URL.Host == "gitee.com" {
			resp.StatusCode = http.StatusServiceUnavailable
		}
		return resp, err
	})
	if err := os.WriteFile(f.path, []byte("0.1.2"), 0755); err != nil {
		t.Fatal(err)
	}
	checked, err := f.u.check(context.Background(), false)
	if err == nil || checked.RunningVersion != "0.1.1" || checked.InstalledVersion != "0.1.2" || !checked.RestartRequired {
		t.Fatalf("发布源全失败也不能丢弃本机版本与重连状态：%+v %v", checked, err)
	}
	var output bytes.Buffer
	writer := bufio.NewWriter(&output)
	handleMcpLine("http://unused.test", "fixture", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"check_cli_update","arguments":{}}}`, writer, f.u)
	_ = writer.Flush()
	var rpc struct {
		Result struct {
			IsError bool                    `json:"isError"`
			Content []struct{ Text string } `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &rpc); err != nil || !rpc.Result.IsError || len(rpc.Result.Content) != 1 {
		t.Fatal("检查失败必须保留工具错误状态", err)
	}
	var details map[string]any
	if err := json.Unmarshal([]byte(rpc.Result.Content[0].Text), &details); err != nil || details["running_version"] != "0.1.1" || details["installed_version"] != "0.1.2" || details["restart_required"] != true || details["error"] == nil || details["latest_version"] != nil {
		t.Fatalf("失败回执应包含已知版本，不能谎报未知的最新版本：%+v %v", details, err)
	}
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "result": map[string]any{"instructions": "保留 cloud 原说明"}})
	}))
	defer cloud.Close()
	output.Reset()
	handleMcpLine(cloud.URL, "fixture", `{"jsonrpc":"2.0","id":2,"method":"initialize"}`, writer, f.u)
	_ = writer.Flush()
	for _, expected := range []string{"保留 cloud 原说明", "未完成", "不代表已是最新", "告知用户", "0.1.1", "0.1.2"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("初始化不能静默忽略检查失败，缺少 %q：%s", expected, output.String())
		}
	}
}

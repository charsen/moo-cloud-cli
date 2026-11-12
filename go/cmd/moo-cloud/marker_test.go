package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindMarkersIncludesCurrentRepositoryAndKeepsWorkspaceDepth(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace[local]")
	paths := []string{".", "one", "group/two", "too/deep/three"}
	for _, path := range paths {
		dir := filepath.Join(root, path)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".moo-cloud.json"), []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	markers := findMarkers(root)
	if len(markers) != 3 || markers[0] != filepath.Join(root, ".moo-cloud.json") {
		t.Fatalf("should discover current repository and two child levels: %v", markers)
	}
	for _, path := range markers {
		if strings.Contains(path, "three") {
			t.Fatal("must not scan beyond the existing workspace depth")
		}
	}
}

func TestFindMarkersDoesNotDuplicateSymlinkedRepository(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "app")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".moo-cloud.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repo, filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if markers := findMarkers(root); len(markers) != 1 {
		t.Fatalf("same physical repository should only be inspected once: %v", markers)
	}
}

func TestMarkerDecodeGivesFieldSpecificErrorsWithoutConfigValues(t *testing.T) {
	cases := []struct{ name, raw, message string }{
		{"syntax", "{\n\"role\": \"app\",\n}", "第 3 行"},
		{"array", `[]`, "顶层必须是 JSON 对象"},
		{"null", `null`, "不能是 null"},
		{"field type", `{"role":"app","project":123}`, "project 类型错误"},
		{"nested type", `{"role":"app","verify":[{"file":123}]}`, ".file 类型错误"},
		{"role", `{"role":"fixture-secret-value"}`, "role 无效"},
		{"platform", `{"role":"app","platforms":["fixture-secret-value"]}`, "platforms[0] 无效"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := decodeMarker([]byte(c.raw))
			if err == nil || !strings.Contains(err.Error(), c.message) || strings.Contains(err.Error(), "fixture-secret-value") {
				t.Fatalf("unexpected diagnostic: %v", err)
			}
		})
	}
}

func TestMarkerUnknownFieldsAreWarningsWithoutAddingRequiredFields(t *testing.T) {
	data, warnings, err := decodeMarker([]byte(`{"role":"app","project":"p","product":"app","root":".","proejct":"fixture-secret-value","verify":[{"file":"app.txt","contians":"example"}]}`))
	if err != nil || data.Project != "p" || len(warnings) != 3 {
		t.Fatalf("unexpected result: %+v %v %v", data, warnings, err)
	}
	message := strings.Join(warnings, "\n")
	if !strings.Contains(message, `"proejct"`) || !strings.Contains(message, `verify[0]."contians"`) || strings.Contains(message, "fixture-secret-value") {
		t.Fatalf("should identify misspelled fields without echoing values: %s", message)
	}
	// platforms 继续可省略，旧 App 的 root 只是冗余字段，不应因新提示被硬拒绝。
	if _, _, err := decodeMarker([]byte(`{"role":"app","project":"p","product":"app"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodeMarker([]byte(`{"root":"engine","deployments":[{"project":"p","env_file":".env"}]}`)); err != nil {
		t.Fatal("existing implicit-backend configuration must remain valid", err)
	}
}

func TestInspectAppFingerprintErrorsDistinguishEmptyMissingAndMismatch(t *testing.T) {
	cases := []struct{ file, contains, message string }{
		{"app.txt", "", "verify[0].contains 不能为空"},
		{"", "example", "verify[0].file 不能为空"},
		{"missing.txt", "example", "无法找到可读文件"},
		{".", "example", "无法找到可读文件"},
		{"../outside.txt", "example", "仓库内的相对文件路径"},
		{"app.txt", "different", "contains 指纹不匹配"},
	}
	for _, c := range cases {
		t.Run(c.message+c.file, func(t *testing.T) {
			marker := appRepo(t, t.TempDir())
			raw, _ := json.Marshal(map[string]any{"role": "app", "project": "p", "product": "product", "verify": []map[string]string{{"file": c.file, "contains": c.contains}}})
			if err := os.WriteFile(marker, raw, 0600); err != nil {
				t.Fatal(err)
			}
			report := inspectRepo(marker, pfOptions{offline: true})
			if report.HardFailures == 0 || !strings.Contains(strings.Join(report.Warnings, "\n"), c.message) {
				t.Fatalf("unexpected fingerprint diagnostic: %+v", report)
			}
		})
	}
}

func TestPreflightRunsInsideRepositoryAndKeepsFailureReport(t *testing.T) {
	marker := appRepo(t, t.TempDir())
	out := t.TempDir()
	args := []string{"--root=" + filepath.Dir(marker), "--out=" + out, "--offline"}
	if code := runPreflight(args); code != 0 {
		t.Fatalf("valid repository-local preflight failed: %d", code)
	}
	if err := os.WriteFile(marker, []byte(`{"role":"unknown"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code := runPreflight(args); code == 0 {
		t.Fatal("invalid configuration must return a nonzero exit status")
	}
	var report struct {
		HardFailures int `json:"hard_failures"`
	}
	if err := json.Unmarshal(mustRead(t, filepath.Join(out, "_preflight.json")), &report); err != nil || report.HardFailures != 1 {
		t.Fatalf("failed checks must still leave the diagnostic report: %+v %v", report, err)
	}
}

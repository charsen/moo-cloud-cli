package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func backendRepo(t *testing.T, workspace, appRoot string) string {
	t.Helper()
	repo := filepath.Join(workspace, "backend")
	app := filepath.Join(repo, appRoot)
	if err := os.MkdirAll(app, 0700); err != nil {
		t.Fatal(err)
	}
	if code, _ := gitCmd(repo, "init"); code != 0 {
		t.Fatal("fixture git init failed")
	}
	for name, body := range map[string]string{"artisan": "fixture", ".env": "MOO_MONITOR_CLOUD_TOKEN=fixture-project-token\n"} {
		if err := os.WriteFile(filepath.Join(app, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(repo, ".moo-cloud.json")
	raw, err := json.Marshal(map[string]any{
		"role": "backend", "root": appRoot,
		"deployments": []map[string]string{{"project": "p", "env_file": ".env"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return marker
}

func TestPreflightReportsRemoveLocalAndCloudRepositoryCredentials(t *testing.T) {
	marker := backendRepo(t, t.TempDir(), ".")
	localURL := "https://fixture-user:local-fixture-secret@example.test/local.git?token=local-query-secret#local-fragment-secret"
	cloudURL := "https://fixture-user:cloud-fixture-secret@example.test/cloud.git?token=cloud-query-secret#cloud-fragment-secret"
	if code, _ := gitCmd(filepath.Dir(marker), "remote", "add", "origin", localURL); code != 0 {
		t.Fatal("fixture remote failed")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"project": map[string]any{"slug": "p", "repo_url": cloudURL}})
	}))
	defer server.Close()
	out := t.TempDir()
	if code := runPreflight([]string{"--root=" + filepath.Dir(marker), "--out=" + out, "--cloud=" + server.URL, "--token=fixture"}); code != 0 {
		t.Fatal("repository URL mismatch should remain a warning")
	}
	for _, name := range []string{"_preflight.md", "_preflight.json"} {
		raw := string(mustRead(t, filepath.Join(out, name)))
		for _, secret := range []string{"fixture-user", "local-fixture-secret", "local-query-secret", "local-fragment-secret", "cloud-fixture-secret", "cloud-query-secret", "cloud-fragment-secret", "fixture-project-token"} {
			if strings.Contains(raw, secret) {
				t.Fatalf("%s leaked a synthetic credential", name)
			}
		}
		for _, location := range []string{"example.test/local.git", "example.test/cloud.git"} {
			if !strings.Contains(raw, location) {
				t.Fatalf("%s lost the repository location needed to explain the mismatch", name)
			}
		}
	}
	if _, remote := gitCmd(filepath.Dir(marker), "remote", "get-url", "origin"); remote != localURL {
		t.Fatal("redaction must not change the Git remote itself")
	}
}

func TestRepositoryRedactionPreservesIdentityAndHandlesInvalidURLs(t *testing.T) {
	for _, remote := range []string{
		"https://fixture-user:fixture-secret@example.test/team/repo.git?token=fixture-query#fixture-fragment",
		"ssh://fixture-user:fixture-secret@example.test/team/repo.git",
		"git@example.test:team/repo.git",
	} {
		if normalizeRemote(remote) != "example.test/team/repo" {
			t.Fatal("authentication details must not affect repository identity")
		}
	}
	remote := "git@example.test:team/repo.git"
	if redactRepoURL(remote) != remote {
		t.Fatal("normal SSH shorthand should remain readable")
	}
	if strings.Contains(redactRepoURL("https://fixture-user:fixture-secret%zz@example.test/repo.git"), "fixture-secret") {
		t.Fatal("invalid URL diagnostics must not echo credentials")
	}
}

func TestBackendGitRootFailureStopsNetworkAndQueueAdmission(t *testing.T) {
	workspace := t.TempDir()
	marker := backendRepo(t, workspace, "other")
	if code, _ := gitCmd(filepath.Join(filepath.Dir(marker), "other"), "init"); code != 0 {
		t.Fatal("fixture nested git init failed")
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"project": map[string]any{"slug": "p"}})
	}))
	defer server.Close()
	out := t.TempDir()
	if code := runPreflight([]string{"--root=" + workspace, "--out=" + out, "--fetch", "--cloud=" + server.URL, "--token=fixture"}); code == 0 {
		t.Fatal("Git root mismatch must fail preflight")
	}
	if calls.Load() != 0 {
		t.Fatal("failed repository identity must stop both summary verification and queue requests")
	}
	var report struct {
		HardFailures int          `json:"hard_failures"`
		Repos        []repoReport `json:"repos"`
	}
	if err := json.Unmarshal(mustRead(t, filepath.Join(out, "_preflight.json")), &report); err != nil {
		t.Fatal(err)
	}
	if report.HardFailures != 1 || len(report.Repos) != 1 || len(report.Repos[0].Deployments) != 0 {
		t.Fatal("failed identity diagnostics or deployment admission are incorrect")
	}
	var queue struct {
		Projects map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(mustRead(t, filepath.Join(out, "_queue.json")), &queue); err != nil || len(queue.Projects) != 0 {
		t.Fatal("failed repository must not produce a project queue", err)
	}
}

func TestBackendFailedEnvironmentKeepsValidEnvironmentQueue(t *testing.T) {
	workspace := t.TempDir()
	marker := backendRepo(t, workspace, ".")
	if err := os.WriteFile(marker, []byte(`{"role":"backend","root":".","deployments":[{"project":"p","env_file":".env"},{"project":"unavailable","env_file":"missing.env"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	server := queueServer(t, nil)
	defer server.Close()
	out := t.TempDir()
	if code := runPreflight([]string{"--root=" + workspace, "--out=" + out, "--offline", "--fetch", "--types=runtime", "--cloud=" + server.URL, "--token=fixture"}); code == 0 {
		t.Fatal("failed environment must remain visible in the exit status")
	}
	var queue struct {
		Projects map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(mustRead(t, filepath.Join(out, "_queue.json")), &queue); err != nil || len(queue.Projects) != 1 || queue.Projects["p"] == nil {
		t.Fatal("valid environment should still be fetched", err)
	}
}

func TestAppMarkdownIncludesIdentityErrorsAndOfflineWarning(t *testing.T) {
	for _, mode := range []string{"missing-token", "wrong-product", "unauthorized", "online", "offline"} {
		t.Run(mode, func(t *testing.T) {
			marker := appRepo(t, t.TempDir())
			server := queueServer(t, nil)
			defer server.Close()
			opts := pfOptions{token: "fixture", cloud: server.URL, offline: mode == "offline"}
			if mode == "missing-token" {
				opts.token = ""
			}
			if mode == "wrong-product" {
				raw := strings.Replace(string(mustRead(t, marker)), `"product":"product"`, `"product":"unknown"`, 1)
				if err := os.WriteFile(marker, []byte(raw), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "unauthorized" {
				denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
				defer denied.Close()
				opts.cloud = denied.URL
			}
			report := inspectRepo(marker, opts)
			if len(report.Deployments) != 1 {
				t.Fatal("fixture did not reach identity verification")
			}
			dep := report.Deployments[0]
			ok := mode == "online" || mode == "offline"
			if dep.OK != ok {
				t.Fatal("unexpected fixture verification result")
			}
			markdown := renderMarkdown("fixture", mode, []repoReport{report}, report.HardFailures, nil)
			status := "❌"
			if ok {
				status = "✅"
			}
			if !strings.Contains(markdown, "项目：`p`") || !strings.Contains(markdown, "身份校验："+status) {
				t.Fatal("App project and identity status are missing from Markdown")
			}
			for _, note := range append(dep.Errors, dep.Warnings...) {
				if !strings.Contains(markdown, note) {
					t.Fatal("Markdown dropped an identity error or offline warning")
				}
			}
		})
	}
}

func TestBooleanValuesAreConsistentAcrossTodoAndPreflight(t *testing.T) {
	t.Setenv("MOO_CLOUD_MCP_TOKEN", "fixture")
	for _, value := range []string{"true", "TRUE", "1", "t", "yes", "on", "false", "FALSE", "0", "F", "no", "off", "", "invalid"} {
		t.Run(value, func(t *testing.T) {
			invalid := value == "" || value == "invalid"
			want := value == "true" || value == "TRUE" || value == "1" || value == "t" || value == "yes" || value == "on"
			todo, err := parseTodoOptions([]string{"--project=p", "--title=fixture", "--yes=" + value, "--json=" + value})
			if (err != nil) != invalid || (!invalid && (todo.yes != want || todo.json != want)) {
				t.Fatal("todo boolean flags accepted an invalid value or changed its meaning")
			}
			pf, err := parsePfOptions([]string{"--offline=" + value, "--fetch=" + value, "--json=" + value})
			if (err != nil) != invalid || (!invalid && (pf.offline != want || pf.fetch != want || pf.json != want)) {
				t.Fatal("preflight boolean flags accepted an invalid value or changed its meaning")
			}
		})
	}
}

func TestConfigBooleanFlagsDoNotAccidentallyWriteOrForce(t *testing.T) {
	for _, target := range []string{"codex", "workbuddy"} {
		for _, flag := range []string{"--write=invalid", "--force=invalid", "--write=", "--force=", "--write=0", "--force=0", "--write=FALSE", "--force=no"} {
			t.Run(target+flag, func(t *testing.T) {
				home := t.TempDir()
				file := filepath.Join(home, ".codex", "config.toml")
				content := "[mcp_servers.moo_cloud]\ncommand = \"original-fixture\"\n"
				if target == "workbuddy" {
					file = filepath.Join(home, ".workbuddy", "mcp.json")
					content = `{"mcpServers":{"moo-cloud":{"command":"original-fixture"},"other":{"command":"preserve-me"}}}`
				}
				if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
				code := runAgentConfig([]string{"--target=" + target, "--home=" + home, "--write", "--force", flag})
				invalid := strings.HasSuffix(flag, "invalid") || strings.HasSuffix(flag, "=")
				if (code != 0) != invalid || string(mustRead(t, file)) != content {
					t.Fatal("boolean flags unexpectedly enabled writing or overwriting")
				}
				backups, err := filepath.Glob(file + ".bak-*")
				if err != nil || len(backups) != 0 {
					t.Fatal("rejected or disabled writes must not create backups", err)
				}
			})
		}
	}
}

func TestPreflightRejectsInvalidBooleanBeforeNetworkOrReports(t *testing.T) {
	workspace := t.TempDir()
	appRepo(t, workspace)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	for _, flag := range []string{"--offline=invalid", "--fetch=invalid", "--json=invalid"} {
		out := t.TempDir()
		code := runPreflight([]string{"--root=" + workspace, "--out=" + out, "--token=fixture", "--cloud=" + server.URL, flag})
		files, err := os.ReadDir(out)
		if code == 0 || err != nil || len(files) != 0 || calls.Load() != 0 {
			t.Fatal("invalid boolean must fail before networking or writing reports", err)
		}
	}
}

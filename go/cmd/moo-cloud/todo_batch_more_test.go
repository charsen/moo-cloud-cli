package main

import (
	"net/http/httptest"
	"testing"
)

// 覆盖 runTodoBatch 的错误分支与 json 汇总输出。
func TestTodoBatchErrorBranches(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"缺project", `{"items":[{"title":"甲"}]}`},
		{"category非法", `{"project":"p","items":[{"title":"甲","category":"weird"}]}`},
		{"priority非法", `{"project":"p","items":[{"title":"甲","priority":"urgent"}]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file := writeBatchFile(t, c.body)
			opts, err := parseTodoOptions([]string{"--batch=" + file, "--dry-run", "--cloud=http://localhost:1", "--token=t"})
			if err != nil {
				t.Fatal(err)
			}
			if code := runTodoBatch(opts); code == 0 {
				t.Fatalf("%s 应拒绝", c.name)
			}
		})
	}
}

func TestTodoBatchReadFileError(t *testing.T) {
	opts, err := parseTodoOptions([]string{"--batch=/nonexistent/report.json", "--dry-run", "--cloud=http://x", "--token=t"})
	if err != nil {
		t.Fatal(err)
	}
	if code := runTodoBatch(opts); code == 0 {
		t.Fatal("读不存在的文件应失败")
	}
}

func TestTodoBatchJSONOutput(t *testing.T) {
	mcp := &batchMCP{}
	srv := httptest.NewServer(mcp)
	defer srv.Close()
	file := writeBatchFile(t, `{"project":"p","items":[{"title":"甲"}]}`)
	// 带 --json 且提供 --yes 走真实创建路径，走 json 汇总分支。
	opts, err := parseTodoOptions([]string{"--batch=" + file, "--yes", "--json", "--cloud=" + srv.URL, "--token=t"})
	if err != nil {
		t.Fatal(err)
	}
	if code := runTodoBatch(opts); code != 0 {
		t.Fatalf("应成功，退出码 %d", code)
	}
	if len(mcp.created) != 1 {
		t.Fatalf("应创建 1 条，实际 %d", len(mcp.created))
	}
}

func TestTodoBatchResolveCloudError(t *testing.T) {
	file := writeBatchFile(t, `{"project":"p","items":[{"title":"甲"}]}`)
	// 非 https 且非 localhost 的 cloud 会被 validateCloudURL 拒绝。
	opts, err := parseTodoOptions([]string{"--batch=" + file, "--cloud=http://evil.example.com", "--token=t", "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	if code := runTodoBatch(opts); code == 0 {
		t.Fatal("非法 cloud 地址应失败")
	}
}

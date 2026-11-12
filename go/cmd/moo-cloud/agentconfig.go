package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func runAgentConfig(args []string) int {
	if contains(args, "--help") || contains(args, "-h") {
		fmt.Print(`agent-config：生成/写入客户端 MCP 配置

  --target=codex|workbuddy|json|claude
  --write    --force    --home=<dir>
`)

		return 0
	}

	opts := struct {
		target string
		write  bool
		force  bool
		home   string
	}{target: "codex"}

	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") {
			fmt.Fprintln(os.Stderr, "参数错误：请使用 --选项 或 --选项=值")
			return 1
		}
		key, value, hasValue := strings.Cut(arg[2:], "=")
		switch key {
		case "target", "home":
			if !hasValue || strings.TrimSpace(value) == "" {
				fmt.Fprintf(os.Stderr, "参数错误：--%s 需要非空值，请使用 --%s=值\n", key, key)
				return 1
			}
			if key == "target" {
				opts.target = value
			} else {
				opts.home = value
			}
		case "write", "force":
			enabled, err := parseBoolFlag(key, value, hasValue)
			if err != nil {
				fmt.Fprintln(os.Stderr, "参数错误："+err.Error())
				return 1
			}
			if key == "write" {
				opts.write = enabled
			} else {
				opts.force = enabled
			}
		default:
			fmt.Fprintf(os.Stderr, "参数错误：未知参数 --%s\n", key)
			return 1
		}
	}

	home := opts.home
	if home == "" {
		home, _ = os.UserHomeDir()
	} else {
		home = filepath.Clean(home)
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到自身可执行文件")

		return 1
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	entry := map[string]any{"type": "stdio", "command": exe, "args": []string{"mcp-server"}}

	switch opts.target {
	case "codex":
		return emitCodex(home, exe, opts.write, opts.force)
	case "workbuddy":
		return emitWorkbuddy(home, entry, opts.write, opts.force)
	case "json", "claude":
		raw, _ := json.MarshalIndent(map[string]any{
			"mcpServers": map[string]any{"moo-cloud": entry},
		}, "", "    ")
		fmt.Println(string(raw))

		return 0
	default:
		fmt.Fprintf(os.Stderr, "未知 target：%s（支持 codex / workbuddy / json / claude）\n", opts.target)

		return 1
	}
}

func codexBlock(exe string) string {
	readTools := []string{
		"list_projects", "list_issues", "get_issue", "get_issue_markdown",
		"list_apps", "list_todos", "get_todo", "get_todo_image",
		"list_comments",
	}
	var b strings.Builder
	b.WriteString("[mcp_servers.moo_cloud]\n")
	fmt.Fprintf(&b, "command = %s\n", tomlString(exe))
	fmt.Fprintf(&b, "args = [%s]\n", tomlString("mcp-server"))
	for _, tool := range readTools {
		fmt.Fprintf(&b, "\n[mcp_servers.moo_cloud.tools.%s]\napproval_mode = \"approve\"\n", tool)
	}

	return b.String()
}

// 配置可能由 dotfiles 软链接管理。先解析真实文件，原子写入与备份均针对目标，保留链接。
// 仅允许新建尚不存在的配置；断链或不可读文件必须报错，不能当成空配置覆盖。
func readClientConfig(file string) (string, []byte, error) {
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil {
		if !os.IsNotExist(err) {
			return file, nil, err
		}
		if _, statErr := os.Lstat(file); statErr == nil {
			return file, nil, err
		} else if !os.IsNotExist(statErr) {
			return file, nil, statErr
		}
		return file, nil, nil
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return file, nil, err
	}
	if !info.Mode().IsRegular() {
		return file, nil, fmt.Errorf("配置必须是普通文件")
	}
	raw, err := os.ReadFile(resolved)
	return resolved, raw, err
}

func emitCodex(home, exe string, write, force bool) int {
	block := codexBlock(exe)
	if !write {
		fmt.Print(block)

		return 0
	}

	file, raw, err := readClientConfig(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取配置失败：%v\n", err)
		return 1
	}
	existing := string(raw)
	if strings.Contains(existing, "[mcp_servers.moo_cloud]") {
		if !force {
			fmt.Fprintln(os.Stderr, "已存在 [mcp_servers.moo_cloud]，未改动（--force 可覆盖）")

			return 0
		}
		existing = stripCodexBlock(existing)
	}

	if err := backupFile(file); err != nil {
		fmt.Fprintf(os.Stderr, "备份配置失败：%v\n", err)
		return 1
	}
	if err := writeFile(file, strings.TrimRight(existing, "\n")+"\n\n"+block); err != nil {
		fmt.Fprintf(os.Stderr, "写入配置失败：%v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "已写入 %s；重启 Codex 后生效\n", file)

	return 0
}

func emitWorkbuddy(home string, entry map[string]any, write, force bool) int {
	if !write {
		raw, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"moo-cloud": entry}}, "", "    ")
		fmt.Println(string(raw))

		return 0
	}

	file, raw, err := readClientConfig(filepath.Join(home, ".workbuddy", "mcp.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取配置失败：%v\n", err)
		return 1
	}
	root := map[string]json.RawMessage{}
	if raw != nil {
		if err := json.Unmarshal(raw, &root); err != nil || root == nil {
			fmt.Fprintf(os.Stderr, "%s 不是合法 JSON，拒绝覆盖\n", file)

			return 1
		}
	}

	servers := map[string]json.RawMessage{}
	if raw, ok := root["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil || servers == nil {
			fmt.Fprintf(os.Stderr, "%s 的 mcpServers 必须是 JSON 对象，拒绝覆盖\n", file)
			return 1
		}
	}
	if _, exists := servers["moo-cloud"]; exists && !force {
		fmt.Fprintln(os.Stderr, "已存在 mcpServers.moo-cloud，未改动（--force 可覆盖）")

		return 0
	}

	encoded, _ := json.Marshal(entry)
	servers["moo-cloud"] = encoded
	serversRaw, _ := json.Marshal(servers)
	root["mcpServers"] = serversRaw

	if err := backupFile(file); err != nil {
		fmt.Fprintf(os.Stderr, "备份配置失败：%v\n", err)
		return 1
	}
	raw, _ = json.MarshalIndent(root, "", "    ")
	if err := writeFile(file, string(raw)+"\n"); err != nil {
		fmt.Fprintf(os.Stderr, "写入配置失败：%v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "已写入 %s\n", file)
	fmt.Fprintln(os.Stderr, "下一步：完全退出 WorkBuddy（Cmd+Q）→ 重开 → 在 MCP 面板启用 moo-cloud → 新建会话")

	return 0
}

func stripCodexBlock(toml string) string {
	kept := []string{}
	skip := false
	for _, line := range strings.Split(toml, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") {
			skip = strings.HasPrefix(trim, "[mcp_servers.moo_cloud]") || strings.HasPrefix(trim, "[mcp_servers.moo_cloud.")
		}
		if !skip {
			kept = append(kept, line)
		}
	}

	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func backupFile(file string) error {
	raw, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.WriteFile(file+".bak-"+time.Now().Format("20060102150405.000000000"), raw, 0o600)
}

func tomlString(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r", "\t", "\\t")

	return "\"" + replacer.Replace(value) + "\""
}

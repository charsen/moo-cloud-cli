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
	opts := struct {
		target string
		write  bool
		force  bool
		home   string
	}{target: "codex"}

	for _, arg := range args {
		key, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		switch key {
		case "target":
			if hasValue {
				opts.target = value
			}
		case "write":
			opts.write = true
			if hasValue {
				if parsed, err := strconvParseBool(value); err == nil {
					opts.write = parsed
				}
			}
		case "force":
			opts.force = true
			if hasValue {
				if parsed, err := strconvParseBool(value); err == nil {
					opts.force = parsed
				}
			}
		case "home":
			if hasValue {
				opts.home = value
			}
		}
	}

	home := opts.home
	if home == "" {
		home, _ = os.UserHomeDir()
	} else {
		home = strings.TrimRight(home, "/")
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

func emitCodex(home, exe string, write, force bool) int {
	block := codexBlock(exe)
	if !write {
		fmt.Print(block)

		return 0
	}

	file := filepath.Join(home, ".codex", "config.toml")
	raw, _ := os.ReadFile(file)
	existing := string(raw)
	if strings.Contains(existing, "[mcp_servers.moo_cloud]") {
		if !force {
			fmt.Fprintln(os.Stderr, "已存在 [mcp_servers.moo_cloud]，未改动（--force 可覆盖）")

			return 0
		}
		existing = stripCodexBlock(existing)
	}

	backupFile(file)
	writeFile(file, strings.TrimRight(existing, "\n")+"\n\n"+block)
	fmt.Fprintf(os.Stderr, "已写入 %s；重启 Codex 后生效\n", file)

	return 0
}

func emitWorkbuddy(home string, entry map[string]any, write, force bool) int {
	if !write {
		raw, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"moo-cloud": entry}}, "", "    ")
		fmt.Println(string(raw))

		return 0
	}

	file := filepath.Join(home, ".workbuddy", "mcp.json")
	root := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(file); err == nil {
		if err := json.Unmarshal(raw, &root); err != nil {
			fmt.Fprintf(os.Stderr, "%s 不是合法 JSON，拒绝覆盖\n", file)

			return 1
		}
	}

	servers := map[string]json.RawMessage{}
	if raw, ok := root["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			servers = map[string]json.RawMessage{}
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

	backupFile(file)
	raw, _ := json.MarshalIndent(root, "", "    ")
	writeFile(file, string(raw)+"\n")
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

func backupFile(file string) {
	if _, err := os.Stat(file); err != nil {
		return
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return
	}
	_ = os.WriteFile(file+".bak-"+time.Now().Format("20060102150405"), raw, 0o600)
}

func tomlString(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r", "\t", "\\t")

	return "\"" + replacer.Replace(value) + "\""
}

func strconvParseBool(value string) (bool, error) {
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid bool")
	}
}

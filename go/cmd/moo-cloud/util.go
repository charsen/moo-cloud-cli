package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var cst = time.FixedZone("CST", 8*3600)

// 裸布尔开关表示启用；显式值必须可识别，不能把拼写错误当成写入/确认授权。
func parseBoolFlag(name, value string, hasValue bool) (bool, error) {
	if !hasValue {
		return true, nil
	}
	switch strings.ToLower(value) {
	case "1", "t", "true", "yes", "on":
		return true, nil
	case "0", "f", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("--%s 需要布尔值：true/false、1/0、yes/no 或 on/off", name)
	}
}

// 报告只需要仓库位置；URL 用户信息、查询串和片段都不应进入可分享的报告。
// 无 scheme 的 Git SSH 简写仍保留 git@host:path，避免改坏正常仓库地址。
func redactRepoURL(remote string) string {
	remote = strings.TrimSpace(remote)
	if !strings.Contains(remote, "://") {
		return remote
	}
	u, err := url.Parse(remote)
	if err != nil {
		// 解析错误可能包含原 URL；不回显错误或原值，防止非法转义中的凭据泄露。
		return "[无效仓库地址]"
	}
	u.User = nil
	u.RawQuery, u.Fragment, u.RawFragment = "", "", ""
	u.ForceQuery = false
	return u.String()
}

func envValue(file, key string) (string, bool) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.HasPrefix(line, key+"=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, key+"="))
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') {
			return strings.Trim(value, "\"'"), true
		}
		// 裸值截掉行内注释：TOKEN=abc # 说明
		if i := strings.Index(value, " #"); i >= 0 {
			value = value[:i]
		}
		return strings.TrimSpace(value), true
	}

	return "", false
}

func initialToken() string {
	if v := strings.TrimSpace(os.Getenv("MOO_CLOUD_MCP_TOKEN")); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		path := filepath.Join(home, ".config", "moo-fleet", "mcp-token")
		if raw, err := os.ReadFile(path); err == nil {
			if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
				fmt.Fprintf(os.Stderr, "警告：%s 权限过宽（%o），建议 chmod 600\n", path, info.Mode().Perm())
			}

			return strings.TrimSpace(string(raw))
		}
	}

	return ""
}

func httpJSON(url string, payload any, headers map[string]string, timeout time.Duration) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}

	// 只对网络错误 / 429 / 5xx 重试；4xx 是确定性错误，重试没有意义。
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
		}

		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		client := &http.Client{Timeout: timeout}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err

			continue
		}
		raw, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr

			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)

			continue
		}

		return resp.StatusCode, raw, nil
	}

	return 0, nil, lastErr
}

// mcpCall 调云端 tools/call，返回工具 JSON 文本解码后的对象。
func mcpCall(cloud, token, tool string, args map[string]any) (map[string]any, error) {
	status, body, err := httpJSON(cloud+"/api/v1/mcp", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	}, map[string]string{"Authorization": "Bearer " + token}, 20*time.Second)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("MCP 返回 HTTP %d", status)
	}

	var resp struct {
		Result *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("MCP 响应不是 JSON：%w", err)
	}
	if resp.Result == nil || len(resp.Result.Content) == 0 {
		msg := "缺少 result"
		if resp.Error != nil && resp.Error.Message != "" {
			msg = resp.Error.Message
		}

		return nil, fmt.Errorf("MCP 协议错误：%s", msg)
	}
	text := resp.Result.Content[0].Text
	if resp.Result.IsError {
		return nil, fmt.Errorf("工具返回错误：%s", text)
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		return nil, fmt.Errorf("工具返回不是 JSON 对象")
	}

	return data, nil
}

// 从云端读取工具定义，用于预检按账号实际权限确定可用的队列工具。
type mcpToolDefinition struct {
	Name        string `json:"name"`
	InputSchema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	} `json:"inputSchema"`
}

func mcpDefinitions(cloud, token string) ([]mcpToolDefinition, error) {
	status, body, err := httpJSON(cloud+"/api/v1/mcp", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]any{},
	}, map[string]string{"Authorization": "Bearer " + token}, 20*time.Second)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("tools/list 返回 HTTP %d", status)
	}
	var resp struct {
		Result *struct {
			Tools []mcpToolDefinition `json:"tools"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list 协议错误：%s", resp.Error.Message)
	}
	if resp.Result == nil || resp.Result.Tools == nil {
		return nil, fmt.Errorf("tools/list 缺少工具清单")
	}
	return resp.Result.Tools, nil
}

func mcpTools(cloud, token string) ([]string, error) {
	definitions, err := mcpDefinitions(cloud, token)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(definitions))
	for _, tool := range definitions {
		names = append(names, tool.Name)
	}
	return names, nil
}

/** 追加一行 JSON（用于跨天趋势日志；失败不影响主流程）。 */
func appendJSONLine(path string, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o775)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(raw, '\n'))
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}

	return false
}

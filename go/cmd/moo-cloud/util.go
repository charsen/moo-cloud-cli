package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var cst = time.FixedZone("CST", 8*3600)

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
		if raw, err := os.ReadFile(filepath.Join(home, ".config", "moo-fleet", "mcp-token")); err == nil {
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
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)

	return resp.StatusCode, raw, err
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

func mcpTools(cloud, token string) ([]string, error) {
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
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(resp.Result.Tools))
	for _, t := range resp.Result.Tools {
		names = append(names, t.Name)
	}

	return names, nil
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}

	return false
}

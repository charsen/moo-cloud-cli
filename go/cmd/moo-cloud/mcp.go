package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func runMcpServer(args []string) int {
	if contains(args, "--help") || contains(args, "-h") {
		fmt.Println("mcp-server：把云端 HTTP MCP 包成本地 stdio MCP\n\n用法: moo-cloud mcp-server [--token=...] [--cloud=...]")

		return 0
	}

	token := ""
	cloud := ""
	logPath := ""
	for _, arg := range args {
		key, value, ok := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if !ok {
			continue
		}
		switch key {
		case "token":
			token = value
		case "cloud":
			cloud = value
		case "log":
			logPath = value
		}
	}
	if token == "" {
		token = initialToken()
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "mcp-server: 缺少 token（--token / MOO_CLOUD_MCP_TOKEN / ~/.config/moo-fleet/mcp-token）")

		return 1
	}
	if cloud == "" {
		cloud = os.Getenv("MOO_CLOUD_URL")
	}
	if cloud == "" {
		cloud = "https://c.mooeen.com"
	}
	cloud = strings.TrimRight(cloud, "/")

	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			started := time.Now()
			ok, errMsg := handleMcpLine(cloud+"/api/v1/mcp", token, strings.TrimSpace(line), writer)
			writer.Flush()
			if logPath != "" {
				appendMcpLog(logPath, line, ok, errMsg, time.Since(started).Milliseconds())
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}

			return 1
		}
	}

	return 0
}

func handleMcpLine(endpoint, token, line string, writer *bufio.Writer) (bool, string) {
	if line == "" {
		return true, ""
	}

	var msg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-server: 忽略非 JSON 输入")

		return false, "invalid json"
	}

	rawID, hasID := msg["id"]
	if hasID && string(rawID) == "null" {
		hasID = false
	}

	body, err := forwardMcp(endpoint, token, line)
	if err != nil {
		if hasID {
			writeRaw(writer, map[string]any{
				"jsonrpc": "2.0", "id": json.RawMessage(rawID),
				"error": map[string]any{"code": -32603, "message": err.Error()},
			})
		}

		return false, err.Error()
	}
	if !hasID {
		return true, ""
	}

	var response map[string]json.RawMessage
	if err := json.Unmarshal(body, &response); err != nil {
		writeRaw(writer, map[string]any{
			"jsonrpc": "2.0", "id": json.RawMessage(rawID),
			"error": map[string]any{"code": -32603, "message": "云端返回非 JSON"},
		})

		return false, "云端返回非 JSON"
	}
	var version string
	_ = json.Unmarshal(response["jsonrpc"], &version)
	_, hasResult := response["result"]
	rawError, hasError := response["error"]
	var rpcErr struct {
		Code    *int    `json:"code"`
		Message *string `json:"message"`
	}
	validError := !hasError || (json.Unmarshal(rawError, &rpcErr) == nil && rpcErr.Code != nil && rpcErr.Message != nil)
	if version != "2.0" || hasResult == hasError || !validError {
		writeRaw(writer, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(rawID),
			"error": map[string]any{"code": -32603, "message": "云端返回非法 JSON-RPC"}})
		return false, "云端返回非法 JSON-RPC"
	}
	if existing, ok := response["id"]; !ok || string(existing) == "null" {
		response["id"] = json.RawMessage(rawID)
	}

	writeRaw(writer, response)
	if hasError {
		return false, "云端返回 JSON-RPC 错误"
	}
	var toolResult struct {
		IsError bool `json:"isError"`
	}
	_ = json.Unmarshal(response["result"], &toolResult)
	if toolResult.IsError {
		return false, "云端工具执行失败"
	}
	return true, ""
}

/** MCP 桥调用日志：一行一条 JSON，默认关闭（--log=<file> 开启）。 */
func appendMcpLog(path, line string, ok bool, errMsg string, durationMs int64) {
	var msg struct {
		ID     any    `json:"id"`
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	_ = json.Unmarshal([]byte(line), &msg)
	entry := map[string]any{
		"ts":          time.Now().UTC().Format(time.RFC3339),
		"id":          msg.ID,
		"method":      msg.Method,
		"tool":        msg.Params.Name,
		"ok":          ok,
		"duration_ms": durationMs,
	}
	if errMsg != "" {
		entry["error"] = errMsg
	}
	appendJSONLine(path, entry)
}

func forwardMcp(endpoint, token, body string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("云端不可达：%w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// 中间件的 401/403/429 是 HTTP 错误体，不能直接当 JSON-RPC 转发。
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("云端 MCP 返回 HTTP %d", resp.StatusCode)
	}

	// 云端当前返回 JSON；如果以后改成 SSE（text/event-stream），取最后一条 data。
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		if event, ok := lastSseData(raw); ok {
			return event, nil
		}
	}

	return raw, nil
}

/** 从 SSE 响应里取最后一个非空 data 行（MCP 的 JSON-RPC 消息在 data 里）。 */
func lastSseData(raw []byte) ([]byte, bool) {
	var last []byte
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload != "" && payload != "[DONE]" {
			last = []byte(payload)
		}
	}

	return last, last != nil
}

func writeRaw(writer *bufio.Writer, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	writer.Write(raw)
	writer.WriteByte('\n')
}

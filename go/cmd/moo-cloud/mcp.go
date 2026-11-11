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
			handleMcpLine(cloud+"/api/v1/mcp", token, strings.TrimSpace(line), writer)
			writer.Flush()
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

func handleMcpLine(endpoint, token, line string, writer *bufio.Writer) {
	if line == "" {
		return
	}

	var msg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-server: 忽略非 JSON 输入")

		return
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

		return
	}
	if !hasID {
		return
	}

	var response map[string]json.RawMessage
	if err := json.Unmarshal(body, &response); err != nil {
		writeRaw(writer, map[string]any{
			"jsonrpc": "2.0", "id": json.RawMessage(rawID),
			"error": map[string]any{"code": -32603, "message": "云端返回非 JSON"},
		})

		return
	}
	if existing, ok := response["id"]; !ok || string(existing) == "null" {
		response["id"] = json.RawMessage(rawID)
	}

	writeRaw(writer, response)
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

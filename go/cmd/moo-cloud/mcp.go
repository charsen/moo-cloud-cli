package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// claimReminder 追加到 MCP 初始化说明：要求操作者「开始处理即领取」，避免静默抢活。
// 领取同时写入 in_progress 与领取人（由云端 update_todo_status 记录）。
const claimReminder = "开始处理待办前，必须先调用 update_todo_status 将状态置为 in_progress 完成领取（同时记录领取状态与领取人），并在说明中写明领取人；处理并验证后再用 status=done 加 note 写回处理结果与验证。不要未领取就静默处理。"

// appendClaimReminder 在云端给的 initialize 说明后追加「先领取」提醒，保留原文。
func appendClaimReminder(instructions string) string {
	return strings.TrimSpace(instructions + "\n" + claimReminder)
}

func runMcpServer(args []string) int {
	if contains(args, "--help") || contains(args, "-h") {
		fmt.Println("mcp-server：把云端 HTTP MCP 包成本地 stdio MCP\n\n用法: moo-cloud mcp-server [--token=...] [--cloud=...]")

		return 0
	}

	token := ""
	cloud := ""
	logPath := ""
	known := map[string]*string{"token": &token, "cloud": &cloud, "log": &logPath}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") {
			fmt.Fprintln(os.Stderr, "mcp-server: 请使用 --选项=值")
			return 1
		}
		key, value, hasValue := strings.Cut(arg[2:], "=")
		target, ok := known[key]
		if !ok {
			fmt.Fprintf(os.Stderr, "mcp-server: 未知参数 --%s\n", key)
			return 1
		}
		if !hasValue || strings.TrimSpace(value) == "" {
			fmt.Fprintf(os.Stderr, "mcp-server: --%s 需要非空值，请使用 --%s=值\n", key, key)
			return 1
		}
		*target = value
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
	validatedCloud, verr := validateCloudURL(cloud)
	if verr != nil {
		fmt.Fprintln(os.Stderr, "mcp-server: "+verr.Error())

		return 1
	}
	cloud = validatedCloud

	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	updater := newCLIUpdater()
	defer writer.Flush()

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			started := time.Now()
			ok, errMsg := handleMcpLine(cloud+"/api/v1/mcp", token, strings.TrimSpace(line), writer, updater)
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

func handleMcpLine(endpoint, token, line string, writer *bufio.Writer, updater *cliUpdater) (bool, string) {
	if line == "" {
		return true, ""
	}

	var msg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-server: 忽略非 JSON 输入")

		return false, "invalid json"
	}

	rawID, hasID := msg["id"]
	// 仅缺少 id 才是通知；显式 null 须返回协议错误，与 cloud 保持一致。
	var method string
	_ = json.Unmarshal(msg["method"], &method)
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Cursor    string          `json:"cursor"`
	}
	paramsErr := json.Unmarshal(msg["params"], &params)
	if method == "tools/call" && (params.Name == "check_cli_update" || params.Name == "update_cli") {
		// 本机能力不转发 cloud；通知没有响应 id，也不能借通知触发本机写入。
		if !hasID {
			return false, "本地工具调用必须带 id"
		}
		var rpcVersion string
		_ = json.Unmarshal(msg["jsonrpc"], &rpcVersion)
		// 云端原先负责协议验证；本机拦截的写工具也必须验证请求，不能绕过这层边界。
		validID := validMcpID(rawID)
		if rpcVersion != "2.0" || !validID || paramsErr != nil {
			var id any
			if validID {
				id = json.RawMessage(rawID)
			}
			writeRaw(writer, map[string]any{"jsonrpc": "2.0", "id": id,
				"error": map[string]any{"code": -32600, "message": "非法的本地 MCP 工具请求"}})
			return false, "非法的本地 MCP 工具请求"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		data, err := updater.callTool(ctx, params.Name, params.Arguments)
		text, _ := json.Marshal(data)
		if err != nil {
			failure := map[string]any{"error": err.Error()}
			if check, ok := data.(cliUpdateCheck); ok {
				// 检查失败仍返回已知本机状态，不返回无法确认的 latest_version/update_available。
				failure["running_version"] = check.RunningVersion
				if check.InstalledVersion != "" {
					failure["installed_version"], failure["restart_required"] = check.InstalledVersion, check.RestartRequired
				}
			}
			text, _ = json.Marshal(failure)
		}
		writeRaw(writer, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(rawID), "result": map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(text)}}, "isError": err != nil,
		}})
		if err != nil {
			return false, err.Error()
		}
		return true, ""
	}

	requestedImages := 0
	if method == "tools/call" && params.Name == "create_todo" {
		var args map[string]json.RawMessage
		_ = json.Unmarshal(params.Arguments, &args)
		paths, hasPaths := args["image_paths"]
		_, hasImages := args["images"]
		if hasPaths || hasImages {
			// 本机读文件同样要求合法的请求与响应 id；通知不得触发本机 I/O。
			if !hasID {
				return false, "带图片的工具调用必须带 id"
			}
			var rpcVersion string
			_ = json.Unmarshal(msg["jsonrpc"], &rpcVersion)
			validID := validMcpID(rawID)
			if rpcVersion != "2.0" || !validID || paramsErr != nil {
				var id any
				if validID {
					id = json.RawMessage(rawID)
				}
				writeRaw(writer, map[string]any{"jsonrpc": "2.0", "id": id,
					"error": map[string]any{"code": -32600, "message": "非法的图片工具请求"}})
				return false, "非法的图片工具请求"
			}
			if hasPaths {
				if hasImages {
					return writeTodoImageError(writer, rawID, fmt.Errorf("image_paths 与 images 只能选择一种"))
				}
				var filenames []string
				if json.Unmarshal(paths, &filenames) != nil || string(paths) == "null" {
					return writeTodoImageError(writer, rawID, fmt.Errorf("image_paths 必须是图片绝对路径数组"))
				}
				images, err := loadTodoImages(filenames, true)
				if err != nil {
					return writeTodoImageError(writer, rawID, err)
				}
				delete(args, "image_paths")
				args["images"], _ = json.Marshal(images)
				var rawParams map[string]json.RawMessage
				_ = json.Unmarshal(msg["params"], &rawParams)
				rawParams["arguments"], _ = json.Marshal(args)
				msg["params"], _ = json.Marshal(rawParams)
				encoded, _ := json.Marshal(msg)
				line = string(encoded)
			}
			var images []string
			if json.Unmarshal(args["images"], &images) == nil {
				requestedImages = len(images)
			}
		}
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
	if !hasError && (method == "initialize" || method == "tools/list") {
		var result map[string]json.RawMessage
		if json.Unmarshal(response["result"], &result) == nil && result != nil {
			if method == "initialize" {
				var instructions string
				_ = json.Unmarshal(result["instructions"], &instructions)
				// 自动检查有 3 秒总预算，并复用当天缓存，不因检查失败阻塞业务工具。
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				check, err := updater.check(ctx, true)
				cancel()
				updateNotice := "本地 CLI 提供 check_cli_update 检查新版；必须先向用户说明目标版本并取得明确同意，才能调用 update_cli。更新后需重新连接 MCP。"
				if err == nil {
					updateNotice += " 当前进程 " + check.RunningVersion + "，本机安装 " + check.InstalledVersion + "，最新发布 " + check.LatestVersion + "。" + check.Message
					if check.UpdateAvailable || check.RestartRequired {
						updateNotice += " 请在本次会话开始时向用户提醒一次。"
					}
				} else {
					updateNotice += " 当前进程 " + check.RunningVersion + "。"
					if check.InstalledVersion != "" {
						updateNotice += " 本机安装 " + check.InstalledVersion + "。"
					}
					// 自动检查失败不能静默伪装成没有新版；业务工具继续可用，提醒仅在会话开始时给出。
					updateNotice += " 本次自动更新检查未完成，不代表已是最新；请在本次会话开始时告知用户，可稍后调用 check_cli_update 重试。"
					if check.RestartRequired {
						updateNotice += " 当前 MCP 仍是旧进程，请重新连接 MCP。"
					}
				}
				result["instructions"], _ = json.Marshal(appendClaimReminder(instructions + "\n" + updateNotice))
			} else {
				var tools []json.RawMessage
				if json.Unmarshal(result["tools"], &tools) == nil {
					for i := range tools {
						tools[i] = todoImageToolDefinition(tools[i])
					}
					if params.Cursor == "" {
						for _, tool := range updater.toolDefinitions() {
							raw, _ := json.Marshal(tool)
							tools = append(tools, raw)
						}
					}
					result["tools"], _ = json.Marshal(tools)
				}
			}
			response["result"], _ = json.Marshal(result)
		}
	}

	if requestedImages > 0 && !hasError {
		var result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(response["result"], &result) != nil {
			return writeTodoImageError(writer, rawID, fmt.Errorf("云端图片保存响应格式错误，请检查附件后再重试"))
		}
		if !result.IsError {
			data := map[string]any{}
			if len(result.Content) > 0 {
				_ = json.Unmarshal([]byte(result.Content[0].Text), &data)
			}
			if err := verifyTodoImages(data, requestedImages); err != nil {
				return writeTodoImageError(writer, rawID, err)
			}
		}
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

// 与 cloud 的请求边界一致：字符串或有符号 64 位整数。
// 不用首字符或 float64 判断，避免小数/科学计数法/溢出 ID 触发本机写入或文件读取。
func validMcpID(raw json.RawMessage) bool {
	if len(raw) > 0 && raw[0] == '"' {
		var id string
		return json.Unmarshal(raw, &id) == nil
	}
	_, err := strconv.ParseInt(string(raw), 10, 64)
	return err == nil
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

// 本机图片错误使用 MCP 工具错误，Agent 可直接向用户说明，不伪装成上传成功。
func writeTodoImageError(writer *bufio.Writer, id json.RawMessage, err error) (bool, string) {
	text, _ := json.Marshal(map[string]any{"error": err.Error()})
	writeRaw(writer, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(text)}}, "isError": true,
	}})
	return false, err.Error()
}

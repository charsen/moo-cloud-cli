package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

func (u *cliUpdater) toolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name": "check_cli_update", "description": "检查本地 moo-cloud CLI 是否有新版，返回运行版本、本机安装版本及最新稳定版。只检查，不安装；发现新版后告知用户并询问是否更新。",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
			"annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
		},
		{
			"name": "update_cli", "description": "更新本机 moo-cloud CLI。必须先运行 check_cli_update，向用户说明目标版本并取得明确同意；不得因检测到新版就自行调用。下载官方平台文件，校验 SHA-256、备份并替换；完成后提醒重新连接 MCP。",
			"inputSchema": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"target_version", "confirm"},
				"properties": map[string]any{
					"target_version": map[string]any{"type": "string", "description": "用户明确同意安装的版本，例如 0.1.2"},
					"confirm":        map[string]any{"type": "boolean", "const": true, "description": "仅在用户明确同意此版本更新后设为 true"},
				},
			},
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": true},
		},
	}
}

func (u *cliUpdater) callTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("arguments 必须是 JSON 对象")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if name == "check_cli_update" {
		var args struct{}
		if err := decoder.Decode(&args); err != nil {
			return nil, fmt.Errorf("check_cli_update 不接收参数")
		}
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return u.check(ctx, false)
	}
	var args struct {
		TargetVersion string `json:"target_version"`
		Confirm       bool   `json:"confirm"`
	}
	if err := decoder.Decode(&args); err != nil {
		return nil, fmt.Errorf("更新参数错误：%w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("更新参数必须是一个 JSON 对象")
	}
	return u.update(ctx, args.TargetVersion, args.Confirm)
}

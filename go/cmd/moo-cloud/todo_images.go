package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const maxTodoImageBytes = 8 * 1024 * 1024

// 由本机读取图片，避免要求 Agent 把大段 base64 放进对话上下文。只接受显式提供的普通文件。
func loadTodoImages(paths []string, absolute bool) ([]string, error) {
	if len(paths) > 5 {
		return nil, fmt.Errorf("最多附加 5 张图片")
	}
	images := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" || (absolute && !filepath.IsAbs(path)) {
			return nil, fmt.Errorf("image_paths 必须提供图片的绝对路径")
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("读取图片 %s：%w", path, err)
		}
		if !info.Mode().IsRegular() || info.Size() > maxTodoImageBytes {
			return nil, fmt.Errorf("图片 %s 必须是普通文件且不超过 8 MiB", path)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("打开图片 %s：%w", path, err)
		}
		data, err := io.ReadAll(io.LimitReader(file, maxTodoImageBytes+1))
		file.Close()
		if err != nil || len(data) > maxTodoImageBytes {
			return nil, fmt.Errorf("图片 %s 读取失败或超过 8 MiB", path)
		}
		mime := http.DetectContentType(data)
		switch mime {
		case "image/png", "image/jpeg", "image/webp", "image/gif":
		default:
			return nil, fmt.Errorf("图片 %s 仅支持 PNG/JPEG/WebP/GIF", path)
		}
		images = append(images, "data:"+mime+";base64,"+base64.StdEncoding.EncodeToString(data))
	}
	return images, nil
}

// 有传图就必须拿到明确回执；不能把仅创建了文字的响应当作上传成功。
func verifyTodoImages(result map[string]any, requested int) error {
	if requested == 0 || result["requires_confirmation"] == true {
		return nil
	}
	if count, ok := result["images_attached"].(float64); ok && count == float64(requested) {
		return nil
	}
	todo, _ := result["todo"].(map[string]any)
	id, _ := todo["id"].(string)
	return fmt.Errorf("云端未确认保存全部 %d 张图片（待办 ID：%s）；请检查附件后再重试，不能视为上传成功", requested, id)
}

// 只扩展 cloud 已声明支持图片的创建工具；本地路径不会发给远端。
func todoImageToolDefinition(raw json.RawMessage) json.RawMessage {
	var tool map[string]any
	if json.Unmarshal(raw, &tool) != nil || tool["name"] != "create_todo" {
		return raw
	}
	schema, _ := tool["inputSchema"].(map[string]any)
	properties, _ := schema["properties"].(map[string]any)
	if properties["images"] == nil {
		return raw
	}
	properties["image_paths"] = map[string]any{
		"type": "array", "maxItems": 5, "items": map[string]any{"type": "string"},
		"description": "用户提供图片时请一并传入图片文件的绝对路径；最多 5 张，每张不超过 8 MiB（像素上限由云端校验），支持 PNG/JPEG/WebP/GIF。本地桥读取并上传，两次确认调用须使用相同图片。与 images 二选一。拿不到原图时明确告知用户，不能声称图片已上传。",
	}
	tool["description"] = fmt.Sprint(tool["description"]) + " 本机可用 image_paths 上传用户附图，不需要手工编码 base64。"
	encoded, _ := json.Marshal(tool)
	return encoded
}

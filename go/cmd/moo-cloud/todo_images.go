package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxTodoImageBytes = 8 * 1024 * 1024

// 待办图片上传模式：默认 auto（先试七牛直传，云端不支持/未配置时回退旧 base64）。
const (
	todoImageModeAuto   = "auto"
	todoImageModeDirect = "direct"
	todoImageModeLegacy = "legacy"
)

// todoImageMode 从环境变量读取；未设置或非法值按 auto 处理。
func todoImageMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MOO_CLOUD_TODO_IMAGE_MODE"))) {
	case todoImageModeDirect:
		return todoImageModeDirect
	case todoImageModeLegacy:
		return todoImageModeLegacy
	default:
		return todoImageModeAuto
	}
}

// todoImageFile 是本机读取的一张待传图片：保留原始字节供 multipart 直传七牛，
// 同时给出 sha256/size/mime 供云端票据与核验。
type todoImageFile struct {
	Path   string
	Name   string
	Mime   string
	Size   int64
	SHA256 string
	Data   []byte
}

// loadTodoImageFiles 只接受显式提供的普通文件；读一次即算出直传所需的全部元信息。
func loadTodoImageFiles(paths []string, absolute bool) ([]todoImageFile, error) {
	if len(paths) > 5 {
		return nil, fmt.Errorf("最多附加 5 张图片")
	}
	files := make([]todoImageFile, 0, len(paths))
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
		sum := sha256.Sum256(data)
		files = append(files, todoImageFile{
			Path: path, Name: filepath.Base(path), Mime: mime,
			Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Data: data,
		})
	}

	return files, nil
}

// loadTodoImages 兼容旧路径：把本机图片编码成 base64 data URL（仅 legacy 兼容期使用）。
func loadTodoImages(paths []string, absolute bool) ([]string, error) {
	files, err := loadTodoImageFiles(paths, absolute)
	if err != nil {
		return nil, err
	}

	return todoImagesBase64(files), nil
}

// todoImagesBase64 把已读取的文件编码成 images 参数（旧 base64 通道）。
func todoImagesBase64(files []todoImageFile) []string {
	images := make([]string, 0, len(files))
	for _, f := range files {
		images = append(images, "data:"+f.Mime+";base64,"+base64.StdEncoding.EncodeToString(f.Data))
	}

	return images
}

// qiniuUploadHosts 与 cloud 前端 media-direct-upload.js 一致：按区域选择上传入口。
var qiniuUploadHosts = map[string]string{
	"z0":  "https://upload.qiniup.com",
	"z1":  "https://upload-z1.qiniup.com",
	"z2":  "https://upload-z2.qiniup.com",
	"na0": "https://upload-na0.qiniup.com",
	"as0": "https://upload-as0.qiniup.com",
}

func qiniuUploadHost(region string) string {
	if host, ok := qiniuUploadHosts[region]; ok {
		return host
	}

	return qiniuUploadHosts["z0"]
}

// qiniuTicket 是 create_todo_image_tickets 返回的单张票据。
type qiniuTicket struct {
	UploadID  string `json:"upload_id"`
	Key       string `json:"key"`
	Region    string `json:"region"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	Business  string `json:"business"`
}

// requestTodoImageTickets 向 cloud 申请直传票据；票据数量必须与文件数一致，避免错位。
func requestTodoImageTickets(cloud, token, project string, files []todoImageFile) ([]qiniuTicket, error) {
	meta := make([]map[string]any, 0, len(files))
	for _, f := range files {
		meta = append(meta, map[string]any{
			"file_size": f.Size, "sha256": f.SHA256,
			"original_name": f.Name, "mime_type": f.Mime,
			"attachment_type": "image",
		})
	}
	data, err := mcpCall(cloud, token, "create_todo_image_tickets", map[string]any{
		"project": project, "files": meta,
	})
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(data["tickets"])
	var tickets []qiniuTicket
	if json.Unmarshal(raw, &tickets) != nil || len(tickets) != len(files) {
		return nil, fmt.Errorf("云端票据数量不符（需要 %d 张，得到 %d 张）", len(files), len(tickets))
	}

	return tickets, nil
}

// uploadTodoImageToQiniu 用票据把原始字节以 multipart 直传七牛；文件字节不经过 cloud。
func uploadTodoImageToQiniu(ticket qiniuTicket, file todoImageFile) error {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("token", ticket.Token); err != nil {
		return err
	}
	if err := writer.WriteField("key", ticket.Key); err != nil {
		return err
	}
	part, err := writer.CreateFormFile("file", file.Name)
	if err != nil {
		return err
	}
	if _, err := part.Write(file.Data); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, qiniuUploadHost(ticket.Region), &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("七牛上传失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))

		return fmt.Errorf("七牛上传失败（HTTP %d）：%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return nil
}

// directUploadTodoImages 走完「申请票据 → 逐张直传七牛 → 收集 upload_id」。
func directUploadTodoImages(cloud, token, project string, files []todoImageFile) ([]string, error) {
	tickets, err := requestTodoImageTickets(cloud, token, project, files)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(files))
	for i, ticket := range tickets {
		if err := uploadTodoImageToQiniu(ticket, files[i]); err != nil {
			return nil, err
		}
		if strings.TrimSpace(ticket.UploadID) == "" {
			return nil, fmt.Errorf("云端票据缺少 upload_id")
		}
		ids = append(ids, ticket.UploadID)
	}

	return ids, nil
}

// chooseTodoImageState 决定一次预览用直传还是旧 base64：
//
//	direct 模式：只直传，失败直接返回错误（不回退，避免掩盖真实故障）；
//	legacy 模式：直接旧路径；
//	auto（默认）：先直传，云端不支持或未配置七牛时回退旧路径（打印提示，不谎报成功）。
func chooseTodoImageState(endpoint, token, project string, files []todoImageFile) (todoDirectState, error) {
	mode := todoImageMode()
	if mode == todoImageModeLegacy {
		return todoDirectState{mode: todoImageModeLegacy}, nil
	}
	cloud := strings.TrimSuffix(endpoint, "/api/v1/mcp")
	ids, err := directUploadTodoImages(cloud, token, project, files)
	if err == nil {
		return todoDirectState{mode: todoImageModeDirect, uploadIDs: ids}, nil
	}
	if mode == todoImageModeDirect {
		return todoDirectState{}, err
	}
	fmt.Fprintf(os.Stderr, "提示：七牛直传失败，回退旧 base64 上传：%v\n", err)

	return todoDirectState{mode: todoImageModeLegacy}, nil
}

// todoDirectState 缓存一次 create_todo 两阶段之间的直传结果，避免预览与确认重复上传。
// 键为参数 + 图片内容指纹，不依赖 confirm_token（预览阶段还拿不到令牌）。
type todoDirectState struct {
	mode      string
	uploadIDs []string
	expires   time.Time
}

var (
	todoDirectStatesMu sync.Mutex
	todoDirectStates   = map[string]todoDirectState{}
)

const todoDirectStateTTL = 15 * time.Minute

func putTodoDirectState(key string, state todoDirectState) {
	todoDirectStatesMu.Lock()
	defer todoDirectStatesMu.Unlock()
	now := time.Now()
	for k, v := range todoDirectStates {
		if now.After(v.expires) {
			delete(todoDirectStates, k)
		}
	}
	state.expires = now.Add(todoDirectStateTTL)
	todoDirectStates[key] = state
}

func getTodoDirectState(key string) (todoDirectState, bool) {
	todoDirectStatesMu.Lock()
	defer todoDirectStatesMu.Unlock()
	state, ok := todoDirectStates[key]
	if !ok || time.Now().After(state.expires) {
		delete(todoDirectStates, key)

		return todoDirectState{}, false
	}

	return state, true
}

func deleteTodoDirectState(key string) {
	todoDirectStatesMu.Lock()
	defer todoDirectStatesMu.Unlock()
	delete(todoDirectStates, key)
}

// todoImageStateKey 只用会影响云端确认令牌的字段 + 图片内容指纹拼出稳定键，
// 使预览与确认两次调用命中同一份直传结果。
func todoImageStateKey(args map[string]json.RawMessage, files []todoImageFile) string {
	parts := map[string]string{}
	for k, v := range args {
		switch k {
		case "image_paths", "images", "image_upload_ids", "confirm_token":
			continue
		}
		parts[k] = string(v)
	}
	shas := make([]string, 0, len(files))
	for _, f := range files {
		shas = append(shas, f.SHA256)
	}
	raw, _ := json.Marshal(map[string]any{"args": parts, "sha": shas})
	sum := sha256.Sum256(raw)

	return hex.EncodeToString(sum[:])
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
		"description": "用户提供图片时请一并传入图片文件的绝对路径；最多 5 张，每张不超过 8 MiB（像素上限由云端校验），支持 PNG/JPEG/WebP/GIF。本地桥优先直传七牛（cloud 不中转），两次确认调用须使用相同图片。与 images 二选一。拿不到原图时明确告知用户，不能声称图片已上传。",
	}
	tool["description"] = fmt.Sprint(tool["description"]) + " 本机可用 image_paths 上传用户附图，不需要手工编码 base64。"
	encoded, _ := json.Marshal(tool)

	return encoded
}

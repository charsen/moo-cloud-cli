package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxTodoImageBytes = 8 * 1024 * 1024

// 待办图片上传模式：默认 direct（严格直传；auto 仅显式开启，legacy 仅兼容旧 cloud）。
const (
	todoImageModeAuto   = "auto"
	todoImageModeDirect = "direct"
	todoImageModeLegacy = "legacy"
)

// todoImageMode 从环境变量读取；未设置或非法值按 direct 处理（严格直传）。
func todoImageMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MOO_CLOUD_TODO_IMAGE_MODE"))) {
	case todoImageModeAuto:
		return todoImageModeAuto
	case todoImageModeDirect:
		return todoImageModeDirect
	case todoImageModeLegacy:
		return todoImageModeLegacy
	default:
		return todoImageModeDirect
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
	client := fixedEndpointClient(60 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("七牛上传失败：%w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("七牛上传失败（HTTP %d）：%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	// 七牛可能 HTTP 200 仍返回 error；必须解析响应，避免把失败当成功。
	var result struct {
		Key   string `json:"key"`
		Hash  string `json:"hash"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("七牛响应不是合法 JSON：%s", strings.TrimSpace(string(body)))
	}
	if result.Error != "" {
		return fmt.Errorf("七牛上传失败：%s", result.Error)
	}
	if result.Key == "" {
		return fmt.Errorf("七牛响应缺少 key")
	}
	if ticket.Key != "" && result.Key != ticket.Key {
		return fmt.Errorf("七牛返回 key 不一致：want %s got %s", ticket.Key, result.Key)
	}

	return nil
}

// directUploadTodoImages 走完「申请票据 → 逐张直传七牛 → 收集 upload_id」。
// 注意：七牛直传是逐张进行的，若第 N 张失败，前面已成功上传的对象没有回滚。
// 这些文件落在票据约定的 todo-attachments/temp/ 前缀下，由 cloud 侧七牛生命周期
// 规则按 TTL 回收（未绑定的临时对象不会永久占用空间），因此这里只需把已上传的 key
// 一并报出，便于人工排查，而不是假装整体成功或静默丢弃。
func directUploadTodoImages(cloud, token, project string, files []todoImageFile) ([]string, error) {
	tickets, err := requestTodoImageTickets(cloud, token, project, files)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(files))
	uploaded := make([]string, 0, len(files))
	for i, ticket := range tickets {
		if err := uploadTodoImageToQiniu(ticket, files[i]); err != nil {
			if len(uploaded) > 0 {
				return nil, fmt.Errorf("%w（本次已直传、将被七牛 TTL 回收的对象：%s）", err, strings.Join(uploaded, "、"))
			}
			return nil, err
		}
		if strings.TrimSpace(ticket.UploadID) == "" {
			return nil, fmt.Errorf("云端票据缺少 upload_id")
		}
		ids = append(ids, ticket.UploadID)
		if ticket.Key != "" {
			uploaded = append(uploaded, ticket.Key)
		}
	}

	return ids, nil
}

// setTodoImageArgs 只在用户确认项目后调用；auto 仅接受 Cloud 明确声明可回退的错误。
func setTodoImageArgs(cloud, token string, args map[string]any, files []todoImageFile) error {
	mode := todoImageMode()
	if mode != todoImageModeLegacy {
		ids, err := directUploadTodoImages(cloud, token, str(args["project"]), files)
		if err == nil {
			args["image_upload_ids"] = ids
			return nil
		}
		var toolErr *mcpToolError
		if mode != todoImageModeAuto || !errors.As(err, &toolErr) || toolErr.code != "todo_direct_upload_unavailable" {
			return err
		}
		fmt.Fprintln(os.Stderr, "提示：Cloud 当前未提供直传，改用 base64 上传")
	}
	args["images"] = todoImagesBase64(files)
	return nil
}

type todoImagePreview struct {
	token     string
	recovery  bool
	attempted bool
	expires   time.Time
}

var todoImagePreviews = struct {
	sync.Mutex
	byKey map[string]todoImagePreview
}{byKey: map[string]todoImagePreview{}}

// todoSubmissionKey 在重跑时保持稳定。只散列业务参数和有序图片内容，不包含路径或票据。
func todoSubmissionKey(args map[string]any, files []todoImageFile) string {
	shas := make([]string, 0, len(files))
	for _, f := range files {
		shas = append(shas, f.SHA256)
	}
	raw, _ := json.Marshal(map[string]any{"args": args, "sha": shas})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func todoPreviewToken(preview map[string]any) (string, error) {
	token := str(preview["confirm_token"])
	if preview["requires_confirmation"] != true || token == "" {
		return "", fmt.Errorf("云端创建待办响应缺少项目确认信息")
	}
	return token, nil
}

func todoSubmissionReceipt(preview map[string]any, imageCount int) (map[string]any, error) {
	raw, present := preview["submission_receipt"]
	if !present {
		return nil, nil
	}
	receipt, ok := raw.(map[string]any)
	if !ok || receipt == nil {
		return nil, fmt.Errorf("云端返回了无效的直传提交回执")
	}
	if err := verifyTodoImages(receipt, imageCount); err != nil {
		return nil, err
	}
	return receipt, nil
}

// submitTodoWithImages 仅在确认项目后传输文件，再取得绑定最终票据的新令牌。
// 已得到回执的请求无需传图；写入传输结果不明时，尝试只读查回执。
func submitTodoWithImages(cloud, token string, args map[string]any, files []todoImageFile, recovery bool, id json.RawMessage) (map[string]any, error) {
	if !recovery {
		delete(args, "submission_key")
	}
	lookup := maps.Clone(args)
	if err := setTodoImageArgs(cloud, token, args, files); err != nil {
		return nil, err
	}
	direct := args["image_upload_ids"] != nil
	if !direct {
		delete(args, "submission_key")
	}
	preview, err := mcpCallWithID(cloud, token, "create_todo", args, 3, id)
	if err != nil {
		return nil, err
	}
	if receipt, err := todoSubmissionReceipt(preview, len(files)); receipt != nil || err != nil {
		return receipt, err
	}
	confirm, err := todoPreviewToken(preview)
	if err != nil {
		return nil, err
	}

	args["confirm_token"] = confirm
	result, err := mcpCallWithID(cloud, token, "create_todo", args, 1, id)
	if err == nil {
		err = verifyTodoImages(result, len(files))
	}
	if err == nil {
		return result, nil
	}
	var toolErr *mcpToolError
	if direct && recovery && !errors.As(err, &toolErr) {
		if preview, lookupErr := mcpCallWithID(cloud, token, "create_todo", lookup, 1, id); lookupErr == nil {
			if receipt, receiptErr := todoSubmissionReceipt(preview, len(files)); receipt != nil && receiptErr == nil {
				return receipt, nil
			}
		}
		return nil, fmt.Errorf("直传提交结果尚未确认；24 小时内用相同参数和图片重试可查询或继续提交：%w", err)
	}
	return nil, err
}

// bridgeTodoImageCall 以 Cloud 的项目确认令牌绑定本机预览和图片指纹。
// 未收到绑定到该项目和文件的确认令牌前，桥只发无文件预览请求。
func bridgeTodoImageCall(cloud, token string, args map[string]any, files []todoImageFile, id json.RawMessage) (map[string]any, error) {
	confirm := args["confirm_token"]
	delete(args, "confirm_token")
	delete(args, "submission_key")
	key := todoSubmissionKey(args, files)
	args["submission_key"] = key
	// 令牌与地址参与本机缓存隔离，但不写入 Cloud 提交键。
	bridgeKey := sha256.Sum256([]byte(cloud + "\x00" + token + "\x00" + key))
	cacheKey := hex.EncodeToString(bridgeKey[:])
	if confirm == nil {
		preview, err := mcpCallWithID(cloud, token, "create_todo", args, 3, id)
		if err != nil {
			return nil, err
		}
		if receipt, err := todoSubmissionReceipt(preview, len(files)); receipt != nil || err != nil {
			return receipt, err
		}
		confirmToken, err := todoPreviewToken(preview)
		if err != nil {
			return nil, err
		}
		preview["images_requested"] = len(files)
		now := time.Now()
		todoImagePreviews.Lock()
		for k, state := range todoImagePreviews.byKey {
			if !now.Before(state.expires) {
				delete(todoImagePreviews.byKey, k)
			}
		}
		todoImagePreviews.byKey[cacheKey] = todoImagePreview{
			token: confirmToken, expires: now.Add(5 * time.Minute), recovery: preview["submission_recovery"] == true,
		}
		todoImagePreviews.Unlock()
		return preview, nil
	}

	todoImagePreviews.Lock()
	state, ok := todoImagePreviews.byKey[cacheKey]
	if !ok || !time.Now().Before(state.expires) || confirm != state.token {
		todoImagePreviews.Unlock()
		return nil, fmt.Errorf("本地桥未找到有效的项目及图片预览，请重新发起 create_todo（重新预览）")
	}
	attempted := state.attempted
	state.attempted = true
	todoImagePreviews.byKey[cacheKey] = state
	todoImagePreviews.Unlock()

	// 确认重发时，原请求可能已在后台完成。先查回执再决定是否申请新票据。
	if attempted && state.recovery {
		preview, err := mcpCallWithID(cloud, token, "create_todo", args, 1, id)
		if err != nil {
			return nil, fmt.Errorf("无法查询上次直传结果：%w", err)
		}
		if receipt, err := todoSubmissionReceipt(preview, len(files)); receipt != nil || err != nil {
			return receipt, err
		}
	}
	result, err := submitTodoWithImages(cloud, token, args, files, state.recovery, id)
	if err == nil {
		todoImagePreviews.Lock()
		delete(todoImagePreviews.byKey, cacheKey)
		todoImagePreviews.Unlock()
	}
	return result, err
}

// 有传图就必须拿到明确回执；不能把仅创建了文字的响应当作上传成功。
func verifyTodoImages(result map[string]any, requested int) error {
	if requested == 0 {
		return nil
	}
	todo, _ := result["todo"].(map[string]any)
	id, _ := todo["id"].(string)
	if count, ok := result["images_attached"].(float64); ok && count == float64(requested) && id != "" {
		return nil
	}

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
		"description": "用户提供图片时请一并传入图片文件的绝对路径；最多 5 张，每张不超过 8 MiB（像素上限由云端校验），支持 PNG/JPEG/WebP/GIF。两次确认调用须使用相同图片，确认项目后桥才上传文件。与 images 二选一。拿不到原图时明确告知用户，不能声称图片已上传。",
	}
	tool["description"] = fmt.Sprint(tool["description"]) + " 本机可用 image_paths 上传用户附图，不需要手工编码 base64。"
	encoded, _ := json.Marshal(tool)

	return encoded
}

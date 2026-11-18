package main

import (
	"fmt"
	"os"
	"strings"
)

// todoShowOptions 是 `todo show` 的参数：查看单条待办详情（含附件清单与正文引用核对）。
type todoShowOptions struct {
	project, id, token, cloud string
	json                      bool
}

func runTodoShow(args []string) int {
	opts, err := parseTodoShowOptions(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "参数错误："+err.Error())
		return 1
	}
	cloud, token, err := resolveCloud(opts.cloud, opts.token)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	data, err := mcpCall(cloud, token, "get_todo", map[string]any{"project": opts.project, "id": opts.id})
	if err != nil {
		fmt.Fprintln(os.Stderr, "查询待办失败："+err.Error())
		return 1
	}
	if opts.json {
		printJSONObject(data)
		return 0
	}
	todo, _ := data["todo"].(map[string]any)
	if todo == nil {
		fmt.Fprintln(os.Stderr, "云端未返回待办内容")
		return 1
	}

	title, _ := todo["title"].(string)
	status, _ := todo["status"].(string)
	priority, _ := todo["priority"].(string)
	category, _ := todo["category"].(string)
	fmt.Printf("[%v] %s\n", todo["id"], title)
	fmt.Printf("状态 %v · 优先级 %v · 分类 %v\n", status, priority, category)
	if name := assigneeLabel(todo); name != "" {
		fmt.Printf("领取人：%s\n", name)
	}
	if submitter, _ := todo["submitter"].(string); submitter != "" {
		fmt.Printf("提交人：%s\n", submitter)
	}
	if pageURL, _ := todo["page_url"].(string); pageURL != "" {
		fmt.Printf("页面：%s\n", pageURL)
	}

	description, _ := todo["description"].(string)
	if strings.TrimSpace(description) == "" {
		description, _ = todo["markdown"].(string)
	}
	if strings.TrimSpace(description) != "" {
		fmt.Println("\n--- 正文摘要 ---")
		fmt.Println(clipRunes(description, 1500))
	}

	attachments := anySlice(todo["attachments"])
	fmt.Printf("\n--- 附件（%d）---\n", len(attachments))
	if len(attachments) == 0 {
		fmt.Println("无附件。")
		if mentionsScreenshot(description) {
			fmt.Println("⚠ 正文提到截图/证据图，但该待办没有附件——请确认是否漏传。")
		}
	} else {
		for _, item := range attachments {
			att, _ := item.(map[string]any)
			fmt.Printf("- [%v] %v · %v · %v bytes · AI可读=%v\n",
				att["id"], att["type"], att["mime"], att["size_bytes"], att["ai_readable"])
		}
		if !mentionsScreenshot(description) {
			fmt.Println("提示：正文未提到附件；如需图文对应，可在正文补充对附件的说明。")
		}
	}

	if note, _ := todo["done_note"].(string); strings.TrimSpace(note) != "" {
		fmt.Printf("\n完成说明：%s\n", oneLine(note))
	}
	return 0
}

// mentionsScreenshot 粗判正文是否声称带有截图/证据图，用于提醒图文是否对得上。
func mentionsScreenshot(text string) bool {
	for _, kw := range []string{"截图", ".png", ".jpg", ".jpeg", "evidence"} {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func parseTodoShowOptions(args []string) (todoShowOptions, error) {
	opts := todoShowOptions{}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") {
			return opts, fmt.Errorf("请使用 --选项=值")
		}
		key, value, hasValue := strings.Cut(arg[2:], "=")
		if key == "json" {
			enabled, err := parseBoolFlag(key, value, hasValue)
			if err != nil {
				return opts, err
			}
			opts.json = enabled
			continue
		}
		if !hasValue || strings.TrimSpace(value) == "" {
			return opts, fmt.Errorf("--%s 需要非空值", key)
		}
		switch key {
		case "project":
			opts.project = value
		case "id":
			opts.id = value
		case "token":
			opts.token = value
		case "cloud":
			opts.cloud = value
		default:
			return opts, fmt.Errorf("未知参数 --%s", key)
		}
	}
	if strings.TrimSpace(opts.project) == "" {
		return opts, fmt.Errorf("--project 必填")
	}
	if !validTodoID(opts.id) {
		return opts, fmt.Errorf("--id 必须是有效的待办 ID（ULID）")
	}
	return opts, nil
}

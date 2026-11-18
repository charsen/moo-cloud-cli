package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// batchReport 是 `todo add --batch` 读取的测试报告文件：一次提交一轮测试的多条待办。
// project 可写在顶层（对本批所有条目生效），单条也可覆盖；images 为图片绝对路径。
type batchReport struct {
	Project string      `json:"project"`
	Items   []batchItem `json:"items"`
}

type batchItem struct {
	Project     string   `json:"project"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Priority    string   `json:"priority"`
	Assignee    string   `json:"assignee"`
	Images      []string `json:"images"`
}

// batchResult 汇总每条待办的创建结果，供脚本/AI 核对（不当作全部成功）。
type batchResult struct {
	Index       int    `json:"index"`
	Title       string `json:"title"`
	TodoID      string `json:"todo_id,omitempty"`
	Status      string `json:"status,omitempty"`
	Deduped     bool   `json:"deduped,omitempty"`
	ImagesAdded any    `json:"images_added,omitempty"`
	Attachments int    `json:"attachments,omitempty"`
	Error       string `json:"error,omitempty"`
}

// runTodoBatch 读取批量报告，逐条调用 addTodo；单条失败不打断后续，最终给出汇总。
// --dry-run 只校验与打印计划，不访问网络、不写云端。
func runTodoBatch(opts todoOptions) int {
	raw, err := os.ReadFile(opts.batch)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取批量报告失败："+err.Error())
		return 1
	}
	var report batchReport
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		fmt.Fprintln(os.Stderr, "批量报告不是合法 JSON 或含未知字段："+err.Error())
		return 1
	}
	// 末尾还有多余内容（如 {…} extra）视为非法，避免误把残片当合法报告静默执行。
	if err := decoder.Decode(new(any)); err != io.EOF {
		fmt.Fprintln(os.Stderr, "批量报告必须是单个 JSON 对象，末尾有多余内容")
		return 1
	}
	if len(report.Items) == 0 {
		fmt.Fprintln(os.Stderr, "批量报告 items 为空")
		return 1
	}

	// 先做静态校验（标题、分类、优先级、项目、图片路径），把明显错误挡在写云端之前。
	plans := make([]todoOptions, 0, len(report.Items))
	for i, item := range report.Items {
		project := strings.TrimSpace(item.Project)
		if project == "" {
			project = strings.TrimSpace(report.Project)
		}
		if project == "" {
			fmt.Fprintf(os.Stderr, "第 %d 条缺 project（顶层与条目均未提供）\n", i+1)
			return 1
		}
		if strings.TrimSpace(item.Title) == "" {
			fmt.Fprintf(os.Stderr, "第 %d 条缺 title\n", i+1)
			return 1
		}
		category := strings.TrimSpace(item.Category)
		if category == "" {
			category = opts.category
		}
		if category != "bug" && category != "task" {
			fmt.Fprintf(os.Stderr, "第 %d 条 category 只能是 bug / task（实际 %q）\n", i+1, category)
			return 1
		}
		priority := strings.TrimSpace(item.Priority)
		if priority == "" {
			priority = opts.priority
		}
		if priority != "low" && priority != "normal" && priority != "high" {
			fmt.Fprintf(os.Stderr, "第 %d 条 priority 只能是 low / normal / high（实际 %q）\n", i+1, priority)
			return 1
		}
		// 提前校验图片（路径存在、普通文件、≤5 张、≤8MiB、MIME 合法），
		// 把「路径写错/图太大」挡在写云端之前——批量场景尤其重要，否则一条坏图会让整轮白跑。
		if _, err := loadTodoImageFiles(item.Images, false); err != nil {
			fmt.Fprintf(os.Stderr, "第 %d 条图片不合法：%v\n", i+1, err)
			return 1
		}
		plans = append(plans, todoOptions{
			project: project, title: item.Title, description: item.Description,
			category: category, priority: priority, assignee: item.Assignee,
			images: item.Images, token: opts.token, cloud: opts.cloud, yes: opts.yes,
		})
	}

	// 解析一次 cloud/token，供 dry-run 之后统一使用（避免每条各自 initialToken 造成不一致）。
	cloud, token, err := resolveCloud(opts.cloud, opts.token)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	for i := range plans {
		plans[i].cloud, plans[i].token = cloud, token
	}

	if opts.dryRun {
		fmt.Printf("批量计划：%d 条（dry-run，已校验图片，未写云端）\n", len(plans))
		for i, p := range plans {
			fmt.Printf("  %d. [%s] %s · %s · %d 张图\n", i+1, p.project, p.priority, clipRunes(p.title, 60), len(p.images))
		}
		return 0
	}

	// 批量是写操作；非交互环境必须 --yes，否则一次性拒绝，不要逐条报「需要终端确认」刷屏。
	if !opts.yes {
		if !stdinIsTerminal() {
			fmt.Fprintln(os.Stderr, "批量建单需要在终端确认，或加 --yes（自动化/AI 必须显式加）")
			return 1
		}
	}

	results := make([]batchResult, 0, len(plans))
	failed := 0
	for i, p := range plans {
		res := batchResult{Index: i + 1, Title: p.title}
		created, err := addTodo(p)
		if err != nil {
			res.Error = err.Error()
			failed++
		} else {
			todo, _ := created["todo"].(map[string]any)
			res.TodoID, _ = todo["id"].(string)
			res.Status, _ = todo["status"].(string)
			res.Deduped, _ = created["deduped"].(bool)
			res.ImagesAdded = created["images_added"]
			// 有图时读回附件数，用于核对「图文是否对得上」，不写云端。
			if len(p.images) > 0 {
				res.Attachments = countTodoAttachments(p.cloud, p.token, p.project, res.TodoID)
			}
		}
		results = append(results, res)
		fmt.Fprintf(os.Stderr, "[%d/%d] %s\n", i+1, len(plans), p.title)
	}

	if opts.json {
		printJSONObject(map[string]any{
			"total": len(plans), "created": len(plans) - failed, "failed": failed, "results": results,
		})
	} else {
		fmt.Printf("\n批量完成：共 %d 条，成功 %d，失败 %d\n", len(plans), len(plans)-failed, failed)
		for _, r := range results {
			if r.Error != "" {
				fmt.Printf("  ✗ #%d %s：%s\n", r.Index, clipRunes(r.Title, 50), r.Error)
				continue
			}
			extra := ""
			if r.Deduped {
				extra += " · 去重复用"
			}
			if r.Attachments > 0 {
				extra += fmt.Sprintf(" · 附件 %d 张", r.Attachments)
			}
			fmt.Printf("  ✓ #%d %s · %s%s\n", r.Index, clipRunes(r.Title, 50), r.TodoID, extra)
		}
	}

	if failed > 0 {
		return 1
	}
	return 0
}

// countTodoAttachments 通过 get_todo 读回附件数；失败返回 0（只影响核对提示，不影响建单结果）。
// cloud/token 已由 runTodoBatch 统一解析后注入，这里直接使用，不再重复 initialToken。
func countTodoAttachments(cloud, token, project, id string) int {
	if id == "" || strings.TrimSpace(token) == "" {
		return 0
	}
	base := strings.TrimSpace(cloud)
	if base == "" {
		base = "https://c.mooeen.com"
	}
	data, err := mcpCall(base, token, "get_todo", map[string]any{"project": project, "id": id})
	if err != nil {
		return 0
	}
	todo, ok := data["todo"].(map[string]any)
	if !ok {
		return 0
	}
	if n, ok := todo["attachments_count"].(float64); ok {
		return int(n)
	}
	return len(anySlice(todo["attachments"]))
}

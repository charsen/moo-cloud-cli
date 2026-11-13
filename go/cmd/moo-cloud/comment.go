package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type commentOptions struct {
	project, typ, key, id, body, token, cloud string
	limit, offset                             string
	json, yes                                 bool
}

func runComment(args []string) int {
	if len(args) == 0 || contains(args, "--help") || contains(args, "-h") || args[0] == "help" {
		fmt.Print(`用法：moo-cloud comment list|add --project=<slug> --type=<类型> [选项]

  --type=runtime|slow_query|frontend_error|app_error|todo
  --key=<12位key>     异常用 key；TODO 不传
  --id=<TODO ID>      type=todo 时用 id；异常不传
  --body=<正文>       add 必填，1–5000 字，仅文字
  --limit=20 --offset=0  list 分页，limit 为 1–50
  --yes               add 跳过终端确认，仍受云端写权限和策略限制
  --json              输出 JSON
  --token=<token> --cloud=<url>  沿用既有默认值

例：
  moo-cloud comment list --project=my-project --type=runtime --key=abcdef123456
  moo-cloud comment add --project=my-project --type=todo --id=<TODO-ID> --body="已补充复现步骤"
`)
		return 0
	}
	action := args[0]
	if action != "list" && action != "add" {
		fmt.Fprintln(os.Stderr, "comment 仅支持 list 或 add")
		return 1
	}
	opts, err := parseCommentOptions(action, args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "参数错误："+err.Error())
		return 1
	}
	data, err := callComment(action, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "评论操作失败："+err.Error())
		return 1
	}
	if opts.json {
		raw, _ := json.MarshalIndent(data, "", "  ")
		fmt.Println(string(raw))
		return 0
	}
	if action == "add" {
		comment, _ := data["comment"].(map[string]any)
		fmt.Printf("评论已保存：%v · 项目 %v\n", comment["id"], data["project"])
		if data["deduped"] == true {
			fmt.Println("复用 2 分钟内已发表的相同评论，未重复通知。")
		}
		return 0
	}
	for _, item := range anySlice(data["comments"]) {
		comment, _ := item.(map[string]any)
		author, _ := comment["author"].(map[string]any)
		fmt.Printf("[%v] %v · %v\n%v\n\n", comment["id"], author["name"], comment["created_at"], comment["body"])
		if count, _ := comment["attachments_count"].(float64); count > 0 {
			fmt.Printf("附图 %v 张，可在 cloud 讨论中查看。\n", count)
		}
	}
	fmt.Printf("本页评论：%v 条\n", data["count"])
	if data["has_more"] == true {
		fmt.Printf("下一页：--offset=%v\n", data["next_offset"])
	}
	return 0
}

func parseCommentOptions(action string, args []string) (commentOptions, error) {
	opts := commentOptions{limit: "20", offset: "0"}
	values := map[string]*string{"project": &opts.project, "type": &opts.typ, "key": &opts.key, "id": &opts.id, "token": &opts.token, "cloud": &opts.cloud}
	if action == "add" {
		values["body"] = &opts.body
	} else {
		values["limit"], values["offset"] = &opts.limit, &opts.offset
	}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") {
			return opts, fmt.Errorf("请使用 --选项=值")
		}
		key, value, hasValue := strings.Cut(arg[2:], "=")
		if key == "json" || (key == "yes" && action == "add") {
			enabled, err := parseBoolFlag(key, value, hasValue)
			if err != nil {
				return opts, err
			}
			if key == "json" {
				opts.json = enabled
			} else {
				opts.yes = enabled
			}
			continue
		}
		target, ok := values[key]
		if !ok {
			return opts, fmt.Errorf("当前评论操作不支持 --%s", key)
		}
		if !hasValue || strings.TrimSpace(value) == "" {
			return opts, fmt.Errorf("--%s 需要非空值", key)
		}
		*target = value
	}
	if strings.TrimSpace(opts.project) == "" {
		return opts, fmt.Errorf("--project 必填")
	}
	if opts.typ == "todo" {
		validID, _ := regexp.MatchString(`^[0-9A-Za-z]{20,32}$`, opts.id)
		if !validID || opts.key != "" {
			return opts, fmt.Errorf("TODO 请只传有效的 --id，不传 --key")
		}
	} else if !isIssueType(opts.typ) || !isIssueKey(opts.key) || opts.id != "" {
		return opts, fmt.Errorf("异常需要有效的 --type 和 12 位十六进制 --key，不传 --id")
	}
	if action == "add" && (strings.TrimSpace(opts.body) == "" || utf8.RuneCountInString(strings.TrimSpace(opts.body)) > 5000) {
		return opts, fmt.Errorf("--body 需要 1–5000 字的评论正文")
	}
	if action == "list" {
		limit, limitErr := strconv.Atoi(opts.limit)
		offset, offsetErr := strconv.Atoi(opts.offset)
		if limitErr != nil || limit < 1 || limit > 50 || offsetErr != nil || offset < 0 {
			return opts, fmt.Errorf("--limit 需为 1–50，--offset 需为非负整数")
		}
	}
	return opts, nil
}

func callComment(action string, opts commentOptions) (map[string]any, error) {
	token := opts.token
	if token == "" {
		token = initialToken()
	}
	if token == "" {
		return nil, fmt.Errorf("缺少账号级 token")
	}
	cloud, err := validateCloudURL(opts.cloud)
	if err != nil {
		return nil, err
	}
	if cloud == "" {
		cloud, err = validateCloudURL(os.Getenv("MOO_CLOUD_URL"))
		if err != nil {
			return nil, err
		}
	}
	if cloud == "" {
		cloud = "https://c.mooeen.com"
	}
	args := map[string]any{"project": opts.project, "type": opts.typ}
	if opts.typ == "todo" {
		args["id"] = opts.id
	} else {
		args["key"] = strings.ToLower(opts.key)
	}
	tool := "list_comments"
	if action == "add" {
		if !opts.yes {
			info, err := os.Stdin.Stat()
			if err != nil || info.Mode()&os.ModeCharDevice == 0 {
				return nil, fmt.Errorf("发表评论需要终端确认；自动化调用需先取得用户同意再加 --yes")
			}
			fmt.Fprintf(os.Stderr, "将向项目「%s」的 %s %s%s 发表评论（%d 字）\n确认？[y/N] ", opts.project, opts.typ, opts.key, opts.id, utf8.RuneCountInString(strings.TrimSpace(opts.body)))
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if reply := strings.ToLower(strings.TrimSpace(line)); reply != "y" && reply != "yes" && reply != "是" {
				return nil, fmt.Errorf("已取消")
			}
		}
		tool, args["body"] = "post_comment", strings.TrimSpace(opts.body)
	} else {
		args["limit"], _ = strconv.Atoi(opts.limit)
		args["offset"], _ = strconv.Atoi(opts.offset)
	}
	var data map[string]any
	if tool == "post_comment" {
		// 发表评论非幂等，网络错误不自动重试；服务端有 2 分钟去重兜底。
		data, err = mcpCallNoRetry(cloud, token, tool, args)
	} else {
		data, err = mcpCall(cloud, token, tool, args)
	}
	if err != nil {
		return nil, err
	}
	if action == "add" {
		comment, _ := data["comment"].(map[string]any)
		if id, _ := comment["id"].(string); id == "" {
			return nil, fmt.Errorf("云端缺少评论保存回执，请查询讨论确认后再重试")
		}
	} else {
		// 缺少列表不能当作「没有评论」，避免连接/协议异常被悄悄显示成空讨论。
		comments, validComments := data["comments"].([]any)
		count, validCount := data["count"].(float64)
		if !validComments || !validCount || count != float64(len(comments)) {
			return nil, fmt.Errorf("云端评论列表响应不完整，不能视为没有评论")
		}
	}
	return data, nil
}

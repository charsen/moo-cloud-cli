package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 错误只描述字段、类型或位置，不回显配置正文，避免误填的 Token 进入报告。
func decodeMarker(raw []byte) (markerFile, []string, error) {
	var data markerFile
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		if syntax, ok := err.(*json.SyntaxError); ok {
			line := 1 + strings.Count(string(raw[:syntax.Offset]), "\n")
			return data, nil, fmt.Errorf(".moo-cloud.json JSON 格式错误：第 %d 行附近，请检查逗号、引号与括号", line)
		}
		return data, nil, fmt.Errorf(".moo-cloud.json 顶层必须是 JSON 对象")
	}
	if fields == nil {
		return data, nil, fmt.Errorf(".moo-cloud.json 顶层必须是 JSON 对象，不能是 null")
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		if field, ok := err.(*json.UnmarshalTypeError); ok {
			return data, nil, fmt.Errorf(".moo-cloud.json 字段 %s 类型错误，请按 README 中的字符串、数组或对象示例填写", field.Field)
		}
		return data, nil, fmt.Errorf(".moo-cloud.json 字段格式错误，请核对 README 示例")
	}
	if data.Role != "" && data.Role != "app" && data.Role != "backend" {
		return data, nil, fmt.Errorf("字段 role 无效：软件仓库用 app，后端仓库用 backend")
	}
	// 缺省 role 的既有后端配置仍按原约定处理，不增加新的必填字段。
	allowed := []string{"role", "root", "deployments"}
	if data.Role == "app" {
		allowed = []string{"role", "project", "product", "platforms", "verify"}
	}
	warnings := markerFieldWarnings(fields, allowed, "")
	if data.Role == "app" {
		platforms := map[string]bool{"android": true, "ios": true, "macos": true, "windows": true, "linux": true, "ohos": true, "web": true}
		for i, platform := range data.Platforms {
			if !platforms[platform] {
				return data, warnings, fmt.Errorf("字段 platforms[%d] 无效；可用 android、ios、macos、windows、linux、ohos、web", i)
			}
		}
		var rules []map[string]json.RawMessage
		_ = json.Unmarshal(fields["verify"], &rules)
		for i, rule := range rules {
			warnings = append(warnings, markerFieldWarnings(rule, []string{"file", "contains"}, fmt.Sprintf("verify[%d].", i))...)
		}
	} else {
		var deployments []map[string]json.RawMessage
		_ = json.Unmarshal(fields["deployments"], &deployments)
		for i, deployment := range deployments {
			warnings = append(warnings, markerFieldWarnings(deployment, []string{"project", "env_file"}, fmt.Sprintf("deployments[%d].", i))...)
		}
	}
	return data, warnings, nil
}

func markerFieldWarnings(fields map[string]json.RawMessage, allowed []string, prefix string) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sortStrings(keys)
	warnings := []string{}
	for _, key := range keys {
		known := false
		for _, field := range allowed {
			if strings.EqualFold(key, field) {
				known = true
				break
			}
		}
		if !known {
			warnings = append(warnings, fmt.Sprintf("未识别字段 %s%q，请检查拼写；可用字段：%s", prefix, key, strings.Join(allowed, "、")))
		}
	}
	return warnings
}

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func paginationServer(t *testing.T, page func(int) map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Arguments struct {
					Offset int `json:"offset"`
					Limit  int `json:"limit"`
				} `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if req.Params.Arguments.Limit != 50 {
			t.Errorf("scan page limit=%d", req.Params.Arguments.Limit)
		}
		data, _ := json.Marshal(page(req.Params.Arguments.Offset))
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": string(data)}}, "isError": false}})
	}))
}

func TestFilteredPaginationFindsProductionIssueAfterTenDebugPages(t *testing.T) {
	calls := 0
	server := paginationServer(t, func(offset int) map[string]any {
		calls++
		env := "debug"
		if offset >= 10 {
			env = "production"
		}
		return map[string]any{"issues": []any{map[string]any{"key": "target", "env": env}}, "has_more": true, "next_offset": offset + 1}
	})
	defer server.Close()
	result, err := fetchIssuesPaged(server.URL, "fake", "p", "app_error", 1)
	if err != nil {
		t.Fatal(err)
	}
	rows := anySlice(result["issues"])
	if calls != 11 || len(rows) != 1 || rows[0].(map[string]any)["env"] != "production" {
		t.Fatalf("calls=%d result=%+v", calls, result)
	}
	if result["has_more"] != true || result["next_offset"] != 11 {
		t.Fatalf("remaining data hidden: %+v", result)
	}
}

func TestFilteredPaginationCursorKeepsUnconsumedRowsInLastPage(t *testing.T) {
	rows := []any{map[string]any{"key": "dev", "env": "development"}, map[string]any{"key": "first", "env": "production"}, map[string]any{"key": "second", "env": "production"}}
	server := paginationServer(t, func(offset int) map[string]any {
		return map[string]any{"issues": rows[offset:], "has_more": false, "next_offset": nil}
	})
	defer server.Close()
	result, err := fetchIssuesPaged(server.URL, "fake", "p", "frontend_error", 1)
	if err != nil {
		t.Fatal(err)
	}
	if result["count"] != 1 || result["has_more"] != true || result["next_offset"] != 2 {
		t.Fatalf("wrong continuation: %+v", result)
	}
	next, err := mcpCall(server.URL, "fake", "list_issues", map[string]any{"project": "p", "type": "frontend_error", "limit": 50, "offset": result["next_offset"]})
	if err != nil {
		t.Fatal(err)
	}
	if remaining := anySlice(next["issues"]); len(remaining) != 1 || remaining[0].(map[string]any)["key"] != "second" {
		t.Fatalf("skipped valid row: %+v", next)
	}
	exhausted, err := fetchIssuesPaged(server.URL, "fake", "p", "frontend_error", 2)
	if err != nil {
		t.Fatal(err)
	}
	if exhausted["count"] != 2 || exhausted["has_more"] != false || exhausted["next_offset"] != nil {
		t.Fatalf("wrong exhausted result: %+v", exhausted)
	}
}

func TestFilteredPaginationUsesServerNextOffsetAndLegacyFallback(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		offsets := []int{}
		server := paginationServer(t, func(offset int) map[string]any {
			offsets = append(offsets, offset)
			if offset == 0 {
				result := map[string]any{"issues": []any{map[string]any{"env": "verify"}}, "has_more": true}
				if explicit {
					result["next_offset"] = 3
				}
				return result
			}
			return map[string]any{"issues": []any{map[string]any{"env": "production"}}, "has_more": false}
		})
		result, err := fetchIssuesPaged(server.URL, "fake", "p", "app_error", 1)
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if explicit {
			want = 3
		}
		if len(offsets) != 2 || offsets[1] != want || result["has_more"] != false || result["next_offset"] != nil {
			t.Fatalf("offsets=%v result=%+v", offsets, result)
		}
	}
}

func TestFilteredPaginationRejectsInvalidOrOverlappingNextOffsets(t *testing.T) {
	for _, cursor := range []any{nil, 0, -1, 0.5, 1, "2", []any{}} {
		calls := 0
		server := paginationServer(t, func(offset int) map[string]any {
			calls++
			return map[string]any{"issues": []any{map[string]any{"env": "debug"}, map[string]any{"env": "verify"}}, "has_more": true, "next_offset": cursor}
		})
		_, err := fetchIssuesPaged(server.URL, "fake", "p", "app_error", 1)
		server.Close()
		if err == nil || calls != 1 {
			t.Fatalf("cursor=%v calls=%d err=%v", cursor, calls, err)
		}
	}
}

func TestFilteredPaginationDistinguishesExhaustionFromBrokenEmptyPage(t *testing.T) {
	for _, more := range []bool{false, true} {
		server := paginationServer(t, func(offset int) map[string]any { return map[string]any{"issues": []any{}, "has_more": more} })
		result, err := fetchIssuesPaged(server.URL, "fake", "p", "app_error", 1)
		server.Close()
		if (err != nil) != more {
			t.Fatalf("more=%v err=%v", more, err)
		}
		if !more && (result["has_more"] != false || result["next_offset"] != nil || result["count"] != 0) {
			t.Fatalf("empty result=%+v", result)
		}
	}
}

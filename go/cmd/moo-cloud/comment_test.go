package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const commentTestTodoID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

func TestCommentOptionsRejectWrongTargetAndInvalidValues(t *testing.T) {
	base := []string{"--project=p", "--type=runtime", "--key=ABCDEF123456"}
	for _, flag := range []string{"--id=" + commentTestTodoID, "--type=unknown", "--key=bad", "--offset=-1", "--limit=0", "--limit=51", "--json=invalid", "--cloud", "--unknown=fixture", "body=fixture"} {
		if _, err := parseCommentOptions("list", append(append([]string{}, base...), flag)); err == nil {
			t.Fatal("invalid comment list option accepted")
		}
	}
	for _, flags := range [][]string{{}, {"--body="}, {"--body=" + strings.Repeat("中", 5001)}, {"--body=x", "--yes=invalid"}, {"--body=x", "--limit=1"}} {
		if _, err := parseCommentOptions("add", append(append([]string{}, base...), flags...)); err == nil {
			t.Fatal("invalid comment add option accepted")
		}
	}
	if _, err := parseCommentOptions("list", []string{"--project=p", "--type=todo", "--id=bad"}); err == nil {
		t.Fatal("invalid TODO ID accepted")
	}
	opts, err := parseCommentOptions("add", append(base, "--body=补充", "--yes=0"))
	if err != nil || opts.yes {
		t.Fatal("--yes=0 must preserve confirmation", err)
	}
}

func TestCommentCallsPreserveTargetsPaginationAndWriteReceipts(t *testing.T) {
	for _, typ := range []string{"runtime", "slow_query", "frontend_error", "app_error", "todo"} {
		for _, action := range []string{"list", "add"} {
			t.Run(typ+action, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					var req struct {
						Params struct {
							Name      string         `json:"name"`
							Arguments map[string]any `json:"arguments"`
						} `json:"params"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					args := req.Params.Arguments
					if r.Header.Get("Authorization") != "Bearer fixture" || args["project"] != "p" || args["type"] != typ {
						t.Error("comment authentication or target lost")
					}
					if typ == "todo" {
						if args["id"] != commentTestTodoID || args["key"] != nil {
							t.Error("TODO identifier confused with issue key")
						}
					} else if args["key"] != "abcdef123456" || args["id"] != nil {
						t.Error("issue key not normalized or mixed with TODO ID")
					}
					data := map[string]any{"project": "p", "type": typ}
					if action == "add" {
						if req.Params.Name != "post_comment" || args["body"] != "复现补充" {
							t.Error("comment post payload lost")
						}
						data["comment"] = map[string]any{"id": "123", "body": "复现补充"}
						data["deduped"] = true
					} else {
						if req.Params.Name != "list_comments" || args["offset"] != float64(3) || args["limit"] != float64(2) {
							t.Error("comment pagination lost")
						}
						data["comments"], data["count"] = []any{}, 0
					}
					todoImageReply(w, 1, data, false)
				}))
				defer server.Close()
				flags := []string{"--project=p", "--type=" + typ, "--token=fixture", "--cloud=" + server.URL + "/"}
				if typ == "todo" {
					flags = append(flags, "--id="+commentTestTodoID)
				} else {
					flags = append(flags, "--key=ABCDEF123456")
				}
				if action == "add" {
					flags = append(flags, "--body=复现补充", "--yes")
				} else {
					flags = append(flags, "--limit=2", "--offset=3")
				}
				opts, err := parseCommentOptions(action, flags)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := callComment(action, opts); err != nil || calls != 1 {
					t.Fatal("comment operation failed or duplicated request", err)
				}
			})
		}
	}
}

func TestCommentWriteRequiresConfirmationAndReceipt(t *testing.T) {
	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() { os.Stdin = original; _ = stdin.Close() })
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		todoImageReply(w, 1, map[string]any{"comment": map[string]any{}}, false)
	}))
	defer server.Close()
	opts, err := parseCommentOptions("add", []string{"--project=p", "--type=runtime", "--key=abcdef123456", "--body=补充", "--token=fixture", "--cloud=" + server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := callComment("add", opts); err == nil || calls != 0 {
		t.Fatal("unconfirmed comment was sent")
	}
	opts.yes = true
	if _, err := callComment("add", opts); err == nil || calls != 1 {
		t.Fatal("missing comment receipt was reported as success")
	}
}

func TestCommentMcpBridgePassesToolsAndKeepsWritesOutOfApprovalList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 7, "result": map[string]any{"tools": []any{
			map[string]any{"name": "list_comments", "inputSchema": map[string]any{"type": "object"}},
			map[string]any{"name": "post_comment", "inputSchema": map[string]any{"type": "object"}},
		}}})
	}))
	defer server.Close()
	var output bytes.Buffer
	writer := bufio.NewWriter(&output)
	if ok, err := handleMcpLine(server.URL, "fixture", `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`, writer, newCLIUpdater()); !ok {
		t.Fatal(err)
	}
	_ = writer.Flush()
	if !strings.Contains(output.String(), "list_comments") || !strings.Contains(output.String(), "post_comment") {
		t.Fatal("comment tools not exposed by bridge")
	}
	block := codexBlock("/fixture/moo-cloud")
	if !strings.Contains(block, "tools.list_comments]") || strings.Contains(block, "tools.post_comment]") {
		t.Fatal("comment approval defaults allow writes or omit reads")
	}
}

func TestCommentListDoesNotTreatMalformedResponsesAsEmptyDiscussion(t *testing.T) {
	for _, data := range []map[string]any{{}, {"comments": nil, "count": 0}, {"comments": []any{}, "count": 1}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { todoImageReply(w, 1, data, false) }))
		opts, err := parseCommentOptions("list", []string{"--project=p", "--type=runtime", "--key=abcdef123456", "--token=fixture", "--cloud=" + server.URL})
		if err != nil {
			t.Fatal(err)
		}
		_, err = callComment("list", opts)
		server.Close()
		if err == nil {
			t.Fatal("malformed comment response was reported as an empty discussion")
		}
	}
}

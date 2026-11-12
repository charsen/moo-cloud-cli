package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalUpdateRejectsNonIntegerRequestIDBeforeAnyIO(t *testing.T) {
	for _, id := range []string{"1.5", "1e3", "9223372036854775808", "true", "[]", "{}", "null"} {
		t.Run(id, func(t *testing.T) {
			f := newUpdateFixture(t)
			var output bytes.Buffer
			writer := bufio.NewWriter(&output)
			request := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":"update_cli","arguments":{"target_version":"0.1.2","confirm":true}}}`, id)
			ok, _ := handleMcpLine("http://unused.test", "fixture", request, writer, f.u)
			_ = writer.Flush()
			if ok || len(f.requests) != 0 || string(mustRead(t, f.path)) != "0.1.1" {
				t.Fatal("invalid request ID triggered an update or network activity")
			}
			var response struct {
				ID    any                 `json:"id"`
				Error *struct{ Code int } `json:"error"`
			}
			if err := json.Unmarshal(output.Bytes(), &response); err != nil || response.ID != nil || response.Error == nil || response.Error.Code != -32600 {
				t.Fatal("invalid request ID must return a protocol error with null ID", err)
			}
		})
	}
}

func TestImageBridgeRejectsNonIntegerRequestIDBeforeReadingFiles(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	for _, id := range []string{"1.5", "1e3", "9223372036854775808", "null"} {
		var output bytes.Buffer
		writer := bufio.NewWriter(&output)
		request := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":"create_todo","arguments":{"image_paths":["/missing-fixture.png"]}}}`, id)
		ok, _ := handleMcpLine(server.URL, "fixture", request, writer, newCLIUpdater())
		_ = writer.Flush()
		var response struct {
			Error *struct{ Code int } `json:"error"`
		}
		if err := json.Unmarshal(output.Bytes(), &response); err != nil || ok || response.Error == nil || response.Error.Code != -32600 {
			t.Fatal("invalid image request reached file handling instead of protocol validation", err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid image request was forwarded")
	}
}

func TestLocalToolsPreserveValidStringAndIntegerIDs(t *testing.T) {
	for _, id := range []string{`"request-1"`, `""`, "0", "-1", "9223372036854775807", "-9223372036854775808"} {
		f := newUpdateFixture(t)
		var output bytes.Buffer
		writer := bufio.NewWriter(&output)
		request := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":"check_cli_update","arguments":{}}}`, id)
		ok, errMsg := handleMcpLine("http://unused.test", "fixture", request, writer, f.u)
		_ = writer.Flush()
		var response struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(output.Bytes(), &response); err != nil || !ok || string(response.ID) != id {
			t.Fatal("valid request ID changed or was rejected", err, errMsg)
		}
	}
}

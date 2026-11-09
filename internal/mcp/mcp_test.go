package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeAPI struct{ calls []string }

func (f *fakeAPI) Call(method string, params any) (json.RawMessage, error) {
	f.calls = append(f.calls, method)
	if method == "doctor" {
		return nil, errors.New("boom")
	}
	return json.RawMessage(`{"ok":true}`), nil
}

func roundtrip(t *testing.T, s *Server, msgs ...string) []map[string]any {
	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(strings.Join(msgs, "\n")), &out); err != nil {
		t.Fatal(err)
	}
	var res []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad JSON from server: %s", line)
		}
		res = append(res, m)
	}
	return res
}

func TestHandshakeAndToolList(t *testing.T) {
	s := &Server{API: &fakeAPI{}, Version: "test"}
	res := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if len(res) != 2 {
		t.Fatalf("want 2 responses (notification gets none), got %d", len(res))
	}
	if v := res[0]["result"].(map[string]any)["protocolVersion"]; v != "2025-03-26" {
		t.Fatalf("version negotiation: %v", v)
	}
	names := map[string]bool{}
	for _, tl := range res[1]["result"].(map[string]any)["tools"].([]any) {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	if !names["sites_list"] || !names["sites_update"] || !names["doctor"] {
		t.Fatalf("missing tools: %v", names)
	}
	if names["db_credentials"] || names["import_database"] {
		t.Fatal("secret tools must be hidden without --allow-secrets")
	}
}

func TestDestructiveGoesToApproval(t *testing.T) {
	api := &fakeAPI{}
	s := &Server{API: api}
	res := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"stop","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"stop","arguments":{"confirm":true}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"sites_update","arguments":{"folder":"x","rm":"-rf"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"doctor","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"approvals_decide","arguments":{"id":"x","approve":true}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/list"}`)
	// Destructive calls are passed to the service, which holds them until the person approves in the app
	if res[0]["result"].(map[string]any)["isError"] != false || res[1]["result"].(map[string]any)["isError"] != false {
		t.Fatal("stop should reach the service (which asks for approval)")
	}
	if res[2]["result"].(map[string]any)["isError"] != true {
		t.Fatal("unknown arguments must be refused")
	}
	if res[3]["result"].(map[string]any)["isError"] != true {
		t.Fatal("API errors must come back as tool errors")
	}
	if res[4]["result"].(map[string]any)["isError"] != true {
		t.Fatal("agents must not be able to approve their own requests")
	}
	for _, tl := range res[5]["result"].(map[string]any)["tools"].([]any) {
		if tl.(map[string]any)["name"] == "approvals_decide" {
			t.Fatal("approvals_decide must not be offered to agents")
		}
	}
	if strings.Join(api.calls, ",") != "stop,stop,doctor" {
		t.Fatalf("unexpected API calls: %v", api.calls)
	}
}

func TestSecretsOptIn(t *testing.T) {
	s := &Server{API: &fakeAPI{}, AllowSecrets: true}
	res := roundtrip(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	found := false
	for _, tl := range res[0]["result"].(map[string]any)["tools"].([]any) {
		if tl.(map[string]any)["name"] == "db_credentials" {
			found = true
		}
	}
	if !found {
		t.Fatal("db_credentials should be listed with AllowSecrets")
	}
}

type sitesAPI struct{ fakeAPI }

func (f *sitesAPI) Call(method string, params any) (json.RawMessage, error) {
	f.calls = append(f.calls, method)
	switch method {
	case "sites.list":
		return json.RawMessage(`[{"folder":"shop","name":"shop","enabled":true,"runtime":"php"},{"folder":"work/dash","name":"dash","enabled":false,"runtime":"app"}]`), nil
	case "sites.context":
		b, _ := json.Marshal(params)
		return json.RawMessage(`{"asked":` + string(b) + `}`), nil
	case "logs":
		return json.RawMessage(`{"text":"PHP Fatal error: boom"}`), nil
	}
	return json.RawMessage(`{}`), nil
}

func TestResourcesAndPrompts(t *testing.T) {
	s := &Server{API: &sitesAPI{}}
	res := roundtrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"localsites://projects/work%2Fdash"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"localsites://logs/shop/errors"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"file:///etc/passwd"}}`,
		`{"jsonrpc":"2.0","id":5,"method":"prompts/get","params":{"name":"diagnose_site","arguments":{"folder":"shop"}}}`)
	list := res[0]["result"].(map[string]any)["resources"].([]any)
	if len(list) != 3 { // 2 summaries + 1 log (only the running project)
		t.Fatalf("resources: %v", list)
	}
	if txt := res[1]["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)["text"].(string); !strings.Contains(txt, `"folder":"work/dash"`) {
		t.Fatalf("summary for a folder with a slash: %s", txt)
	}
	if txt := res[2]["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)["text"].(string); txt != "PHP Fatal error: boom" {
		t.Fatalf("log: %s", txt)
	}
	if res[3]["error"] == nil {
		t.Fatal("non-LocalSites URIs must be refused")
	}
	msg := res[4]["result"].(map[string]any)["messages"].([]any)[0].(map[string]any)["content"].(map[string]any)["text"].(string)
	if !strings.Contains(msg, "sites_check") || !strings.Contains(msg, "localsites://projects/shop") {
		t.Fatalf("prompt: %s", msg)
	}
}

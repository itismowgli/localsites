// Package mcp exposes LocalSites to AI agents over the Model Context Protocol (stdio transport).
//
// Every API method becomes a tool with a JSON schema and safety annotations:
//   - read-only tools run freely
//   - destructive tools (stop, restart, quit) require "confirm": true, which agents should
//     only send after the user approved the action
//   - tools that take or return secrets are hidden unless the server runs with --allow-secrets
package mcp

import (
	"bufio"
	"encoding/json"
	"io"
	"net/url"
	"strings"

	"github.com/localsites/localsites/internal/api"
)

var supportedVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

type Caller interface {
	Call(method string, params any) (json.RawMessage, error)
}

type Server struct {
	API          Caller
	AllowSecrets bool
	Version      string
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve reads newline-delimited JSON-RPC from r and writes responses to w until r closes.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	enc := json.NewEncoder(w)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = enc.Encode(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
			continue
		}
		if len(req.ID) == 0 { // notification (e.g. notifications/initialized): no reply
			continue
		}
		res, rerr := s.handle(req)
		out := response{JSONRPC: "2.0", ID: req.ID}
		if rerr != nil {
			out.Error = rerr
		} else {
			out.Result = res
		}
		if err := enc.Encode(out); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (s *Server) handle(req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := supportedVersions[0]
		for _, v := range supportedVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}, "resources": map[string]any{"listChanged": false}, "prompts": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "localsites", "title": "LocalSites", "version": s.Version},
			"instructions": "LocalSites runs local PHP/MySQL projects at https://<name>.test. Start with sites_list and status; " +
				"use doctor or sites_check to diagnose problems and logs to read errors. Tools marked destructive wait for the user " +
				"to click Approve in the LocalSites app (up to 90 seconds); tell the user to look at the app. Never copy or change databases unless the user asked.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.tools()}, nil
	case "resources/list":
		return s.resources()
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(req.Params, &p)
		return s.readResource(p.URI)
	case "prompts/list":
		return map[string]any{"prompts": prompts}, nil
	case "prompts/get":
		var p struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		return getPrompt(p.Name, p.Arguments)
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		return s.call(p.Name, p.Arguments), nil
	}
	return nil, &rpcError{-32601, "method not found: " + req.Method}
}

func toolName(method string) string { return strings.ReplaceAll(method, ".", "_") }

func (s *Server) exposed(m api.Method) bool {
	if m.Name == "methods" || m.Name == "approvals.decide" { // only the person decides approvals
		return false
	}
	return !m.Sensitive || s.AllowSecrets
}

func (s *Server) tools() []map[string]any {
	var list []map[string]any
	for _, m := range api.Methods {
		if !s.exposed(m) {
			continue
		}
		props := map[string]any{}
		var required []string
		for _, p := range m.Params {
			prop := map[string]any{"type": p.Type, "description": p.Description}
			if len(p.Enum) > 0 {
				prop["enum"] = p.Enum
			}
			props[p.Name] = prop
			if p.Required {
				required = append(required, p.Name)
			}
		}
		desc := m.Description
		if m.Destructive {
			desc += ". Needs the user's approval: LocalSites asks them in the app and waits up to 90 seconds."
		}
		schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		list = append(list, map[string]any{
			"name":        toolName(m.Name),
			"title":       m.Name,
			"description": desc,
			"inputSchema": schema,
			"annotations": map[string]any{
				"readOnlyHint":    !m.Mutates,
				"destructiveHint": m.Destructive,
				"idempotentHint":  !m.Mutates,
				"openWorldHint":   false,
			},
		})
	}
	return list
}

func (s *Server) call(name string, args map[string]any) map[string]any {
	var method *api.Method
	for i := range api.Methods {
		if toolName(api.Methods[i].Name) == name && s.exposed(api.Methods[i]) {
			method = &api.Methods[i]
		}
	}
	if method == nil {
		return toolError("Unknown tool " + name)
	}
	if args == nil {
		args = map[string]any{}
	}
	delete(args, "confirm") // older agents still send it; approval now happens in the app
	allowed := map[string]bool{}
	for _, p := range method.Params {
		allowed[p.Name] = true
	}
	for k := range args {
		if !allowed[k] {
			return toolError("Unknown argument " + k)
		}
	}
	res, err := s.API.Call(method.Name, args)
	if err != nil {
		return toolError(err.Error())
	}
	var pretty any
	text := string(res)
	if json.Unmarshal(res, &pretty) == nil {
		if b, err := json.MarshalIndent(pretty, "", "  "); err == nil {
			text = string(b)
		}
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": false}
}

func toolError(msg string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": msg}}, "isError": true}
}

/* ------------------------------------------------------- resources and prompts */

// Resources: a summary of every project and the error log of running ones, so agents can read them directly.
func (s *Server) resources() (any, *rpcError) {
	raw, err := s.API.Call("sites.list", nil)
	if err != nil {
		return nil, &rpcError{-32603, err.Error()}
	}
	var sites []struct {
		Folder  string `json:"folder"`
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		Runtime string `json:"runtime"`
	}
	_ = json.Unmarshal(raw, &sites)
	list := []map[string]any{}
	for _, st := range sites {
		f := url.PathEscape(st.Folder)
		list = append(list, map[string]any{"uri": "localsites://projects/" + f, "name": st.Name, "title": st.Name + ": project summary",
			"description": "Stack, address, PHP and web server, database, git, problems and recent errors", "mimeType": "application/json"})
		if st.Enabled {
			kind := "errors"
			if st.Runtime == "app" {
				kind = "app"
			}
			list = append(list, map[string]any{"uri": "localsites://logs/" + f + "/" + kind, "name": st.Name + " log", "title": st.Name + ": " + kind + " log", "mimeType": "text/plain"})
		}
	}
	return map[string]any{"resources": list}, nil
}

var logKinds = map[string]string{"errors": "site-error", "php": "site-php", "access": "site-access", "app": "site-app"}

func (s *Server) readResource(uri string) (any, *rpcError) {
	rest, ok := strings.CutPrefix(uri, "localsites://")
	if !ok {
		return nil, &rpcError{-32602, "unknown resource " + uri}
	}
	parts := strings.Split(rest, "/")
	text, mime := "", "text/plain"
	switch {
	case len(parts) == 2 && parts[0] == "projects":
		folder, _ := url.PathUnescape(parts[1])
		raw, err := s.API.Call("sites.context", map[string]any{"folder": folder})
		if err != nil {
			return nil, &rpcError{-32603, err.Error()}
		}
		text, mime = string(raw), "application/json"
	case len(parts) == 3 && parts[0] == "logs" && logKinds[parts[2]] != "":
		folder, _ := url.PathUnescape(parts[1])
		raw, err := s.API.Call("logs", map[string]any{"kind": logKinds[parts[2]], "folder": folder, "lines": 300})
		if err != nil {
			return nil, &rpcError{-32603, err.Error()}
		}
		var l struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &l)
		text = l.Text
	default:
		return nil, &rpcError{-32602, "unknown resource " + uri}
	}
	return map[string]any{"contents": []map[string]any{{"uri": uri, "mimeType": mime, "text": text}}}, nil
}

var prompts = []map[string]any{
	{"name": "diagnose_site", "title": "Diagnose a project", "description": "Find out why a local site is broken or slow, step by step",
		"arguments": []map[string]any{{"name": "folder", "description": "Project folder (from sites_list)", "required": true}}},
	{"name": "explain_project", "title": "Explain a project's setup", "description": "Summarise how a project runs locally and how to work on it",
		"arguments": []map[string]any{{"name": "folder", "description": "Project folder (from sites_list)", "required": true}}},
}

func getPrompt(name string, args map[string]string) (any, *rpcError) {
	f := args["folder"]
	if f == "" {
		return nil, &rpcError{-32602, "folder is required"}
	}
	var text string
	switch name {
	case "diagnose_site":
		text = "The local site in project folder \"" + f + "\" isn't working right. Diagnose it with LocalSites:\n" +
			"1. Read the resource localsites://projects/" + url.PathEscape(f) + " (or call sites_context) for its setup, problems and recent errors.\n" +
			"2. Call sites_check to test DNS, PHP, the database socket and HTTPS.\n" +
			"3. Read more of the error log with logs (kind site-error, site-php or site-app).\n" +
			"4. Explain the cause in plain words and suggest the smallest fix. Ask before changing settings; never change databases or .env values unless I agree."
	case "explain_project":
		text = "Explain how the project in folder \"" + f + "\" runs locally: read localsites://projects/" + url.PathEscape(f) +
			" (or call sites_context), then tell me its stack, address, PHP version and web server, which database it uses, how to start working on it, and anything that needs attention."
	default:
		return nil, &rpcError{-32602, "unknown prompt " + name}
	}
	return map[string]any{"messages": []map[string]any{{"role": "user", "content": map[string]any{"type": "text", "text": text}}}}, nil
}

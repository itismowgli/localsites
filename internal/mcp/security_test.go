package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMaskEnvAndSnippets(t *testing.T) {
	var v any
	_ = json.Unmarshal([]byte(`[{"folder":"shop","env_vars":{"STRIPE_KEY":"sk_live_1"}},{"addons":[{"id":"postgres","snippets":[{"text":"DB_PASSWORD=pw123"}]}]}]`), &v)
	b, _ := json.Marshal(maskEnv(v))
	if s := string(b); strings.Contains(s, "sk_live_1") || strings.Contains(s, "pw123") || !strings.Contains(s, "STRIPE_KEY") {
		t.Fatalf("got %s", s)
	}
}

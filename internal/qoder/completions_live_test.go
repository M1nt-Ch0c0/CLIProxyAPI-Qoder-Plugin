//go:build live

// completions_live_test.go probes the production chat route against the real
// upstream: encoded agent_chat_generation on the region gateway
// (api3.qoder.sh for intl, gateway.qoder.com.cn for cn). /qsvr/chat/completions
// returns an empty 404 once a token is accepted, so this probe must not post
// there.
//
// A minimal "hi" prompt keeps the cost at ~1 credit. QD_CHAT_MODEL picks the
// CPA-facing name (default qfmodel); it rides cpaToUpstreamKey exactly like
// production.
//
// Run:
//
//	QD_TOKEN=dt-... [QD_REGION=cn|intl] [QD_CHAT_MODEL=gmodel_latest] go test -tags live -run TestLiveAgentChat -v .
//
// Skipped unless -tags live AND QD_TOKEN are set, so normal CI stays hermetic.
package qoder

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestLiveAgentChat(t *testing.T) {
	sa := liveSA(t)
	requested := strings.TrimSpace(os.Getenv("QD_CHAT_MODEL"))
	if requested == "" {
		requested = "gmodel_latest"
	}
	upstream := cpaToUpstreamKey(stripProviderPrefix(requested))
	t.Logf("=== live agent chat via executor, region=%s model=%s→%s ===", authRegion(sa), requested, upstream)
	storage, err := json.Marshal(sa)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(pluginapi.ExecutorRequest{
		Model:       requested,
		Payload:     []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		StorageJSON: storage,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	out, err := handleExecExecute(raw)
	if err != nil {
		t.Fatalf("execute: %v (after %s)", err, time.Since(start).Truncate(time.Millisecond))
	}
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		t.Fatalf("envelope: %s err=%v", out, err)
	}
	var resp pluginapi.ExecutorResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	var folded map[string]any
	if err := json.Unmarshal(resp.Payload, &folded); err != nil {
		t.Fatal(err)
	}
	msg, _ := folded["choices"].([]any)
	text := ""
	if len(msg) > 0 {
		choice, _ := msg[0].(map[string]any)
		message, _ := choice["message"].(map[string]any)
		text, _ = message["content"].(string)
		if text == "" {
			text, _ = message["reasoning_content"].(string)
		}
	}
	t.Logf("[agent] http 200 after %s folded model=%v text_len=%d", time.Since(start).Truncate(time.Millisecond), folded["model"], len(text))
	if folded["model"] != upstream {
		t.Fatalf("folded model = %v, want %s", folded["model"], upstream)
	}
	if strings.TrimSpace(text) == "" {
		t.Fatal("assistant text is empty")
	}
	if bytes.Contains(resp.Payload, []byte("event:error")) || bytes.Contains(resp.Payload, []byte("JsonParser")) {
		t.Fatalf("upstream error in payload: %s", truncateRedacted(string(resp.Payload), 200))
	}
}

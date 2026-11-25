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
	catalog, err := callModelsAPI(sa)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	display := ""
	for _, m := range catalog {
		if m.ID == upstream {
			display = m.Name
		}
	}
	if display == "" {
		t.Fatalf("catalog has no %s", upstream)
	}
	alias := strings.ToLower(display)
	storeDynamicModels(modelCatalogAccountKey(sa), catalog)
	t.Cleanup(func() {
		dynamicModelsCache.Lock()
		if dynamicModelsCache.accountKey == modelCatalogAccountKey(sa) {
			dynamicModelsCache.models = nil
			dynamicModelsCache.accountKey = ""
			dynamicModelsCache.fetched = time.Time{}
		}
		dynamicModelsCache.Unlock()
	})
	t.Logf("=== live agent chat via executor, region=%s model=%s→%s display=%s alias=%s ===", authRegion(sa), requested, upstream, display, alias)
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
	var rawModels []string
	observeChunkModel = func(m string) { rawModels = append(rawModels, m) }
	t.Cleanup(func() { observeChunkModel = nil })
	start := time.Now()
	out, err := handleExecExecute(raw)
	if err != nil {
		t.Fatalf("execute: %v (after %s) http=%d", err, time.Since(start).Truncate(time.Millisecond), observedChatStatus)
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
	seen := map[string]int{}
	var order []string
	for _, m := range rawModels {
		if seen[m] == 0 {
			order = append(order, m)
		}
		seen[m]++
	}
	foldedModel, _ := folded["model"].(string)
	t.Logf("[agent] http %d after %s raw_chunk_model=%v counts=%v folded_model=%s text_len=%d", observedChatStatus, time.Since(start).Truncate(time.Millisecond), order, seen, foldedModel, len(text))
	if observedChatStatus != 200 {
		t.Fatalf("http status = %d, want 200", observedChatStatus)
	}
	if foldedModel != alias {
		t.Fatalf("folded model = %q, want %s (raw chunks %v)", foldedModel, alias, order)
	}
	if strings.TrimSpace(text) == "" {
		t.Fatal("assistant text is empty")
	}
	if bytes.Contains(resp.Payload, []byte("event:error")) || bytes.Contains(resp.Payload, []byte("JsonParser")) {
		t.Fatalf("upstream error in payload: %s", truncateRedacted(string(resp.Payload), 200))
	}
}

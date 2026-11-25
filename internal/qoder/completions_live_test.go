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
//	QD_TOKEN=dt-... [QD_REGION=cn|intl] [QD_CHAT_MODEL=glm-5.4] go test -tags live -run TestLiveAgentChat -v .
//
// Skipped unless -tags live AND QD_TOKEN are set, so normal CI stays hermetic.
package qoder

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveAgentChat(t *testing.T) {
	sa := liveSA(t)
	requested := strings.TrimSpace(os.Getenv("QD_CHAT_MODEL"))
	if requested == "" {
		requested = "qfmodel"
	}
	upstream := cpaToUpstreamKey(stripProviderPrefix(requested))
	endpoint := endpointChatFor(sa)
	t.Logf("=== live agent chat, region=%s endpoint=%s model=%s→%s ===", authRegion(sa), endpoint, requested, upstream)

	qwReq := &openAIRequest{}
	if err := json.Unmarshal([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), qwReq); err != nil {
		t.Fatal(err)
	}
	body, err := buildQoderBody(qwReq, upstream, uiUserType(nil))
	if err != nil {
		t.Fatalf("buildQoderBody: %v", err)
	}
	encoded := qoderEncode(body)
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if err := applyCosyHeaders(req, sa, encoded, endpoint, upstream, true); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	resp, err := liveChatClient().Do(req)
	if err != nil {
		t.Fatalf("transport: %v (after %s)", err, time.Since(start).Truncate(time.Millisecond))
	}
	defer resp.Body.Close()
	t.Logf("[agent] http %d after %s, content-type=%s", resp.StatusCode,
		time.Since(start).Truncate(time.Millisecond), resp.Header.Get("Content-Type"))
	var buf bytes.Buffer
	br := bufio.NewReaderSize(resp.Body, 64<<10)
	for {
		if buf.Len() >= 16384 {
			t.Logf("[agent] stop: limit reached (%d bytes)", buf.Len())
			break
		}
		chunk, err := br.ReadBytes('\n')
		if len(chunk) > 0 {
			buf.Write(chunk)
			line := strings.TrimSpace(string(chunk))
			if line != "" && (buf.Len() < 2048 || bytes.HasPrefix(chunk, []byte("data:"))) {
				t.Logf("[agent] +%s %s", time.Since(start).Truncate(time.Millisecond), truncateRedacted(line, 160))
			}
		}
		if err != nil {
			t.Logf("[agent] stream ended +%s: %v (total %d bytes)", time.Since(start).Truncate(time.Millisecond), err, buf.Len())
			break
		}
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from agent_chat_generation, got %d: %s", resp.StatusCode, truncateRedacted(buf.String(), 300))
	}
	completion, err := aggregateQoderSSE(strings.NewReader(buf.String()), requested)
	if err != nil {
		t.Fatalf("fold: %v (head=%s)", err, truncateRedacted(buf.String(), 300))
	}
	var folded map[string]any
	if err := json.Unmarshal(completion, &folded); err != nil {
		t.Fatal(err)
	}
	t.Logf("[agent] folded model=%v finish=%v", folded["model"], folded["choices"])
	if folded["model"] == "" || folded["model"] == "auto" {
		t.Fatalf("the reply model must name the requested model, got %v", folded["model"])
	}
}

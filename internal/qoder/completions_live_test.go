//go:build live

// completions_live_test.go — v0.8.76: probes the current client chat dialect
// (top-level model on the completions endpoint) against the real upstream.
// The field report this transport switch answers: the legacy envelope only
// carried model_config.key, the service no longer reads it, and the reply
// came back as auto (panel "model mismatch"). This probe asserts the reply
// model echoes the requested catalog key.
//
// A minimal "hi" prompt keeps the cost at ~1 credit. QD_CHAT_MODEL picks the
// CPA-facing name (default qfmodel); it rides cpaToUpstreamKey exactly like
// production.
//
// Run:
//
//	QD_TOKEN=dt-... [QD_REGION=cn|intl] [QD_CHAT_MODEL=glm-5.4] go test -tags live -run TestLiveCompletionsTopLevelModel -v .
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

func TestLiveCompletionsTopLevelModel(t *testing.T) {
	sa := liveSA(t)
	requested := strings.TrimSpace(os.Getenv("QD_CHAT_MODEL"))
	if requested == "" {
		requested = "qfmodel"
	}
	upstream := cpaToUpstreamKey(stripProviderPrefix(requested))
	endpoint := endpointCompletionsFor(sa)
	t.Logf("=== live completions probe, region=%s endpoint=%s model=%s→%s ===", authRegion(sa), endpoint, requested, upstream)

	qwReq := &openAIRequest{}
	if err := json.Unmarshal([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), qwReq); err != nil {
		t.Fatal(err)
	}
	body, err := buildCompletionsBody(qwReq, upstream)
	if err != nil {
		t.Fatalf("buildCompletionsBody: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	applyCompletionsHeaders(req, sa)

	start := time.Now()
	resp, err := liveChatClient().Do(req)
	if err != nil {
		t.Fatalf("transport: %v (after %s)", err, time.Since(start).Truncate(time.Millisecond))
	}
	defer resp.Body.Close()
	t.Logf("[completions] http %d after %s, content-type=%s", resp.StatusCode,
		time.Since(start).Truncate(time.Millisecond), resp.Header.Get("Content-Type"))
	var buf bytes.Buffer
	br := bufio.NewReaderSize(resp.Body, 64<<10)
	for {
		if buf.Len() >= 16384 {
			t.Logf("[completions] stop: limit reached (%d bytes)", buf.Len())
			break
		}
		chunk, err := br.ReadBytes('\n')
		if len(chunk) > 0 {
			buf.Write(chunk)
			line := strings.TrimSpace(string(chunk))
			if line != "" && (buf.Len() < 2048 || bytes.HasPrefix(chunk, []byte("data:"))) {
				t.Logf("[completions] +%s %s", time.Since(start).Truncate(time.Millisecond), truncateRedacted(line, 160))
			}
		}
		if err != nil {
			t.Logf("[completions] stream ended +%s: %v (total %d bytes)", time.Since(start).Truncate(time.Millisecond), err, buf.Len())
			break
		}
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from the completions endpoint, got %d: %s", resp.StatusCode, truncateRedacted(buf.String(), 300))
	}
	completion, err := aggregateCompletionsSSE(strings.NewReader(buf.String()), requested)
	if err != nil {
		t.Fatalf("fold: %v (head=%s)", err, truncateRedacted(buf.String(), 300))
	}
	var folded map[string]any
	if err := json.Unmarshal(completion, &folded); err != nil {
		t.Fatal(err)
	}
	t.Logf("[completions] folded model=%v finish=%v", folded["model"], folded["choices"])
	if folded["model"] != upstream {
		t.Fatalf("the reply model must echo the requested key %q (the auto reroute is the bug), got %v", upstream, folded["model"])
	}
}

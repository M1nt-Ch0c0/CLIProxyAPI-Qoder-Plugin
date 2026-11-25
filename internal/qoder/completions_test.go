package qoder

// The executor tests below pin the production chat wire contract: the request
// prepareInferRequest emits, model_config.key gmodel_latest, folded through
// qoderUnwrapFrame. The completions helpers earlier in this file cover the
// unused OpenAI-style dialect and are not what the executor sends.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// completionsSSEBody renders one plain OpenAI SSE reply: role opener, content
// delta, finish chunk, include_usage terminal chunk, [DONE].
func completionsSSEBody(model, text string) string {
	chunk := func(delta, finish string) string {
		return `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"` + model +
			`","choices":[{"index":0,"delta":` + delta + `,"finish_reason":` + finish + `}]}` + "\n\n"
	}
	return chunk(`{"role":"assistant","content":""}`, "null") +
		chunk(`{"content":`+jsonStringForTest(text)+`}`, "null") +
		chunk(`{}`, `"stop"`) +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"` + model +
		`","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}` + "\n\n" +
		"data: [DONE]\n\n"
}

func jsonStringForTest(s string) string {
	out, _ := json.Marshal(s)
	return string(out)
}

type capturedChat struct {
	mu     sync.Mutex
	method string
	path   string
	header http.Header
	body   []byte
	hits   int32
}

func (c *capturedChat) snapshot() (string, string, http.Header, []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.method, c.path, c.header.Clone(), append([]byte(nil), c.body...)
}

// completionsCaptureServer records the outbound request and serves one
// buffered reply, the way hostHTTPDoStream's test fallback consumes it.
func completionsCaptureServer(t *testing.T, status int, reply string) (*httptest.Server, *capturedChat) {
	t.Helper()
	cap := &capturedChat{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		cap.mu.Lock()
		cap.method, cap.path, cap.header, cap.body = r.Method, r.URL.RequestURI(), r.Header.Clone(), raw
		cap.hits++
		cap.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

func stubChatEndpoint(t *testing.T, raw string) {
	t.Helper()
	target, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	origChat := endpointChatFn
	endpointChatFn = func(*storedAuth) string { return raw }
	origPrep := prepareInferFn
	prepareInferFn = func(sa *storedAuth, body []byte, modelKey string) (*http.Request, error) {
		req, err := origPrep(sa, body, modelKey)
		if err != nil {
			return nil, err
		}
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		return req, nil
	}
	t.Cleanup(func() {
		endpointChatFn = origChat
		prepareInferFn = origPrep
	})
}

func agentChatURL(base string) string {
	return base + "/algo/api/v2/service/pro/sse/agent_chat_generation"
}

func decodeCapturedAgentBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	plain, err := qoderDecode(string(raw))
	if err != nil {
		t.Fatalf("outbound body must be QoderEncoding: %v", err)
	}
	var sent map[string]any
	if err := json.Unmarshal(plain, &sent); err != nil {
		t.Fatalf("decoded agent body: %v\nplain=%s", err, plain)
	}
	return sent
}

func assertAgentModel(t *testing.T, sent map[string]any, header http.Header) {
	t.Helper()
	mc, _ := sent["model_config"].(map[string]any)
	if mc["key"] != "gmodel_latest" {
		t.Fatalf("model_config.key = %v, want gmodel_latest", mc["key"])
	}
	if got := header.Get("X-Model-Key"); got != "gmodel_latest" {
		t.Fatalf("X-Model-Key = %q", got)
	}
	if !strings.HasPrefix(header.Get("Authorization"), "Bearer COSY.") {
		t.Fatalf("Authorization = %q, want a COSY bearer", header.Get("Authorization"))
	}
}

func completionsTestStorage(t *testing.T, region string) []byte {
	t.Helper()
	raw, err := json.Marshal(storedAuth{
		Auth:    storedTokens{AccessToken: "dt-test-token", Region: region},
		Account: storedAccount{UID: "uid-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// --- body / endpoint / header contract ---------------------------------------

func TestBuildCompletionsBodyTopLevelModel(t *testing.T) {
	qwReq := &openAIRequest{}
	if err := json.Unmarshal([]byte(`{"model":"glm-5.4","messages":[{"role":"user","content":"hi"}],"stream":false}`), qwReq); err != nil {
		t.Fatal(err)
	}
	body, err := buildCompletionsBody(qwReq, "gmodel_latest")
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatalf("body must be plain JSON (no QoderEncoding): %v", err)
	}
	if probe["model"] != "gmodel_latest" {
		t.Fatalf("the model key must ride the TOP level, got %v", probe["model"])
	}
	if probe["stream"] != true {
		t.Fatalf("stream must be forced true, got %v", probe["stream"])
	}
	opts, _ := probe["stream_options"].(map[string]any)
	if opts["include_usage"] != true {
		t.Fatalf("stream_options.include_usage must be true, got %v", probe["stream_options"])
	}
	msgs, _ := probe["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages must ride verbatim, got %v", probe["messages"])
	}
	for _, legacy := range []string{"model_config", "chat_context", "agent_chat_generation", "parameters"} {
		if _, present := probe[legacy]; present {
			t.Fatalf("legacy envelope key %q must not ride the completions body", legacy)
		}
	}
	for _, unset := range []string{"max_tokens", "temperature", "top_p", "stop", "tools", "reasoning_effort"} {
		if _, present := probe[unset]; present {
			t.Fatalf("unset dial %q must be omitted, got %v", unset, probe[unset])
		}
	}
}

func TestBuildCompletionsBodyForwardsSamplingDials(t *testing.T) {
	qwReq := &openAIRequest{}
	payload := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":128,"temperature":0.2,"top_p":0.9,` +
		`"stop":["END"],"reasoning_effort":"low","tools":[{"type":"function","function":{"name":"f"}}]}`
	if err := json.Unmarshal([]byte(payload), qwReq); err != nil {
		t.Fatal(err)
	}
	body, err := buildCompletionsBody(qwReq, "gmodel_latest")
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatal(err)
	}
	if probe["max_tokens"] != float64(128) || probe["temperature"] != 0.2 || probe["top_p"] != 0.9 {
		t.Fatalf("sampling dials must ride verbatim: %v", probe)
	}
	if probe["reasoning_effort"] != "low" {
		t.Fatalf("reasoning_effort must ride verbatim, got %v", probe["reasoning_effort"])
	}
	if _, ok := probe["stop"].([]any); !ok {
		t.Fatalf("stop must ride verbatim, got %v", probe["stop"])
	}
	if _, ok := probe["tools"].([]any); !ok {
		t.Fatalf("tools must ride verbatim, got %v", probe["tools"])
	}
}

func TestEndpointCompletionsPerRegion(t *testing.T) {
	cases := []struct {
		name string
		sa   *storedAuth
		want string
	}{
		{"intl explicit", &storedAuth{Auth: storedTokens{Region: "intl"}}, "https://api3.qoder.sh/qsvr/chat/completions"},
		{"cn explicit", &storedAuth{Auth: storedTokens{Region: "cn"}}, "https://gateway.qoder.com.cn/qsvr/chat/completions"},
		{"cn default", &storedAuth{}, "https://gateway.qoder.com.cn/qsvr/chat/completions"},
		{"intl legacy domain", &storedAuth{Auth: storedTokens{Domain: "qoder.sh"}}, "https://api3.qoder.sh/qsvr/chat/completions"},
	}
	for _, tc := range cases {
		if got := endpointCompletionsFor(tc.sa); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestApplyCompletionsHeadersBearerNoCosy(t *testing.T) {
	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-test-token"}}
	req, err := http.NewRequest(http.MethodPost, "https://example.invalid/chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	applyCompletionsHeaders(req, sa)
	if got := req.Header.Get("Authorization"); got != "Bearer dt-test-token" {
		t.Fatalf("Authorization must be the Bearer access token, got %q", got)
	}
	if got := req.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("Accept: got %q", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type: got %q", got)
	}
	if req.Header.Get("X-Request-ID") == "" || req.Header.Get("X-Session-ID") == "" {
		t.Fatal("X-Request-ID / X-Session-ID must be minted")
	}
	for k := range req.Header {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "cosy") || lower == "x-model-key" || lower == "x-model-source" {
			t.Fatalf("the completions path must not carry the legacy signed header %q", k)
		}
	}
}

// --- plain-SSE decoder guards -------------------------------------------------

func TestCompletionsUnwrapFrameGuards(t *testing.T) {
	body, meaningful, err := completionsUnwrapFrame(`data: {"choices":[{"delta":{"content":"hi"}}]}`)
	if err != nil || !meaningful || !strings.Contains(body, `"content":"hi"`) {
		t.Fatalf("plain chunk must pass through: body=%q meaningful=%v err=%v", body, meaningful, err)
	}
	body, meaningful, err = completionsUnwrapFrame(`data: {"choices":[],"usage":{"total_tokens":8}}`)
	if err != nil || !meaningful || body == "" {
		t.Fatalf("usage terminal chunk must pass: body=%q meaningful=%v err=%v", body, meaningful, err)
	}
	body, meaningful, err = completionsUnwrapFrame("data: [DONE]")
	if err != nil || meaningful || body != "[DONE]" {
		t.Fatalf("[DONE]: body=%q meaningful=%v err=%v", body, meaningful, err)
	}
	for _, control := range []string{": keep-alive", "event: message", "", `data: {"error":null}`, `data: {"firstTokenDuration":3037,"totalDuration":3045,"serverDuration":28}`} {
		body, meaningful, err = completionsUnwrapFrame(control)
		if err != nil || meaningful || body != "" {
			t.Errorf("control/metadata line %q must be swallowed: body=%q meaningful=%v err=%v", control, body, meaningful, err)
		}
	}
	_, _, err = completionsUnwrapFrame(`data: {"error":{"message":"model not found"}}`)
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("an error object on the data channel must surface, got %v", err)
	}
}

func TestCompletionsUnwrapFrameQueueTicketStillRetries(t *testing.T) {
	frame := "data: " + `{"error":` + queueTicketL1() + `}`
	_, _, err := completionsUnwrapFrame(frame)
	var fe *qoderFrameError
	if !errors.As(err, &fe) || fe.queued == nil {
		t.Fatalf("a 10605 ticket riding an error frame must stay retryable, got %v", err)
	}
}

func TestAggregateCompletionsSSEGuards(t *testing.T) {
	out, err := aggregateCompletionsSSE(strings.NewReader(completionsSSEBody("gmodel_latest", "hello")), "glm-5.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var completion map[string]any
	if err := json.Unmarshal(out, &completion); err != nil {
		t.Fatal(err)
	}
	if completion["model"] != "gmodel_latest" {
		t.Fatalf("the folded completion must carry the upstream model, got %v", completion["model"])
	}
	if !strings.Contains(string(out), `"content":"hello"`) || !strings.Contains(string(out), `"finish_reason":"stop"`) {
		t.Fatalf("content/finish not folded: %s", out)
	}

	_, err = aggregateCompletionsSSE(strings.NewReader("data: {\"error\":{\"message\":\"boom\"}}\n\n"), "glm-5.4")
	if err == nil || !strings.Contains(err.Error(), "qoder upstream error") {
		t.Fatalf("a 200 error frame must fail the fold, got %v", err)
	}
	_, err = aggregateCompletionsSSE(strings.NewReader(": keep-alive\ndata: [DONE]\n\n"), "glm-5.4")
	if err == nil || !strings.Contains(err.Error(), "empty_stream") {
		t.Fatalf("an empty stream must fail the fold, got %v", err)
	}
	_, err = aggregateCompletionsSSE(strings.NewReader("data: {\"firstTokenDuration\":1335,\"totalDuration\":6280}\ndata: [DONE]\n\n"), "glm-5.4")
	if err == nil || !strings.Contains(err.Error(), "empty_stream") {
		t.Fatalf("a metadata-only stream must fail the fold, got %v", err)
	}
}

// TestEndpointChatForDesktopCapture pins the chat URL the desktop app POSTs.
// Qoder 0.5.18 intl log: POST https://api3.qoder.sh/algo/api/v2/service/pro/sse/agent_chat_generation
// returned 200, with no query string. The model rides model_config.key.
func TestEndpointChatForDesktopCapture(t *testing.T) {
	const path = "/algo/api/v2/service/pro/sse/agent_chat_generation"
	cases := []struct {
		region string
		host   string
	}{
		{"intl", "api3.qoder.sh"},
		{"cn", "gateway.qoder.com.cn"},
	}
	for _, c := range cases {
		raw := endpointChatFor(&storedAuth{Auth: storedTokens{Region: c.region}})
		req, err := http.NewRequest(http.MethodPost, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		if req.URL.Host != c.host || req.URL.Path != path || req.URL.RawQuery != "" {
			t.Errorf("region %s chat = %s %s?%s", c.region, req.URL.Host, req.URL.Path, req.URL.RawQuery)
		}
	}
}

// --- executor: production chat posts agent_chat_generation -------------------

func TestQoderEncodeRoundTrip(t *testing.T) {
	plain := []byte(`{"model_config":{"key":"gmodel_latest"},"text":"你好"}`)
	got, err := qoderDecode(qoderEncode(plain))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plain) {
		t.Fatalf("round trip = %s", got)
	}
}

// TestExecutePostsAgentChatAndFoldsReply: "glm-5.4" leaves as
// model_config.key gmodel_latest on agent_chat_generation. The folded
// completion reports the CPA alias glm-5.4, not the catalog key and not
// whatever the chunk labeled itself.
func TestExecutePostsAgentChatAndFoldsReply(t *testing.T) {
	srv, cap := completionsCaptureServer(t, http.StatusOK, completionsSSEBody("gmodel_latest", "hello"))
	stubChatEndpoint(t, agentChatURL(srv.URL))

	req := pluginapi.ExecutorRequest{
		Model:       "glm-5.4",
		Payload:     []byte(`{"model":"glm-5.4","messages":[{"role":"user","content":"hi"}],"stream":false}`),
		StorageJSON: completionsTestStorage(t, "intl"),
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	out, err := handleExecExecute(raw)
	if err != nil {
		t.Fatalf("execute must succeed: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		t.Fatalf("envelope: %s err=%v", out, err)
	}
	var resp pluginapi.ExecutorResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	var completion map[string]any
	if err := json.Unmarshal(resp.Payload, &completion); err != nil {
		t.Fatal(err)
	}
	if completion["model"] != "glm-5.4" {
		t.Fatalf("the reply model must be the CPA alias glm-5.4, got %v", completion["model"])
	}
	if !strings.Contains(string(resp.Payload), `"content":"hello"`) {
		t.Fatalf("answer not folded: %s", resp.Payload)
	}

	method, path, header, body := cap.snapshot()
	wantPath := "/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1"
	if method != http.MethodPost || path != wantPath {
		t.Fatalf("must POST agent_chat_generation, got %s %s", method, path)
	}
	if strings.Contains(string(body), "glm-5") || strings.Contains(string(body), "GLM-5.4") {
		t.Fatalf("upstream was asked for a display name: %s", body)
	}
	t.Logf("executor posted %s model_header=%s body_has_glm-5=%v", path, header.Get("X-Model-Key"), strings.Contains(string(body), "glm-5"))
	sent := decodeCapturedAgentBody(t, body)
	assertAgentModel(t, sent, header)
	if sent["stream"] != true {
		t.Fatalf("outbound stream = %v, want true", sent["stream"])
	}
}

func TestExecuteUsesCatalogDisplayName(t *testing.T) {
	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "dt-test-token-glm99", Region: "intl"},
		Account: storedAccount{UID: "uid-glm99"},
	}
	dynamicModelsCache.Lock()
	prevModels, prevFetched, prevKey := dynamicModelsCache.models, dynamicModelsCache.fetched, dynamicModelsCache.accountKey
	dynamicModelsCache.models = []pluginapi.ModelInfo{{ID: "gmodel_latest", Name: "GLM-9.9"}}
	dynamicModelsCache.fetched = time.Now()
	dynamicModelsCache.accountKey = modelCatalogAccountKey(sa)
	dynamicModelsCache.Unlock()
	t.Cleanup(func() {
		dynamicModelsCache.Lock()
		dynamicModelsCache.models, dynamicModelsCache.fetched, dynamicModelsCache.accountKey = prevModels, prevFetched, prevKey
		dynamicModelsCache.Unlock()
	})

	srv, cap := completionsCaptureServer(t, http.StatusOK, completionsSSEBody("auto", "hello"))
	stubChatEndpoint(t, agentChatURL(srv.URL))
	storage, err := json.Marshal(sa)
	if err != nil {
		t.Fatal(err)
	}
	req := pluginapi.ExecutorRequest{
		Model:       "GLM-9.9",
		Payload:     []byte(`{"model":"GLM-9.9","messages":[{"role":"user","content":"hi"}],"stream":false}`),
		StorageJSON: storage,
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	out, err := handleExecExecute(raw)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		t.Fatalf("envelope: %s err=%v", out, err)
	}
	var resp pluginapi.ExecutorResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	var completion map[string]any
	if err := json.Unmarshal(resp.Payload, &completion); err != nil {
		t.Fatal(err)
	}
	if completion["model"] != "glm-9.9" {
		t.Fatalf("reply model = %v, want glm-9.9", completion["model"])
	}
	_, _, header, body := cap.snapshot()
	sent := decodeCapturedAgentBody(t, body)
	plain, err := json.Marshal(sent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(plain)), "glm-9.9") {
		t.Fatalf("upstream body carried the display name: %s", plain)
	}
	assertAgentModel(t, sent, header)
}

func TestExecuteRejectsErrorFrame(t *testing.T) {
	srv, _ := completionsCaptureServer(t, http.StatusOK, "data: {\"error\":{\"message\":\"model not found\"}}\n\n")
	stubChatEndpoint(t, srv.URL)
	req := pluginapi.ExecutorRequest{
		Model:       "glm-5.4",
		Payload:     []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		StorageJSON: completionsTestStorage(t, "cn"),
	}
	raw, _ := json.Marshal(req)
	_, err := handleExecExecute(raw)
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("a 200 error frame must fail the request, got %v", err)
	}
}

func TestStreamSyncPathPostsAgentChat(t *testing.T) {
	srv, cap := completionsCaptureServer(t, http.StatusOK, completionsSSEBody("gmodel_latest", "hello"))
	stubChatEndpoint(t, agentChatURL(srv.URL))

	req := executorStreamRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:       "glm-5.4",
			Payload:     []byte(`{"messages":[{"role":"user","content":"hi"}],"stream":true}`),
			StorageJSON: completionsTestStorage(t, "cn"),
			Metadata:    map[string]any{"request_path": "/v1/messages"},
		},
	}
	raw, _ := json.Marshal(req)
	out, err := handleExecStream(raw)
	if err != nil {
		t.Fatalf("sync stream must succeed: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		t.Fatalf("envelope: %s err=%v", out, err)
	}
	var resp streamResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Chunks) < 3 {
		t.Fatalf("expected the opener/content/finish/usage chunks, got %d", len(resp.Chunks))
	}
	joined := ""
	for _, chunk := range resp.Chunks {
		payload := string(chunk.Payload)
		if !strings.HasPrefix(payload, "data: ") {
			t.Fatalf("cross-format entry must self-frame chunks, got %q", payload)
		}
		joined += payload
	}
	if !strings.Contains(joined, "hello") {
		t.Fatalf("answer missing from chunks: %s", joined)
	}
	if !strings.Contains(joined, `"model":"glm-5.4"`) {
		t.Fatalf("streamed chunks must report the CPA alias, got %s", joined)
	}
	if strings.Contains(joined, `"model":"gmodel_latest"`) {
		t.Fatalf("streamed chunks kept the catalog key: %s", joined)
	}
	_, path, header, body := cap.snapshot()
	if !strings.Contains(path, "/algo/api/v2/service/pro/sse/agent_chat_generation") {
		t.Fatalf("CN must ride agent_chat_generation, got %s", path)
	}
	assertAgentModel(t, decodeCapturedAgentBody(t, body), header)
}

func TestStreamAsyncPathDeliversChunks(t *testing.T) {
	rec := installStreamSinks(t)
	srv, cap := completionsCaptureServer(t, http.StatusOK, completionsSSEBody("gmodel_latest", "hello"))
	stubChatEndpoint(t, agentChatURL(srv.URL))

	req := executorStreamRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:       "glm-5.4",
			Payload:     []byte(`{"messages":[{"role":"user","content":"hi"}],"stream":true}`),
			StorageJSON: completionsTestStorage(t, "intl"),
			Metadata:    map[string]any{"request_path": "/v1/messages"},
		},
		StreamID: "agent-async",
	}
	raw, _ := json.Marshal(req)
	out, err := handleExecStream(raw)
	if err != nil {
		t.Fatalf("async hand-off must succeed: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		t.Fatalf("envelope: %s err=%v", out, err)
	}
	rec.waitFor(t, 4)
	if faults := rec.inBand(); len(faults) != 0 {
		t.Fatalf("a clean reply must not report faults: %q", faults)
	}
	delivered := rec.delivered()
	if len(delivered) < 3 {
		t.Fatalf("expected the answer chunks, got %q", delivered)
	}
	joined := ""
	for _, payload := range delivered {
		if !strings.HasPrefix(payload, "data: ") {
			t.Fatalf("cross-format entry must self-frame chunks, got %q", payload)
		}
		joined += payload
	}
	if !strings.Contains(joined, `"model":"glm-5.4"`) || strings.Contains(joined, `"model":"gmodel_latest"`) {
		t.Fatalf("async chunks must report the CPA alias, got %s", joined)
	}
	_, _, header, body := cap.snapshot()
	assertAgentModel(t, decodeCapturedAgentBody(t, body), header)
}

func TestCollectCompletionsQueueRetryThenSucceeds(t *testing.T) {
	installFastQueueWait(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, queueTicketL1())
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, completionsSSEBody("gmodel_latest", "served after the queue"))
	}))
	t.Cleanup(srv.Close)
	stubChatEndpoint(t, srv.URL)

	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-test-token", Region: "intl"}}
	collector := &sseUsageCollector{}
	chunks, statusCode, err := collectUpstreamStreamCompletions([]byte(`{"model_config":{"key":"gmodel_latest"}}`), "gmodel_latest", sa, false, collector, "")
	if err != nil || statusCode != 0 {
		t.Fatalf("the retried round must succeed: status=%d err=%v", statusCode, err)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("exactly one retry must follow the ticket, hits=%d", hits)
	}
	if len(chunks) < 3 {
		t.Fatalf("expected the answer chunks, got %d", len(chunks))
	}
	if collector.detail().TotalTokens != 8 {
		t.Fatalf("usage must be collected from the terminal chunk, got %+v", collector.detail())
	}
}

func TestCollectCompletionsGuards(t *testing.T) {
	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-test-token"}}
	cases := []struct {
		name  string
		reply string
		want  string
	}{
		{"empty stream", ": keep-alive\ndata: [DONE]\n\n", "empty_stream"},
		{"truncated stream", "data: {\"choices\":[{\"delta\":{\"content\":\"half\"}}]}\n\n", "truncated"},
		{"error frame", "data: {\"error\":{\"message\":\"boom\"}}\n\n", "boom"},
	}
	for _, tc := range cases {
		srv, _ := completionsCaptureServer(t, http.StatusOK, tc.reply)
		stubChatEndpoint(t, srv.URL)
		_, _, err := collectUpstreamStreamCompletions([]byte(`{"model_config":{"key":"gmodel_latest"}}`), "gmodel_latest", sa, false, nil, "")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want error containing %q, got %v", tc.name, tc.want, err)
		}
	}
}

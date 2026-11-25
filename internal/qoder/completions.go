// completions.go is the OpenAI-style dialect tried in 0.8.76–0.8.78.
// Production chat does not use it. /qsvr/chat/completions answers 401 for
// every path under that prefix, and an empty 404 once a real token gets
// past that filter, so it is not a chat route. The executor posts the
// encoded agent_chat_generation body to endpointChatFor (region.go):
// api3.qoder.sh for intl, gateway.qoder.com.cn for cn.
//
// The helpers below stay so the dialect unit tests still compile. Do not
// point the executor back at endpointCompletionsFor.
package qoder

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

func endpointCompletionsFor(sa *storedAuth) string {
	return gatewayBaseFor(sa) + "/qsvr/chat/completions"
}

// endpointCompletionsFn is the test seam for the chat URL (httptest servers).
var endpointCompletionsFn = endpointCompletionsFor

// buildCompletionsBody mirrors the client body builders (Intl Qcn / CN fni):
// top-level model, verbatim messages, forced stream with usage reporting, and
// the sampling dials forwarded when the caller set them.
func buildCompletionsBody(qwReq *openAIRequest, upstreamModel string) ([]byte, error) {
	body := map[string]any{
		"model":          upstreamModel,
		"messages":       qwReq.Messages,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if len(qwReq.Tools) > 0 {
		body["tools"] = qwReq.Tools
	}
	if qwReq.ReasoningEffort != "" {
		body["reasoning_effort"] = qwReq.ReasoningEffort
	}
	if qwReq.MaxTokens > 0 {
		body["max_tokens"] = qwReq.MaxTokens
	}
	if qwReq.Temperature != nil {
		body["temperature"] = *qwReq.Temperature
	}
	if qwReq.TopP != nil {
		body["top_p"] = *qwReq.TopP
	}
	if len(qwReq.Stop) > 0 {
		body["stop"] = qwReq.Stop
	}
	return json.Marshal(body)
}

func applyCompletionsHeaders(req *http.Request, sa *storedAuth) {
	req.Header.Set("Authorization", "Bearer "+sa.Auth.AccessToken)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", uuid.NewString())
	req.Header.Set("X-Session-ID", uuid.NewString())
}

// completionsUnwrapFrame is the plain-SSE decoder for the pump and the
// synchronous collector: one data line IS the chunk, no envelope inside.
// It keeps the same guard contract qoderUnwrapFrame established for the
// legacy dialect, because the failure shapes are dialect-independent:
//   - an {"error":...} object on the data channel is an upstream failure,
//     not a chunk (v0.8.17 rule); a 10605 ticket riding it still retries
//     (v0.8.64 rule). No envelope status exists here, so the frame error
//     carries status 0 and the head gate falls back to 502 (headGateStatus).
//   - an object with neither choices nor usage can never be a chat chunk
//     (metadata/timing noise, issue #19 rule) — swallowed, meaningful=false,
//     so a metadata-only stream still trips the empty-stream guard.
func completionsUnwrapFrame(line string) (string, bool, error) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "data:") {
		return "", false, nil
	}
	body := stripDataPrefix(trimmed)
	if body == "[DONE]" {
		return "[DONE]", false, nil
	}
	var probe map[string]any
	if json.Unmarshal([]byte(body), &probe) != nil {
		return "", false, nil
	}
	if errVal, present := probe["error"]; present && errVal != nil {
		raw, _ := json.Marshal(errVal)
		frameErr := &qoderFrameError{
			msg: fmt.Sprintf("qoder upstream error: %s", describeQoderEnvelopeRejection(body, raw, 0)),
		}
		if sig := parseQueueSignal(string(raw)); sig.detected {
			frameErr.queued = &sig
		}
		return "", false, frameErr
	}
	if _, hasChoices := probe["choices"]; !hasChoices {
		if _, hasUsage := probe["usage"]; !hasUsage {
			return "", false, nil
		}
	}
	return body, true, nil
}

// aggregateCompletionsSSE folds the plain OpenAI SSE reply of the completions
// endpoint into one chat.completion (non-stream execute path), with the same
// error-frame and empty-stream guards aggregateQoderSSE applies to the legacy
// envelope stream.
func aggregateCompletionsSSE(r io.Reader, model string) ([]byte, error) {
	return aggregateUnwrappedSSE(r, model, "", completionsUnwrapFrame)
}

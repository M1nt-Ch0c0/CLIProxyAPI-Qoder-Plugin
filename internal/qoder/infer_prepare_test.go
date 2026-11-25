package qoder

import (
	"io"
	"strings"
	"testing"
)

func TestPrepareInferUsesCatalogKey(t *testing.T) {
	body := []byte(`{"model_config":{"key":"gmodel_latest"},"messages":[{"role":"user","content":"hi"}]}`)
	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-test-token", Region: "intl"}, Account: storedAccount{UID: "uid-1"}}
	req, err := prepareInferChat(sa, body, "gmodel_latest")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.URL.Path, "/algo/api/v2/service/pro/sse/agent_chat_generation") {
		t.Fatalf("path %s", req.URL.Path)
	}
	if req.URL.Query().Get("Encode") != "1" || req.URL.Query().Get("FetchKeys") == "" {
		t.Fatalf("query %s", req.URL.RawQuery)
	}
	if got := req.Header.Get("X-Model-Key"); got != "gmodel_latest" {
		t.Fatalf("X-Model-Key %q", got)
	}
	raw, _ := io.ReadAll(req.Body)
	plain, err := qoderDecode(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plain), `"key":"gmodel_latest"`) {
		t.Fatalf("body %s", plain)
	}
	if strings.Contains(string(plain), "glm-5") || strings.Contains(string(plain), "GLM-5.4") {
		t.Fatalf("display name leaked: %s", plain)
	}
	t.Logf("prepared path=%s query=%s X-Model-Key=%s body_has_gmodel_latest=%v body_has_glm-5=%v",
		req.URL.Path, req.URL.RawQuery, req.Header.Get("X-Model-Key"),
		strings.Contains(string(plain), `"key":"gmodel_latest"`), strings.Contains(string(plain), "glm-5"))
}

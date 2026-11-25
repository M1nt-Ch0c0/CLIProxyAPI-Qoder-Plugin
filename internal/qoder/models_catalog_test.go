package qoder

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// catalogEntryJSON renders one model/list entry; empty fields are omitted so
// tests can exercise missing key/display_name/format/enable shapes.
func catalogEntryJSON(key, name, format string, enable any, ctxTokens int) string {
	var sb strings.Builder
	sb.WriteByte('{')
	first := true
	add := func(k, v string) {
		if !first {
			sb.WriteByte(',')
		}
		first = false
		sb.WriteString(k)
		sb.WriteByte(':')
		sb.WriteString(v)
	}
	if key != "" {
		add(`"key"`, fmt.Sprintf("%q", key))
	}
	if name != "" {
		add(`"display_name"`, fmt.Sprintf("%q", name))
	}
	if format != "" {
		add(`"format"`, fmt.Sprintf("%q", format))
	}
	if enable != nil {
		b, _ := json.Marshal(enable)
		add(`"enable"`, string(b))
	}
	if ctxTokens > 0 {
		add(`"max_input_tokens"`, fmt.Sprintf("%d", ctxTokens))
	}
	sb.WriteByte('}')
	return sb.String()
}

func parseCatalogForTest(t *testing.T, body string) []string {
	t.Helper()
	models, err := parseModelsCatalog([]byte(body))
	if err != nil {
		t.Fatalf("parseModelsCatalog: %v", err)
	}
	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	return ids
}

// TestParseModelsCatalogAssistantOnly — the client's DEFAULT_SCENE is
// "assistant"; a response carrying only that scene must parse (the old
// chat-only reader failed here and dropped the whole catalog to the static
// table, which is how requested keys silently became auto).
func TestParseModelsCatalogAssistantOnly(t *testing.T) {
	body := `{"assistant":[` + catalogEntryJSON("smodel", "Sonus", "openai", true, 180000) + `]}`
	ids := parseCatalogForTest(t, body)
	if len(ids) != 1 || ids[0] != "smodel" {
		t.Fatalf("assistant-only response: got %v, want [smodel]", ids)
	}
}

// TestParseModelsCatalogChatOnly — legacy chat-only responses keep working.
func TestParseModelsCatalogChatOnly(t *testing.T) {
	body := `{"chat":[` + catalogEntryJSON("qfmodel", "Qwen3.8-Flash", "openai", true, 180000) + `]}`
	ids := parseCatalogForTest(t, body)
	if len(ids) != 1 || ids[0] != "qfmodel" {
		t.Fatalf("chat-only response: got %v, want [qfmodel]", ids)
	}
}

// TestParseModelsCatalogAssistantEmptyFallsToChat — an empty assistant array
// must not terminate discovery; the next scene with entries wins.
func TestParseModelsCatalogAssistantEmptyFallsToChat(t *testing.T) {
	body := `{"assistant":[],"chat":[` + catalogEntryJSON("gmodel", "GLM-5.3", "openai", true, 180000) + `]}`
	ids := parseCatalogForTest(t, body)
	if len(ids) != 1 || ids[0] != "gmodel" {
		t.Fatalf("empty assistant must fall to chat: got %v, want [gmodel]", ids)
	}
}

// TestParseModelsCatalogAssistantPreferredOverChat — when both scenes carry
// entries, assistant (the CLI catalog scene) wins.
func TestParseModelsCatalogAssistantPreferredOverChat(t *testing.T) {
	body := `{"chat":[` + catalogEntryJSON("dfmodel", "DeepSeek-Flash", "openai", true, 0) + `],` +
		`"assistant":[` + catalogEntryJSON("cmodel", "Cantus", "openai", true, 0) + `]}`
	ids := parseCatalogForTest(t, body)
	if len(ids) != 1 || ids[0] != "cmodel" {
		t.Fatalf("assistant must win over chat: got %v, want [cmodel]", ids)
	}
}

// TestParseModelsCatalogAppFallback — app is the last scene in the ladder.
func TestParseModelsCatalogAppFallback(t *testing.T) {
	body := `{"app":[` + catalogEntryJSON("ultimate", "Ultimate", "openai", true, 1000000) + `]}`
	ids := parseCatalogForTest(t, body)
	if len(ids) != 1 || ids[0] != "ultimate" {
		t.Fatalf("app fallback: got %v, want [ultimate]", ids)
	}
}

// TestParseModelsCatalogEnableRules — the client keeps an entry unless enable
// is literally false or 0. A missing enable field must NOT drop the entry
// (the old bool decode turned missing into false and emptied the catalog).
func TestParseModelsCatalogEnableRules(t *testing.T) {
	body := `{"assistant":[` +
		catalogEntryJSON("keep-missing", "Missing", "openai", nil, 0) + `,` +
		catalogEntryJSON("keep-true", "True", "openai", true, 0) + `,` +
		catalogEntryJSON("keep-null", "Null", "openai", json.RawMessage("null"), 0) + `,` +
		catalogEntryJSON("drop-false", "False", "openai", false, 0) + `,` +
		catalogEntryJSON("drop-zero", "Zero", "openai", 0, 0) + `,` +
		catalogEntryJSON("keep-one", "One", "openai", 1, 0) + `]}`
	ids := parseCatalogForTest(t, body)
	want := []string{"keep-missing", "keep-true", "keep-null", "keep-one"}
	if len(ids) != len(want) {
		t.Fatalf("enable rules: got %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("enable rules: got %v, want %v", ids, want)
		}
	}
}

// TestParseModelsCatalogMissingFieldsDropped — key, display_name and format
// must all be non-empty strings; an entry missing any of them is dropped
// individually (the rest of the scene survives).
func TestParseModelsCatalogMissingFieldsDropped(t *testing.T) {
	body := `{"assistant":[` +
		catalogEntryJSON("ok", "OK", "openai", true, 0) + `,` +
		catalogEntryJSON("no-format", "NoFormat", "", true, 0) + `,` +
		catalogEntryJSON("", "NoKey", "openai", true, 0) + `,` +
		catalogEntryJSON("no-name", "", "openai", true, 0) + `]}`
	ids := parseCatalogForTest(t, body)
	if len(ids) != 1 || ids[0] != "ok" {
		t.Fatalf("invalid entries must be dropped: got %v, want [ok]", ids)
	}
}

// TestParseModelsCatalogNoUsableScene — no scene with usable entries is a
// discovery failure (the caller's ladder applies), never a partial catalog.
func TestParseModelsCatalogNoUsableScene(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"assistant":[]}`,
		`{"assistant":[{"key":"x","display_name":"X"}]}`,
		`{"developer":[` + catalogEntryJSON("dev", "Dev", "openai", true, 0) + `]}`,
	} {
		if _, err := parseModelsCatalog([]byte(body)); err == nil {
			t.Fatalf("expected discovery failure for %s", body)
		}
	}
}

// TestEndpointModelsForInferencePath pins the catalog URL the desktop app
// actually GETs: {inference host}/algo/api/v2/model/list with no query
// (Qoder 0.5.18 intl log, status 200).
func TestEndpointModelsForInferencePath(t *testing.T) {
	cases := []struct {
		region string
		want   string
	}{
		{"intl", "https://api3.qoder.sh/algo/api/v2/model/list"},
		{"cn", "https://gateway.qoder.com.cn/algo/api/v2/model/list"},
	}
	for _, c := range cases {
		sa := &storedAuth{Auth: storedTokens{AccessToken: "tok", Region: c.region}}
		got := endpointModelsFor(sa)
		if got != c.want {
			t.Errorf("endpointModelsFor(%s) = %q, want %q", c.region, got, c.want)
		}
		req, err := http.NewRequest(http.MethodGet, got, nil)
		if err != nil {
			t.Fatal(err)
		}
		if req.Method != http.MethodGet || req.URL.Path != "/algo/api/v2/model/list" || req.URL.RawQuery != "" {
			t.Errorf("catalog request = %s %s", req.Method, req.URL.RequestURI())
		}
	}
}

// TestInferenceBaseFromRegionEndpoints — center discovery payload shapes:
// plain string nodes, object nodes, enterprise skip, scheme normalization.
func TestInferenceBaseFromRegionEndpoints(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"string nodes", `{"inferNodes":["https://api2.qoder.sh","https://api3.qoder.sh"]}`, "https://api2.qoder.sh"},
		{"object nodes", `{"inferNodes":[{"url":"https://api1.qoder.sh","type":"public"}]}`, "https://api1.qoder.sh"},
		{"enterprise skipped", `{"inferNodes":[{"url":"https://ent.example","type":"enterprise"},"https://api2.qoder.sh"]}`, "https://api2.qoder.sh"},
		{"bare host", `{"inferNodes":["api3.qoder.sh"]}`, "https://api3.qoder.sh"},
		{"trailing slash", `{"inferNodes":["https://api3.qoder.sh/"]}`, "https://api3.qoder.sh"},
		{"empty", `{"inferNodes":[]}`, ""},
		{"missing field", `{"centerNodes":["https://center.qoder.sh"]}`, ""},
		{"encrypted garbage", `UU1DAQK8HFyXrjpY`, ""},
	}
	for _, c := range cases {
		if got := inferenceBaseFromRegionEndpoints([]byte(c.body)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestDiscoverInferenceBaseViaCenter — the fallback only runs when the
// election yields no host; it tries v3 first and falls to v5 on failure.
func TestDiscoverInferenceBaseViaCenter(t *testing.T) {
	defer func(id *machineIdentity) { machineIdentityOverride = id }(machineIdentityOverride)
	machineIdentityOverride = &machineIdentity{MachineToken: "test-token", MachineType: "test-type", Source: "derived"}

	var v3Hits, v5Hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/service/region/endpoints":
			v3Hits++
			if v3Hits == 1 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			fmt.Fprint(w, `{"inferNodes":["https://api2.qoder.sh"]}`)
		case "/api/v5/service/region/endpoints":
			v5Hits++
			if r.Header.Get("x-gw-user-id") != "u1" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer dt-") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fmt.Fprint(w, `{"inferNodes":["https://api1.qoder.sh"]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-center-test", Region: "intl"}, Account: storedAccount{UID: "u1"}}

	// v3 fails → v5 answers.
	if got := discoverInferenceBaseFromCenter(srv.URL, sa); got != "https://api1.qoder.sh" {
		t.Fatalf("v3→v5 fallback: got %q, want https://api1.qoder.sh", got)
	}
	if v3Hits != 1 || v5Hits != 1 {
		t.Fatalf("expected one v3 and one v5 probe, got v3=%d v5=%d", v3Hits, v5Hits)
	}
	// v3 answers → v5 untouched.
	if got := discoverInferenceBaseFromCenter(srv.URL, sa); got != "https://api2.qoder.sh" {
		t.Fatalf("v3 success: got %q, want https://api2.qoder.sh", got)
	}
	if v5Hits != 1 {
		t.Fatalf("v5 must not run when v3 answered: v5=%d", v5Hits)
	}
}

// TestBuildQoderBodyKeepsCatalogKey — acceptance: a non-auto catalog key
// rides model_config.key AND chat_context.extra.modelConfig.key verbatim, so
// the upstream never sees a blank/auto-substituted key.
func TestBuildQoderBodyKeepsCatalogKey(t *testing.T) {
	for _, key := range []string{"smodel", "cmodel", "fast", "gmodel_latest", "qmodel_38max"} {
		req := &openAIRequest{Model: key, Messages: []openAIMessage{{Role: "user", Content: "hi"}}}
		raw, err := buildQoderBody(req, cpaToUpstreamKey(key), "personal_professional_trial")
		if err != nil {
			t.Fatalf("build(%s): %v", key, err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("json(%s): %v", key, err)
		}
		mc, _ := m["model_config"].(map[string]any)
		if mc == nil || mc["key"] != key {
			t.Errorf("%s: model_config.key = %v, want %q", key, mc["key"], key)
		}
		cc, _ := m["chat_context"].(map[string]any)
		extra, _ := cc["extra"].(map[string]any)
		emc, _ := extra["modelConfig"].(map[string]any)
		if emc == nil || emc["key"] != key {
			t.Errorf("%s: chat_context.extra.modelConfig.key = %v, want %q", key, emc["key"], key)
		}
	}
}

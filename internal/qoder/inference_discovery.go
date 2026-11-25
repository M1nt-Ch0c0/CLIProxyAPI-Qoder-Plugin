// inference_discovery.go — last-resort inference-host discovery through the
// center region-endpoint service. The client worker queries
// GET {center}/api/v3/service/region/endpoints (COSY-signed, anonymous body)
// and GET {center}/api/v5/service/region/endpoints (raw bearer + user id +
// machine identity) to elect its inference node. This plugin only needs the
// fallback when the per-region election yields no inference host at all
// (inferenceBaseFor); the normal catalog path never touches center, and the
// discovery-failure ladder (peer → model_cache → static) is unchanged.
package qoder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func discoverInferenceBaseViaCenter(sa *storedAuth) string {
	if sa == nil {
		return ""
	}
	return discoverInferenceBaseFromCenter(centerBaseForRegion(authRegion(sa)), sa)
}

func discoverInferenceBaseFromCenter(center string, sa *storedAuth) string {
	if center == "" || sa == nil {
		return ""
	}
	for _, probe := range []struct {
		path string
		v5   bool
	}{
		{"/api/v3/service/region/endpoints", false},
		{"/api/v5/service/region/endpoints", true},
	} {
		body, err := fetchRegionEndpoints(center+probe.path, sa, probe.v5)
		if err != nil {
			continue
		}
		if base := inferenceBaseFromRegionEndpoints(body); base != "" {
			return base
		}
	}
	return ""
}

// fetchRegionEndpoints issues one center discovery GET. v3 rides the same
// COSY signing as inference with an empty body (the worker signs the GET
// with no body); v5 carries the raw access-token bearer, x-gw-user-id and
// the machine-identity headers. The v5 response may be wasm-encrypted —
// parsing is best-effort plain JSON, an unreadable payload simply falls
// through to the next probe.
func fetchRegionEndpoints(rawURL string, sa *storedAuth, v5 bool) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if v5 {
		req.Header.Set("Authorization", "Bearer "+sa.Auth.AccessToken)
		req.Header.Set("x-gw-user-id", sa.Account.UID)
		mi := machineIdentityFor(authRegion(sa), sa.Account.UID, false)
		attachMachineIdentityHeaders(req, &mi)
	} else if err := applyCosyHeaders(req, sa, "", rawURL, "", false); err != nil {
		return nil, fmt.Errorf("cosy sign: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", clientUA)
	resp, err := hostHTTPDo(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("region endpoints status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// inferenceBaseFromRegionEndpoints extracts the first usable inference node
// from a region-endpoints payload: {"inferNodes":["https://...", {"url":...}]}.
// Nodes are plain URL strings or objects with url/type; enterprise-only nodes
// are skipped (this plugin never rides an enterprise gateway).
func inferenceBaseFromRegionEndpoints(body []byte) string {
	var doc struct {
		InferNodes []json.RawMessage `json:"inferNodes"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return ""
	}
	for _, raw := range doc.InferNodes {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if base := normalizeNodeBase(s); base != "" {
				return base
			}
			continue
		}
		var node struct {
			URL  string `json:"url"`
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &node) != nil || strings.EqualFold(node.Type, "enterprise") {
			continue
		}
		if base := normalizeNodeBase(node.URL); base != "" {
			return base
		}
	}
	return ""
}

func normalizeNodeBase(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	return strings.TrimRight(s, "/")
}

// models.go implements the ModelProvider capability: static and per-auth
// model lists, dynamic model discovery via the upstream models API, alias
// reverse resolution (client-facing alias → upstream model id), and the
// host-config oauth-excluded-models filter.
package qoder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// wbModels is the static fallback model list. Kept in sync with the live
// assistant-scene catalog (the client's DEFAULT_SCENE is "assistant", not
// "chat") as of 2026-11-25: auto, ultimate, fast, efficient, smodel, cmodel,
// qmodel_latest, qmodel_38max, qfmodel, kmodel_latest, kmodel, gmodel_latest,
// gmodel, gfmodel, dmodel, dfmodel, mmodel. The 2026-09 chat-scene table
// advertised keys the gateway no longer knows (performance, qmodel,
// q36fmodel) — requesting one silently routed to auto. Only keys present in
// the current catalog are registered here; dynamic refresh via the inference
// host's /algo/api/v2/model/list replaces this at runtime when an account is
// present. Aliases use the qoder/ prefix in AuthAttributes; bare IDs work
// too.
func wbModels() []pluginapi.ModelInfo {
	return []pluginapi.ModelInfo{
		{ID: "auto", Name: "Auto", ContextLength: 200000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "ultimate", Name: "Ultimate", ContextLength: 1000000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "fast", Name: "Fast", ContextLength: 200000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "efficient", Name: "Efficient", ContextLength: 200000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "smodel", Name: "Sonus", ContextLength: 180000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "cmodel", Name: "Cantus", ContextLength: 180000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "qmodel_latest", Name: "Qwen4-Max", ContextLength: 1000000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "qmodel_38max", Name: "Qwen3.8-Max", ContextLength: 180000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "qfmodel", Name: "Qwen3.8-Flash", ContextLength: 180000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "kmodel_latest", Name: "Kimi-K3.1-Pro", ContextLength: 180000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "kmodel", Name: "Kimi-K2.8-Preview", ContextLength: 180000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "gmodel_latest", Name: "GLM-5.4", ContextLength: 180000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "gmodel", Name: "GLM-5.3", ContextLength: 180000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "gfmodel", Name: "GLM-5.3-Flash", ContextLength: 1000000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "dmodel", Name: "DeepSeek-V4-Pro", ContextLength: 1000000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "dfmodel", Name: "DeepSeek-Flash", ContextLength: 1000000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
		{ID: "mmodel", Name: "MiniMax-M3.1-Flash", ContextLength: 180000, MaxCompletionTokens: 8192, OwnedBy: providerName, SupportedGenerationMethods: []string{"chat"}},
	}
}

func cachedDynamicModels(accountKey string) ([]pluginapi.ModelInfo, bool) {
	dynamicModelsCache.RLock()
	defer dynamicModelsCache.RUnlock()
	// v0.8.17: the entry must belong to the asking credential — a catalog
	// fetched for account A (region + plan-specific offers) must never serve
	// account B (fork libo0118/qoder-custom review).
	if dynamicModelsCache.accountKey == accountKey && len(dynamicModelsCache.models) > 0 && time.Since(dynamicModelsCache.fetched) < dynamicModelsCacheTTL {
		return dynamicModelsCache.models, true
	}
	return nil, false
}

func storeDynamicModels(accountKey string, models []pluginapi.ModelInfo) {
	dynamicModelsCache.Lock()
	dynamicModelsCache.models = models
	dynamicModelsCache.fetched = time.Now()
	dynamicModelsCache.accountKey = accountKey
	dynamicModelsCache.Unlock()
}

// dropDynamicModelsCache invalidates the per-credential discovery cache.
// Called when a model cooldown starts or is cleared: the cooled model must
// not ride a stale catalog, so the next per-auth model query rebuilds
// immediately (v0.8.18 per-model cooldown).
func dropDynamicModelsCache() {
	dynamicModelsCache.Lock()
	dynamicModelsCache.models = nil
	dynamicModelsCache.fetched = time.Time{}
	dynamicModelsCache.Unlock()
}

func fetchDynamicModels() []pluginapi.ModelInfo {
	models := wbModels()
	files, err := hostAuthListFiles()
	if err != nil || len(files) == 0 {
		return models
	}
	// Strict filename-prefix match — same filter as host_auth.go hostAuthList.
	// (Earlier code also matched files containing "codebuddy" anywhere, which
	// would wrongly include workbuddy-*.json auths here and cause us to call
	// the qoderwork models API with a workbuddy token.)
	prefix := providerName + "-"
	for _, f := range files {
		if !strings.HasPrefix(strings.ToLower(f.Name), prefix) {
			continue
		}
		raw, err := hostAuthGetByIndex(f.AuthIndex)
		if err != nil {
			continue
		}
		sa, err := parseStored(raw)
		if err != nil || sa == nil {
			continue
		}
		dyn, err := callModelsAPIFn(sa)
		if err == nil && len(dyn) > 0 {
			storeDynamicModels(modelCatalogAccountKey(sa), dyn)
			return dyn
		}
	}
	return models
}

func fetchDynamicModelsFromStorage(storageJSON []byte) []pluginapi.ModelInfo {
	sa, err := parseStored(storageJSON)
	if err != nil || sa == nil {
		return fetchDynamicModels()
	}
	// v0.8.17: cache lookup is scoped to THIS credential (the storage JSON is
	// the account the host is asking about). On miss, discovery runs against
	// the same credential and the result is stored under its key.
	key := modelCatalogAccountKey(sa)
	region := authRegion(sa)
	if models, ok := cachedDynamicModels(key); ok {
		return filterCoolingModels(sa, models)
	}
	if dyn, err := callModelsAPIFn(sa); err == nil && len(dyn) > 0 {
		storeDynamicModels(key, dyn)
		// v0.8.27: stamp the last-known-good catalog into the credential file
		// (raw list, pre cooldown filter and pre exclusion — both re-apply on
		// every serve branch). Change-guarded inside; best-effort.
		persistModelSnapshot(storageJSON, region, dyn)
		return filterCoolingModels(sa, dyn)
	}
	// v0.8.27 failure ladder: a live same-region peer discovery still beats
	// every persisted answer, but the peer path must NOT fall through to the
	// static guess — that would mask the real persisted catalog. Ladder:
	// live peer → persisted snapshot → static wbModels().
	if peer := discoverModelsViaRegionPeers(region); len(peer) > 0 {
		return filterCoolingModels(sa, peer)
	}
	if snap, ok := persistedSnapshotForStorage(storageJSON, region); ok {
		return filterCoolingModels(sa, snap)
	}
	return filterCoolingModels(sa, wbModels())
}

// discoverModelsViaRegionPeers scans same-region peer credentials for a live
// catalog when THIS credential's own discovery fails (v0.12.76 region
// boundary: an intl account can never be handed a cn catalog and vice versa).
// Returns nil when no peer answers — callers decide what fallback applies
// (persisted snapshot, static list). This split-out form exists so the
// failure ladder in fetchDynamicModelsFromStorage can slot the persisted
// model_cache snapshot between "peers answered" and the static guess without
// the static list masking the snapshot.
func discoverModelsViaRegionPeers(region string) []pluginapi.ModelInfo {
	files, err := hostAuthListFiles()
	if err != nil || len(files) == 0 {
		return nil
	}
	// Strict filename-prefix match — same filter as host_auth.go hostAuthList.
	prefix := providerName + "-"
	for _, f := range files {
		if !strings.HasPrefix(strings.ToLower(f.Name), prefix) {
			continue
		}
		raw, err := hostAuthGetByIndex(f.AuthIndex)
		if err != nil {
			continue
		}
		sa, err := parseStored(raw)
		if err != nil || sa == nil {
			continue
		}
		if authRegion(sa) != region {
			continue
		}
		dyn, err := callModelsAPIFn(sa)
		if err == nil && len(dyn) > 0 {
			storeDynamicModels(modelCatalogAccountKey(sa), dyn)
			return dyn
		}
	}
	return nil
}

// fetchDynamicModelsForRegion is the region-scoped fallback used when THIS
// credential's own discovery fails: only catalogs discovered under the SAME
// region are accepted, so an intl account can never be handed a cn catalog
// (different gateway, different model ids) and vice versa (v0.12.76).
//
// v0.8.27: the live peer scan moved to discoverModelsViaRegionPeers (returns
// nil instead of the static list when no peer succeeds) so callers can slot
// the persisted model_cache snapshot between "peers answered" and "static
// guess". This wrapper keeps the historical static-terminated contract for
// its remaining callers.
func fetchDynamicModelsForRegion(region string) []pluginapi.ModelInfo {
	if peer := discoverModelsViaRegionPeers(region); len(peer) > 0 {
		return peer
	}
	return wbModels()
}

// filterCoolingModels removes models currently cooling for THIS credential
// (v0.8.18 per-model cooldown) so the host's built-in scheduler is never
// offered a degraded (auth, model) pair. A credential with no active
// cooldowns returns the list untouched.
func filterCoolingModels(sa *storedAuth, models []pluginapi.ModelInfo) []pluginapi.ModelInfo {
	authID := cooldownAuthIDFor(sa)
	if authID == "" || len(models) == 0 || len(cooldownSnapshotFor(authID)) == 0 {
		return models
	}
	out := make([]pluginapi.ModelInfo, 0, len(models))
	for _, m := range models {
		if modelIsCooling(authID, m.ID) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// fetchDynamicModels calls the QoderWork API to get the latest model list.
// Falls back to the hardcoded list on any error.
// callModelsAPIFn is the discovery seam: tests swap it to exercise the
// for_auth chain without the network. Production always uses callModelsAPI.
var callModelsAPIFn = callModelsAPI

// callModelsAPI GETs /algo/api/v2/model/list?Encode=1 from the account's
// inference host with COSY signing (same as inference). The desktop
// models.list interceptor always adds Encode=1; the request log drops the
// query. Returns plain JSON (not QoderEncoding). Falls back to wbModels()
// on any error.
func callModelsAPI(sa *storedAuth) ([]pluginapi.ModelInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// model/list works with an empty JSON object body (verified in
	// reference_impl.py). The COSY signature covers the request body, so
	// the body we sign MUST be the body we send: the gateway recomputes
	// md5 over the bytes it actually received, and an unsigned/absent body
	// against a signed "{}" fails with 403 "Signature invalid" (issue #8:
	// 3/3 repros; body==signed-body passes 3/3). A GET with a body is
	// unusual but legal, and matches how the chat path signs+sends.
	encodedBody := qoderEncode([]byte("{}"))
	rawURL := endpointModelsFor(sa)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, strings.NewReader(encodedBody))
	if err != nil {
		return nil, err
	}
	if err := applyCosyHeaders(req, sa, encodedBody, rawURL, "", false); err != nil {
		return nil, fmt.Errorf("cosy sign: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", clientUA)
	resp, err := hostHTTPDo(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models API status %d", resp.StatusCode)
	}
	return parseModelsCatalog(resp.Body)
}

// modelCatalogScenes is the scene preference order. The client's DEFAULT_SCENE
// is "assistant" (sg().scene); "chat" and "app" remain as fallbacks for older
// catalog shapes. The first scene yielding a non-empty usable list wins.
var modelCatalogScenes = []string{"assistant", "chat", "app"}

// parseModelsCatalog decodes a model/list response ({scene: [entry, ...]}).
// Entry acceptance mirrors the client worker: key, display_name and format
// must all be non-empty strings; enable defaults to enabled — only a literal
// false or 0 disables an entry (a missing field decoded into a Go bool would
// wrongly drop the whole catalog).
func parseModelsCatalog(body []byte) ([]pluginapi.ModelInfo, error) {
	var apiResp map[string]json.RawMessage
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("models parse: %w", err)
	}
	for _, scene := range modelCatalogScenes {
		raw, ok := apiResp[scene]
		if !ok {
			continue
		}
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			continue
		}
		out := make([]pluginapi.ModelInfo, 0, len(entries))
		for _, e := range entries {
			key := catalogString(e["key"])
			name := catalogString(e["display_name"])
			format := catalogString(e["format"])
			if key == "" || name == "" || format == "" {
				continue
			}
			if !catalogEntryEnabled(e["enable"]) {
				continue
			}
			ctxLen := int64(180000)
			if n, ok := catalogNumber(e["max_input_tokens"]); ok && n > 0 {
				ctxLen = int64(n)
			}
			out = append(out, pluginapi.ModelInfo{
				ID:                         key,
				Name:                       name,
				ContextLength:              ctxLen,
				MaxCompletionTokens:        8192,
				OwnedBy:                    providerName,
				SupportedGenerationMethods: []string{"chat"},
			})
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	return nil, fmt.Errorf("no usable model scene (assistant/chat/app) in models response")
}

// catalogString mirrors the worker's cf(): a non-empty JSON string, else "".
func catalogString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// catalogEntryEnabled mirrors the worker's enable rule
// (enable: false !== raw && 0 !== raw): missing/null/true/anything-but-false-or-0
// keeps the entry; only a literal false or numeric 0 drops it.
func catalogEntryEnabled(raw json.RawMessage) bool {
	t := strings.TrimSpace(string(raw))
	switch t {
	case "", "null":
		return true
	case "false":
		return false
	}
	var n float64
	if json.Unmarshal([]byte(t), &n) == nil && n == 0 {
		return false
	}
	return true
}

func catalogNumber(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var n float64
	if json.Unmarshal(raw, &n) != nil {
		return 0, false
	}
	return n, true
}

func cacheModelAliases(host pluginapi.HostConfigSummary) {
	// Model discovery is the second HostConfigSummary delivery point (parse
	// is the first). Cache the configured proxy URL here too so deployments
	// whose first plugin callback was a model request still populate the
	// proxy policy (v0.8.55).
	rememberHostProxy(host.ProxyURL)
	entries := host.OAuthModelAlias[providerName]
	if len(entries) == 0 {
		// Host may key the channel case-insensitively; fall back to a scan.
		for channel, list := range host.OAuthModelAlias {
			if strings.EqualFold(strings.TrimSpace(channel), providerName) {
				entries = list
				break
			}
		}
	}
	byAlias := make(map[string]string, len(entries))
	for _, e := range entries {
		name := strings.TrimSpace(e.Name)
		alias := strings.TrimSpace(e.Alias)
		if name == "" || alias == "" || strings.EqualFold(name, alias) {
			continue
		}
		byAlias[strings.ToLower(alias)] = name
	}
	modelAliasCache.Lock()
	modelAliasCache.byAlias = byAlias
	modelAliasCache.Unlock()
}

// resolveUpstreamModel maps an aliased requested model back to the real
// upstream model ID. Returns the input unchanged when nothing matches.
func resolveUpstreamModel(model string, attributes map[string]string) string {
	m := strings.TrimSpace(model)
	if m == "" {
		return model
	}
	key := strings.ToLower(m)
	if name, ok := parseModelAliasAttribute(attributes)[key]; ok {
		return name
	}
	modelAliasCache.RLock()
	name, ok := modelAliasCache.byAlias[key]
	modelAliasCache.RUnlock()
	if ok {
		return name
	}
	return m
}

// parseModelAliasAttribute decodes a per-auth alias override from auth
// attributes. Accepts JSON ([{"name":...,"alias":...}] or {alias:name}) or
// comma-separated "alias=name" pairs.
func parseModelAliasAttribute(attributes map[string]string) map[string]string {
	if len(attributes) == 0 {
		return nil
	}
	raw := ""
	for _, k := range []string{"model_alias", "model-alias", "oauth-model-alias"} {
		if v := strings.TrimSpace(attributes[k]); v != "" {
			raw = v
			break
		}
	}
	if raw == "" {
		return nil
	}
	out := make(map[string]string)
	add := func(name, alias string) {
		name, alias = strings.TrimSpace(name), strings.TrimSpace(alias)
		if name != "" && alias != "" && !strings.EqualFold(name, alias) {
			out[strings.ToLower(alias)] = name
		}
	}
	if strings.HasPrefix(raw, "[") {
		var list []struct {
			Name  string `json:"name"`
			Alias string `json:"alias"`
		}
		if json.Unmarshal([]byte(raw), &list) == nil {
			for _, e := range list {
				add(e.Name, e.Alias)
			}
			return out
		}
	}
	if strings.HasPrefix(raw, "{") {
		var m map[string]string
		if json.Unmarshal([]byte(raw), &m) == nil {
			for alias, name := range m {
				add(name, alias)
			}
			return out
		}
	}
	for _, pair := range strings.Split(raw, ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			add(kv[1], kv[0])
		}
	}
	return out
}

// filterExcludedModels removes models listed in oauth-excluded-models for
// the qoderwork provider. The host passes this config via HostConfigSummary.
func filterExcludedModels(models []pluginapi.ModelInfo, host pluginapi.HostConfigSummary) []pluginapi.ModelInfo {
	if len(host.ExcludedModels) == 0 {
		return models
	}
	return applyExcludedSet(models, excludedModelsForKeys(host, providerName))
}

// excludedModelsForKeys resolves the UNION of the oauth-excluded-models
// lists for keys, each via exact match then a case-insensitive scan (the
// host lowercases keys before the plugin ever sees them, but hand-written
// YAML may drift). Deduplicated, lowercased, order-preserving.
func excludedModelsForKeys(host pluginapi.HostConfigSummary, keys ...string) []string {
	if len(host.ExcludedModels) == 0 || len(keys) == 0 {
		return nil
	}
	var out []string
	seen := make(map[string]struct{})
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		list := host.ExcludedModels[key]
		if len(list) == 0 {
			for channel, l := range host.ExcludedModels {
				if strings.EqualFold(strings.TrimSpace(channel), key) {
					list = l
					break
				}
			}
		}
		for _, m := range list {
			id := strings.ToLower(strings.TrimSpace(m))
			if id == "" {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

// applyExcludedSet is the shared filter tail. Fresh output slice — never
// filter in place: models may alias the dynamicModelsCache's own slice
// (P0: one in-place filter would corrupt the cache for the next fetch).
// IDs match case-insensitively; patterns are exact ids here — wildcard
// patterns are the HOST's applyExcludedModels concern.
func applyExcludedSet(models []pluginapi.ModelInfo, excluded []string) []pluginapi.ModelInfo {
	if len(excluded) == 0 || len(models) == 0 {
		return models
	}
	excludeSet := make(map[string]struct{}, len(excluded))
	for _, m := range excluded {
		excludeSet[m] = struct{}{}
	}
	out := make([]pluginapi.ModelInfo, 0, len(models))
	for _, m := range models {
		if _, skip := excludeSet[strings.ToLower(m.ID)]; skip {
			continue
		}
		out = append(out, m)
	}
	return out
}

// filterExcludedModelsForRegion adds the region sub-key on top of the
// provider-key filter (v0.8.26): "qoder-cn" / "qoder-intl" manage one
// region each from the panel's global oauth-excluded-models page (any key
// present in config shows up in the page's provider dropdown), while the
// bare "qoder" key keeps excluding across both regions. Empty region
// (no credential context) degrades to the provider key alone.
func filterExcludedModelsForRegion(models []pluginapi.ModelInfo, host pluginapi.HostConfigSummary, region string) []pluginapi.ModelInfo {
	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		return filterExcludedModels(models, host)
	}
	return applyExcludedSet(models, excludedModelsForKeys(host, providerName, providerName+"-"+region))
}

// publishUsage reports one upstream attempt into CPAMP request monitoring.
// requestedModel is client-facing (may be alias); upstreamModel is resolved.

func handleModelStatic(raw []byte) ([]byte, error) {
	var req pluginapi.StaticModelRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	cacheModelAliases(req.Host)
	models := fetchDynamicModels()
	models = filterExcludedModels(models, req.Host)
	return okEnvelope(pluginapi.ModelResponse{Provider: providerName, Models: models})
}

func handleModelForAuth(raw []byte) ([]byte, error) {
	var req pluginapi.AuthModelRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	// Always return the plugin's canonical provider key. The host skips any
	// response whose Provider doesn't match the auth's provider, so echoing
	// req.AuthProvider back would silently drop the model list whenever the
	// auth file carries a non-canonical provider string.
	cacheModelAliases(req.Host)
	models := fetchDynamicModelsFromStorage(req.StorageJSON)
	// v0.8.26: filter at BOTH granularities — the provider key (whole
	// plugin) and this credential's region sub-key (channel-scoped).
	if sa, perr := parseStored(req.StorageJSON); perr == nil && sa != nil {
		models = filterExcludedModelsForRegion(models, req.Host, authRegion(sa))
	} else {
		models = filterExcludedModels(models, req.Host)
	}
	return okEnvelope(pluginapi.ModelResponse{Provider: providerName, Models: models})
}

package qoder

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	_ "embed"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

//go:embed qoder_auth.wasm
var qoderAuthWASM []byte

// inferPrepared is one desktop prepareInferRequest result.
type inferPrepared struct {
	URL     string
	Body    string
	Headers map[string]string
}

// prepareInferFn is the chat wire preparer. Tests may wrap the real function
// to observe the single call the executor posts.
var prepareInferFn = prepareInferChat

// prepareInferChat runs prepareInferRequest for an agent body whose
// model_config.key is modelKey. The account's COSY key and encrypted info are
// the signature material the gateway accepts; the wasm copies them into the
// bearer and sets X-Model-Key from modelKey.
func prepareInferChat(sa *storedAuth, body []byte, modelKey string) (*http.Request, error) {
	sess, err := cosySessionFor(sa)
	if err != nil {
		return nil, err
	}
	user, err := json.Marshal(map[string]any{
		"uid":                sa.Account.UID,
		"encrypt_user_info":  sess.Info,
		"key":                sess.CosyKey,
		"organization_id":    "",
		"organization_tags":  []any{},
		"data_policy_agreed": true,
	})
	if err != nil {
		return nil, err
	}
	client, err := json.Marshal(map[string]string{
		"client_type":      "5",
		"business_product": "cli",
		"business_type":    "agent",
		"scene":            "assistant",
	})
	if err != nil {
		return nil, err
	}
	prep, err := callPrepareInfer(gatewayBaseFor(sa), sess.MachineID, "1.1.86-hotfix.19", string(user), string(client), string(body), modelKey, "system")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, prep.URL, strings.NewReader(prep.Body))
	if err != nil {
		return nil, err
	}
	for k, v := range prep.Headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

var extNull = &struct{}{}

type extHeap struct {
	slots []any
	free  []int32
}

func newExtHeap() *extHeap {
	h := &extHeap{slots: make([]any, 1028)}
	h.slots[1025] = extNull
	h.slots[1026] = true
	h.slots[1027] = false
	return h
}

func (h *extHeap) alloc(v any) int32 {
	if n := len(h.free); n > 0 {
		i := h.free[n-1]
		h.free = h.free[:n-1]
		h.slots[i] = v
		return i
	}
	h.slots = append(h.slots, v)
	return int32(len(h.slots) - 1)
}

func (h *extHeap) get(i int32) any {
	if i <= 0 || int(i) >= len(h.slots) {
		return nil
	}
	return h.slots[i]
}

func (h *extHeap) drop(i int32) {
	if i < 1028 || int(i) >= len(h.slots) {
		return
	}
	h.slots[i] = nil
	h.free = append(h.free, i)
}

type wasmEnv struct {
	mu   sync.Mutex
	rt   wazero.Runtime
	mod  api.Module
	heap *extHeap
	nf   uint32
	ctx  context.Context
	fail error
}

var authWASM wasmEnv

func (e *wasmEnv) ensure(ctx context.Context) error {
	if e.mod != nil {
		e.fail = nil
		e.ctx = ctx
		return nil
	}
	e.ctx = ctx
	e.heap = newExtHeap()
	e.rt = wazero.NewRuntime(ctx)
	compiled, err := e.rt.CompileModule(ctx, qoderAuthWASM)
	if err != nil {
		return err
	}
	host := e.rt.NewHostModuleBuilder("./qoder_auth_wasm_bg.js")
	e.bind(host)
	if _, err := host.Instantiate(ctx); err != nil {
		return err
	}
	e.mod, err = e.rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName("qoder_auth"))
	return err
}

func (e *wasmEnv) bind(b wazero.HostModuleBuilder) {
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, _ api.Module, i uint32) { e.heap.drop(int32(i)) }).Export("__wbindgen_object_drop_ref")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, _ api.Module, a, k, v uint32) uint32 {
		m, _ := e.heap.get(int32(a)).(map[string]string)
		if m == nil {
			m = map[string]string{}
		}
		key, _ := e.heap.get(int32(k)).(string)
		val, _ := e.heap.get(int32(v)).(string)
		m[key] = val
		return uint32(e.heap.alloc(m))
	}).Export("__wbg_set_08463b1df38a7e29")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, m api.Module, ptr, n uint32) {
		buf := make([]byte, n)
		_, _ = rand.Read(buf)
		_ = m.Memory().Write(ptr, buf)
	}).Export("__wbg_getRandomValues_d49329ff89a07af1")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, _ api.Module, i uint32) uint32 {
		return uint32(e.heap.alloc(struct{ crypto bool }{}))
	}).Export("__wbg_crypto_38df2bab126b63dc")
	b.NewFunctionBuilder().WithFunc(func(context.Context, api.Module, uint32) uint32 { return 0 }).Export("__wbg_process_44c7a14e11e9f69e")
	b.NewFunctionBuilder().WithFunc(func(context.Context, api.Module, uint32) uint32 { return 0 }).Export("__wbg_versions_276b2795b1c6a219")
	b.NewFunctionBuilder().WithFunc(func(context.Context, api.Module, uint32) uint32 { return 0 }).Export("__wbg_node_84ea875411254db1")
	b.NewFunctionBuilder().WithFunc(func(context.Context, api.Module) uint32 { return 0 }).Export("__wbg_require_b4edbdcf3e2a1ef0")
	b.NewFunctionBuilder().WithFunc(func(context.Context, api.Module, uint32) uint32 { return 0 }).Export("__wbg_msCrypto_bd5a034af96bcba6")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, _ api.Module, _, buf uint32) {
		if b, ok := e.heap.get(int32(buf)).([]byte); ok {
			_, _ = rand.Read(b)
		}
	}).Export("__wbg_getRandomValues_c44a50d8cfdaebeb")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, _ api.Module, _, buf uint32) {
		if b, ok := e.heap.get(int32(buf)).([]byte); ok {
			_, _ = rand.Read(b)
		}
	}).Export("__wbg_randomFillSync_6c25eac9869eb53c")
	b.NewFunctionBuilder().WithFunc(func(context.Context, api.Module, uint32, uint32, uint32) uint32 { return 0 }).Export("__wbg_call_d578befcc3145dee")
	b.NewFunctionBuilder().WithFunc(func(context.Context, api.Module) uint32 {
		return uint32(e.heap.alloc(map[string]string{}))
	}).Export("__wbg_new_99cabae501c0a8a0")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, _ api.Module) float64 {
		return float64(time.Now().UnixMilli())
	}).Export("__wbg_now_88621c9c9a4f3ffc")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, _ api.Module, i uint32) uint32 {
		return uint32(e.heap.alloc(e.heap.get(int32(i))))
	}).Export("__wbindgen_object_clone_ref")
	b.NewFunctionBuilder().WithFunc(func(context.Context, api.Module) uint32 { return uint32(e.heap.alloc(struct{}{})) }).Export("__wbg_static_accessor_GLOBAL_THIS_a1248013d790bf5f")
	b.NewFunctionBuilder().WithFunc(func(context.Context, api.Module) uint32 { return 0 }).Export("__wbg_static_accessor_SELF_24f78b6d23f286ea")
	b.NewFunctionBuilder().WithFunc(func(context.Context, api.Module) uint32 { return 0 }).Export("__wbg_static_accessor_WINDOW_59fd959c540fe405")
	b.NewFunctionBuilder().WithFunc(func(context.Context, api.Module) uint32 { return uint32(e.heap.alloc(struct{}{})) }).Export("__wbg_static_accessor_GLOBAL_f2e0f995a21329ff")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, m api.Module, dst, n, src uint32) {
		b, _ := e.heap.get(int32(src)).([]byte)
		if b == nil {
			return
		}
		if uint32(len(b)) < n {
			n = uint32(len(b))
		}
		_ = m.Memory().Write(dst, b[:n])
	}).Export("__wbg_prototypesetcall_3e05eb9545565046")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, _ api.Module, n uint32) uint32 {
		return uint32(e.heap.alloc(make([]byte, n)))
	}).Export("__wbg_new_with_length_9cedd08484b73942")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, _ api.Module, i, start, end uint32) uint32 {
		b, _ := e.heap.get(int32(i)).([]byte)
		if b == nil {
			return uint32(e.heap.alloc([]byte{}))
		}
		if int(end) > len(b) {
			end = uint32(len(b))
		}
		if start > end {
			start = end
		}
		return uint32(e.heap.alloc(b[start:end]))
	}).Export("__wbg_subarray_0f98d3fb634508ad")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, _ api.Module, i uint32) uint32 {
		switch v := e.heap.get(int32(i)).(type) {
		case string:
			return uint32(len(v))
		case []byte:
			return uint32(len(v))
		case map[string]string:
			return uint32(len(v))
		default:
			return 0
		}
	}).Export("__wbg_length_0c32cb8543c8e4c8")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, m api.Module, ptr, n uint32) uint32 {
		s := e.readStr(m, ptr, n)
		return uint32(e.heap.alloc(fmt.Errorf("%s", s)))
	}).Export("__wbg_Error_2e59b1b37a9a34c3")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, _ api.Module, i uint32) uint32 {
		if e.heap.get(int32(i)) == nil {
			return 1
		}
		return 0
	}).Export("__wbg___wbindgen_is_undefined_c0cca72b82b86f4d")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, _ api.Module, i uint32) uint32 {
		v := e.heap.get(int32(i))
		switch v.(type) {
		case nil, string, bool:
			return 0
		default:
			if v == nil {
				return 0
			}
			return 1
		}
	}).Export("__wbg___wbindgen_is_object_40c5a80572e8f9d3")
	b.NewFunctionBuilder().WithFunc(func(context.Context, api.Module, uint32) uint32 { return 0 }).Export("__wbg___wbindgen_is_function_49868bde5eb1e745")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, _ api.Module, i uint32) uint32 {
		_, ok := e.heap.get(int32(i)).(string)
		if ok {
			return 1
		}
		return 0
	}).Export("__wbg___wbindgen_is_string_b29b5c5a8065ba1a")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, m api.Module, ptr, n uint32) {
		panic(e.readStr(m, ptr, n))
	}).Export("__wbg___wbindgen_throw_81fc77679af83bc6")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, m api.Module, ptr, n uint32) uint32 {
		b, ok := m.Memory().Read(ptr, n)
		if !ok {
			return 0
		}
		cp := append([]byte(nil), b...)
		return uint32(e.heap.alloc(cp))
	}).Export("__wbindgen_cast_0000000000000001")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, m api.Module, ptr, n uint32) uint32 {
		return uint32(e.heap.alloc(e.readStr(m, ptr, n)))
	}).Export("__wbindgen_cast_0000000000000002")
}

func (e *wasmEnv) readStr(m api.Module, ptr, n uint32) string {
	b, ok := m.Memory().Read(ptr, n)
	if !ok {
		return ""
	}
	return string(b)
}

func (e *wasmEnv) pass(m api.Module, s string) (uint32, uint32, error) {
	b := []byte(s)
	res, err := m.ExportedFunction("__wbindgen_export2").Call(e.ctx, uint64(len(b)), 1)
	if err != nil {
		return 0, 0, err
	}
	ptr := uint32(res[0])
	if len(b) > 0 {
		if !m.Memory().Write(ptr, b) {
			return 0, 0, fmt.Errorf("wasm memory write")
		}
	}
	e.nf = uint32(len(b))
	return ptr, e.nf, nil
}

func (e *wasmEnv) sp(m api.Module, delta int32) (uint32, error) {
	res, err := m.ExportedFunction("__wbindgen_add_to_stack_pointer").Call(e.ctx, uint64(uint32(delta)))
	if err != nil {
		return 0, err
	}
	return uint32(res[0]), nil
}

func (e *wasmEnv) i32(m api.Module, addr uint32) uint32 {
	v, _ := m.Memory().ReadUint32Le(addr)
	return v
}

func callPrepareInfer(origin, machineID, version, userInfo, clientInfo, body, modelKey, modelSource string) (inferPrepared, error) {
	ctx := context.Background()
	e := &authWASM
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.ensure(ctx); err != nil {
		return inferPrepared{}, err
	}
	m := e.mod
	strs := []string{machineID, version, userInfo, clientInfo}
	ptrs := make([]uint32, 4)
	lens := make([]uint32, 4)
	for i, s := range strs {
		p, n, err := e.pass(m, s)
		if err != nil {
			return inferPrepared{}, err
		}
		ptrs[i], lens[i] = p, n
	}
	sp, err := e.sp(m, -16)
	if err != nil {
		return inferPrepared{}, err
	}
	_, err = m.ExportedFunction("qodercontext_new").Call(ctx,
		uint64(sp),
		uint64(ptrs[0]), uint64(lens[0]),
		uint64(ptrs[1]), uint64(lens[1]),
		uint64(ptrs[2]), uint64(lens[2]),
		uint64(ptrs[3]), uint64(lens[3]),
	)
	if err != nil {
		_, _ = e.sp(m, 16)
		return inferPrepared{}, err
	}
	if e.i32(m, sp+8) != 0 {
		msg := e.errObj(e.i32(m, sp+4))
		_, _ = e.sp(m, 16)
		return inferPrepared{}, fmt.Errorf("qodercontext_new: %s", msg)
	}
	qctx := e.i32(m, sp)
	_, _ = e.sp(m, 16)

	args := []string{origin, body, modelKey, modelSource}
	ap := make([]uint32, 4)
	an := make([]uint32, 4)
	for i, s := range args {
		p, n, err := e.pass(m, s)
		if err != nil {
			return inferPrepared{}, err
		}
		ap[i], an[i] = p, n
	}
	sp, err = e.sp(m, -16)
	if err != nil {
		return inferPrepared{}, err
	}
	_, err = m.ExportedFunction("qodercontext_prepareInferRequest").Call(ctx,
		uint64(sp), uint64(qctx),
		uint64(ap[0]), uint64(an[0]),
		uint64(ap[1]), uint64(an[1]),
		uint64(ap[2]), uint64(an[2]),
		uint64(ap[3]), uint64(an[3]),
	)
	if err != nil {
		_, _ = e.sp(m, 16)
		return inferPrepared{}, err
	}
	if e.i32(m, sp+8) != 0 {
		msg := e.errObj(e.i32(m, sp+4))
		_, _ = e.sp(m, 16)
		return inferPrepared{}, fmt.Errorf("prepareInferRequest: %s", msg)
	}
	resPtr := e.i32(m, sp)
	_, _ = e.sp(m, 16)

	prep, err := e.readResult(m, resPtr)
	_, _ = m.ExportedFunction("__wbg_qodercontext_free").Call(ctx, uint64(qctx), 0)
	_, _ = m.ExportedFunction("__wbg_requestresult_free").Call(ctx, uint64(resPtr), 0)
	return prep, err
}

func (e *wasmEnv) errObj(i uint32) string {
	switch v := e.heap.get(int32(i)).(type) {
	case error:
		return v.Error()
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func (e *wasmEnv) readOwned(m api.Module, sp uint32) (string, error) {
	ptr := e.i32(m, sp)
	n := e.i32(m, sp+4)
	if ptr == 0 {
		return "", nil
	}
	s := e.readStr(m, ptr, n)
	_, err := m.ExportedFunction("__wbindgen_export4").Call(e.ctx, uint64(ptr), uint64(n), 1)
	return s, err
}

func (e *wasmEnv) readResult(m api.Module, res uint32) (inferPrepared, error) {
	sp, err := e.sp(m, -16)
	if err != nil {
		return inferPrepared{}, err
	}
	if _, err := m.ExportedFunction("requestresult_url").Call(e.ctx, uint64(sp), uint64(res)); err != nil {
		_, _ = e.sp(m, 16)
		return inferPrepared{}, err
	}
	url, err := e.readOwned(m, sp)
	_, _ = e.sp(m, 16)
	if err != nil {
		return inferPrepared{}, err
	}
	sp, err = e.sp(m, -16)
	if err != nil {
		return inferPrepared{}, err
	}
	if _, err := m.ExportedFunction("requestresult_body").Call(e.ctx, uint64(sp), uint64(res)); err != nil {
		_, _ = e.sp(m, 16)
		return inferPrepared{}, err
	}
	body, err := e.readOwned(m, sp)
	_, _ = e.sp(m, 16)
	if err != nil {
		return inferPrepared{}, err
	}
	hdrIdx, err := m.ExportedFunction("requestresult_headers").Call(e.ctx, uint64(res))
	if err != nil {
		return inferPrepared{}, err
	}
	headers := map[string]string{}
	if mp, ok := e.heap.get(int32(hdrIdx[0])).(map[string]string); ok {
		for k, v := range mp {
			headers[k] = v
		}
	}
	e.heap.drop(int32(hdrIdx[0]))
	return inferPrepared{URL: url, Body: body, Headers: headers}, nil
}

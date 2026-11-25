// Package main is the c-shared ABI shell for the Qoder CLIProxyAPI plugin.
//
// cgo exports and -buildmode=c-shared have to live in package main, and the
// release ldflags (-X main.version, -X main.providerName, -X main.pluginVariantPin)
// stay here so existing build commands keep working. The plugin itself is
// package qoder.
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
        void* ptr;
        size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
        uint32_t abi_version;
        void* host_ctx;
        cliproxy_host_call_fn call;
        cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
        uint32_t abi_version;
        cliproxy_plugin_call_fn call;
        cliproxy_plugin_free_fn free_buffer;
        cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

// Wrappers so Go can invoke the host function-pointer table via cgo. The host
// API captured at init is used to push streaming chunks back asynchronously.
static int wb_call_host(cliproxy_host_api* api, const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
        return api->call(api->host_ctx, method, request, request_len, response);
}
static void wb_free_host_buffer(cliproxy_host_api* api, void* ptr, size_t len) {
        api->free_buffer(ptr, len);
}

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/M1nt-Ch0c0/CLIProxyAPI-Qoder-Plugin/internal/qoder"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

// version is injected at build time via -ldflags "-X main.version=...".
// It must track the VERSION file when the flag is absent (version_guard_test).
// SetBuild copies it into the plugin before the host calls in.
var version = "0.8.77"

// providerName is a VAR (split flavor, issue #29): the unified build keeps
// "qoder"; split-channel builds rename it via
// -ldflags "-X main.providerName=qoder-intl".
var providerName = "qoder"

// pluginVariantPin is the build-time realm pin ("cn" / "intl"); empty for
// the unified flavor. -ldflags "-X main.pluginVariantPin=intl".
var pluginVariantPin = ""

func init() {
	qoder.SetBuild(version, providerName, pluginVariantPin)
}

func main() {}

var hostAPI *C.cliproxy_host_api

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	hostAPI = host
	if host != nil && host.call != nil {
		qoder.SetHost(callHost)
	}
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, qoder.ErrorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, errHandle := qoder.Handle(C.GoString(method), requestBytes)
	if errHandle != nil {
		writeResponse(response, qoder.ErrorEnvelopeFor(errHandle))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, len C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	// Intentionally a no-op. The host calls this on its own exit path (after
	// the host Go runtime has started tearing down) and dlclose()es this
	// library immediately afterwards. Touching any Go runtime state here —
	// mutexes, channel close, goroutine synchronization — risks a SIGSEGV in
	// cgo (observed on every docker restart: SIGSEGV in
	// _Cfunc_cliproxy_shutdown_plugin, PC near a freed runtime pointer).
	// The scheduler goroutine and janitor ticker hold no resources that
	// outlive the process; the OS reclaims them on exit.
}

// callHost invokes a host RPC method via the function-pointer table captured
// at init. Used to push stream chunks back asynchronously (host.stream.emit /
// host.stream.close) and to read the host's auth store (host.auth.list/get).
func callHost(method string, request []byte) ([]byte, error) {
	if hostAPI == nil || hostAPI.call == nil {
		return nil, fmt.Errorf("host API unavailable")
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	var cReq unsafe.Pointer
	var reqLen C.size_t
	if len(request) > 0 {
		cReq = C.CBytes(request)
		defer C.free(cReq)
		reqLen = C.size_t(len(request))
	}
	var resp C.cliproxy_buffer
	rc := C.wb_call_host(hostAPI, cMethod, (*C.uint8_t)(cReq), reqLen, &resp)
	var out []byte
	if resp.ptr != nil && resp.len > 0 {
		out = C.GoBytes(resp.ptr, C.int(resp.len))
	}
	if resp.ptr != nil && hostAPI.free_buffer != nil {
		C.wb_free_host_buffer(hostAPI, resp.ptr, resp.len)
	}
	if rc != 0 {
		return out, fmt.Errorf("host call %s returned %d", method, int(rc))
	}
	return out, nil
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}

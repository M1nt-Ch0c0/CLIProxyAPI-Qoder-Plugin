package qoder

import "fmt"

// hostCall is the host RPC bridge. The ABI shell installs the real cgo
// implementation with SetHost; until then (unit tests, direct calls) every
// host RPC fails and HTTP falls back to the direct client.
var hostCall = func(method string, request []byte) ([]byte, error) {
	return nil, fmt.Errorf("host API unavailable")
}

// hostBound is true once SetHost has installed a live bridge. Tests leave it
// false so billing and chat use the direct HTTP client.
var hostBound bool

// SetHost installs the cgo host bridge. The ABI shell calls it from
// cliproxy_plugin_init, before any RPC.
func SetHost(fn func(method string, request []byte) ([]byte, error)) {
	if fn == nil {
		return
	}
	hostCall = fn
	hostBound = true
}

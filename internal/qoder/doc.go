// Package qoder is the Qoder CLIProxyAPI plugin.
//
// The root package main is only the c-shared ABI (cgo exports plus the
// ldflags injection points). This package is the plugin: one credential
// state, one host bridge, one RPC dispatcher.
//
// The files stay in one package because the calls form a mesh — check-in
// calls campaigns, chat calls the scheduler, the scheduler calls auth files.
// Splitting that mesh into packages would export the private surface and
// add import cycles without making any path shorter. The file names are
// the modules:
//
//	auth*, oauth, adopt, expiry, cosy_session, active_auth
//	    credentials, device login, auth-file IO
//	billing*, campaign*, checkin*, cache, round_state, credits_handler
//	    quotas, campaigns, daily check-in
//	body*, stream, completions*, image_upload, schema_typeunions, queue_retry
//	    chat wire format and streaming
//	models*, inference_discovery
//	    model catalog
//	machine_identity, umid_provision, sign, encoding
//	    device identity and COSY signing
//	host*, proxy_policy, redact
//	    host RPC and the HTTP bridge
//	lifecycle, policy, cooldown, keepalive, scheduler
//	    account lifecycle and selection
//	management, panel, usage*
//	    management API, panel, usage report
//	region, configparse, split_build, plugin
//	    region routing, config, registration and RPC dispatch
package qoder

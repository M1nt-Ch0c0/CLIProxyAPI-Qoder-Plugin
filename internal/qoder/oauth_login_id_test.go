package qoder

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// The host upserts auth records by ID and only derives the path key
// (authIDForPath) when AuthData.ID is empty. A login-success return carrying
// ID=uid therefore landed beside the watcher's path-keyed record: two entries
// for one file. handleParseAuth and toAuthDataForRefresh already blank ID;
// these tests pin the login paths to the same rule.
func TestLoginSuccessAuthLeavesIDEmpty(t *testing.T) {
	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "dt-login-test", Region: "cn"},
		Account: storedAccount{UID: "u1"},
	}
	ad := toAuthDataForLogin(sa)
	if ad.ID != "" {
		t.Fatalf("login AuthData.ID = %q, want empty so host derives the path key", ad.ID)
	}
	if ad.FileName != "qoder-cn-u1.json" {
		t.Fatalf("login AuthData.FileName = %q, want canonical name kept for the host write", ad.FileName)
	}
}

func TestPollLoginPanelImportEchoesFileNameAndClearsID(t *testing.T) {
	origList, origGet := hostAuthListFn, hostAuthGetPhysicalFn
	origPoll := pollDeviceTokenFn
	t.Cleanup(func() {
		hostAuthListFn, hostAuthGetPhysicalFn, pollDeviceTokenFn = origList, origGet, origPoll
	})
	pollDeviceTokenFn = func(string, string, string) (*deviceTokenResponse, bool, error) {
		return nil, true, nil
	}

	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "dt-login-test", Region: "cn"},
		Account: storedAccount{UID: "u1"},
	}
	raw, err := json.Marshal(sa)
	if err != nil {
		t.Fatalf("marshal stored auth: %v", err)
	}
	// Deliberately NOT the recomputed canonical name: the record already
	// exists under the host's name, echoing anything else rename-dupes.
	const hostName = "qoder-cn-legacy.json"
	hostAuthListFn = func() ([]pluginapi.HostAuthFileEntry, error) {
		return []pluginapi.HostAuthFileEntry{{
			AuthIndex: "idx-1",
			Name:      hostName,
			CreatedAt: time.Now(),
		}}, nil
	}
	hostAuthGetPhysicalFn = func(string) (*hostAuthPhysical, error) {
		return &hostAuthPhysical{AuthIndex: "idx-1", Name: hostName, JSON: raw}, nil
	}

	state := "test-panel-import-state"
	loginStates.Store(state, &loginCtx{
		region:    "cn",
		expires:   time.Now().Add(time.Minute),
		startedAt: time.Now().Add(-time.Hour).UnixNano(),
	})
	t.Cleanup(func() { loginStates.Delete(state) })

	out, err := handlePollLogin([]byte(`{"state":"` + state + `"}`))
	if err != nil {
		t.Fatalf("handlePollLogin: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		t.Fatalf("bad envelope: %v %s", err, out)
	}
	var resp pluginapi.AuthLoginPollResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatalf("decode poll response: %v", err)
	}
	if resp.Status != pluginapi.AuthLoginStatusSuccess {
		t.Fatalf("status = %q, want success", resp.Status)
	}
	if resp.Auth.ID != "" {
		t.Fatalf("poll login Auth.ID = %q, want empty (watcher upsert key is path-derived)", resp.Auth.ID)
	}
	if resp.Auth.FileName != hostName {
		t.Fatalf("poll login Auth.FileName = %q, want echoed host name %q", resp.Auth.FileName, hostName)
	}
}

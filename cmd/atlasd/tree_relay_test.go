package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"atlas/internal/daemon"
)

// The daemon sits between the hub and the UI. It once re-marshalled the tree
// through a typed struct that predated the native-channel fields, silently
// dropping native/id/template/hidden/source/channel_id/category_id: new
// categories never appeared and hidden chats showed as normal ones. The relay
// must hand the hub's JSON through untouched, including fields it has never
// heard of.

const treeFixture = `{"generated_at":1,"errors":[],"sections":[{"kind":"profile","name":"Nolan","profile":"default","children":[{"kind":"category","name":"Misc","id":"cat-1","native":true,"profile":"default","children":[{"kind":"channel","name":"c","id":"chan-1","native":true,"template":"t","category_id":"cat-1","children":[{"kind":"post","name":"p","session_id":"s1","hidden":true,"source":"atlas","channel_id":"chan-1","future_field":{"x":1}}]}]}]}]}`

const spawnedFixture = `{"items":[{"kind":"debbie","id":"debb-1","state":"done","parent":"s1","title":"t","future_field":"x"}],"errors":["e"]}`

// relayAPI points a real daemon.Service at a fake hub serving body on path.
func relayAPI(t *testing.T, path, body string) *api {
	t.Helper()
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path || (path == "/tree" && r.URL.Query().Get("include_hidden") != "1") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(hub.Close)
	t.Setenv("ATLAS_HUB_URL", hub.URL)
	t.Setenv("ATLAS_API_KEY", "test-key")
	t.Setenv("HERMES_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return &api{svc: daemon.New()}
}

func relayCheck(t *testing.T, h func(*api, http.ResponseWriter, *http.Request), a *api, url, want string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(a, rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got, exp any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("relay is not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &exp); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, exp) {
		t.Fatalf("relay altered the hub's payload (dropped or changed fields):\n got: %s\nwant: %s", rec.Body.String(), want)
	}
}

func TestTreeRelayIsVerbatim(t *testing.T) {
	a := relayAPI(t, "/tree", treeFixture)
	relayCheck(t, (*api).tree, a, "/api/tree", treeFixture)
}

func TestSpawnedRelayIsVerbatim(t *testing.T) {
	a := relayAPI(t, "/spawned", spawnedFixture)
	relayCheck(t, (*api).spawned, a, "/api/spawned", spawnedFixture)
}

package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"atlas/internal/hermes"
)

func TestSplitInput(t *testing.T) {
	txt, imgs, err := splitInput(json.RawMessage(`"hello"`))
	if err != nil || txt != "hello" || len(imgs) != 0 {
		t.Fatalf("plain string: %q %v %v", txt, imgs, err)
	}

	parts := `[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,QUJD"}},` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,REVG"}},{"type":"text","text":"  "}]`
	txt, imgs, err = splitInput(json.RawMessage(parts))
	if err != nil {
		t.Fatal(err)
	}
	if txt != "look" {
		t.Errorf("text = %q, want %q (blank parts dropped)", txt, "look")
	}
	if len(imgs) != 2 || imgs[0].b64 != "QUJD" || imgs[0].filename != "atlas-paste-1.jpg" || imgs[1].filename != "atlas-paste-2.png" {
		t.Errorf("images = %+v", imgs)
	}

	if _, _, err := splitInput(json.RawMessage(`[{"type":"image_url","image_url":{"url":"data:image/png,notbase64"}}]`)); err == nil {
		t.Error("non-base64 data URL should be refused")
	}
	if _, _, err := splitInput(json.RawMessage(`{"oops":1}`)); err == nil {
		t.Error("unsupported shape should be refused")
	}
}

// newTestService builds a Service without dialing anything.
func newTestService() *Service { return newBare() }

func ev(sid, typ string, seq int64, payload string) hermes.ServeEvent {
	return hermes.ServeEvent{Type: typ, SessionID: sid, Seq: seq, Payload: json.RawMessage(payload)}
}

func drain(ch <-chan TurnEvent) []TurnEvent {
	var out []TurnEvent
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		case <-time.After(20 * time.Millisecond):
			return out
		}
	}
}

func TestServeEventsDriveATurn(t *testing.T) {
	d := newTestService()
	ch, cancel := d.Subscribe()
	defer cancel()

	d.rtMu.Lock()
	d.bindRuntimeLocked("stored1", "rt1")
	d.rtMu.Unlock()
	turn := &activeTurn{profile: "default", runtime: "rt1", done: make(chan struct{}), lastEvent: time.Now()}
	d.turns["stored1"] = turn

	d.onServeEvent(ev("rt-unknown", "message.delta", 1, `{"text":"ignored"}`)) // someone else's session
	d.onServeEvent(ev("rt1", "reasoning.delta", 1, `{"text":"hmm"}`))          // reasoning streams as its own segment
	d.onServeEvent(ev("rt1", "message.delta", 2, `{"text":"Hel"}`))
	d.onServeEvent(ev("rt1", "message.delta", 2, `{"text":"DUPLICATE"}`)) // replay overlap: seq already seen
	d.onServeEvent(ev("rt1", "tool.start", 3, `{"name":"terminal","tool_id":"a"}`))
	d.onServeEvent(ev("rt1", "tool.complete", 4, `{"name":"terminal","tool_id":"a"}`))
	d.onServeEvent(ev("rt1", "message.delta", 5, `{"text":"lo"}`))

	snap := d.TurnState("stored1")
	if snap == nil {
		t.Fatal("turn should still be live")
	}
	if len(snap.Segments) != 4 || snap.Segments[0].Type != "reasoning" || snap.Segments[0].Text != "hmm" ||
		snap.Segments[1].Text != "Hel" || snap.Segments[2].State != "done" || snap.Segments[3].Text != "lo" {
		t.Fatalf("segments = %+v", snap.Segments)
	}

	d.onServeEvent(ev("rt1", "message.complete", 6, `{"text":"Hello","status":"complete"}`))
	if d.TurnState("stored1") != nil {
		t.Error("turn should be finished after message.complete")
	}
	// A late duplicate completion must not double-fire.
	d.onServeEvent(ev("rt1", "message.complete", 6, `{"status":"complete"}`))

	var kinds []string
	for _, e := range drain(ch) {
		kinds = append(kinds, e.Kind+":"+e.Text+e.Tool+e.ToolState)
	}
	want := []string{"reasoning:hmm", "delta:Hel", "tool:terminalrunning", "tool:terminaldone", "delta:lo", "done:"}
	if len(kinds) != len(want) {
		t.Fatalf("events = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("events = %v, want %v", kinds, want)
		}
	}
}

func TestInterruptedAndErroredTurns(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		wantStopped   bool
		wantErr       string
	}{
		{"interrupted", `{"status":"interrupted"}`, true, ""},
		{"error", `{"status":"error","error":"provider 400"}`, false, "provider 400"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestService()
			ch, cancel := d.Subscribe()
			defer cancel()
			d.rtMu.Lock()
			d.bindRuntimeLocked("s", "r")
			d.rtMu.Unlock()
			d.turns["s"] = &activeTurn{runtime: "r", done: make(chan struct{}), lastEvent: time.Now()}
			d.onServeEvent(ev("r", "message.complete", 1, tc.payload))
			var gotErr string
			var done *TurnEvent
			for _, e := range drain(ch) {
				e := e
				if e.Kind == "error" {
					gotErr = e.Error
				}
				if e.Kind == "done" {
					done = &e
				}
			}
			if done == nil || done.Stopped != tc.wantStopped || gotErr != tc.wantErr || done.OK != (tc.wantErr == "") {
				t.Fatalf("done=%+v err=%q", done, gotErr)
			}
		})
	}
}

func TestSessionInfoEmitsStats(t *testing.T) {
	d := newTestService()
	ch, cancel := d.Subscribe()
	defer cancel()
	d.rtMu.Lock()
	d.bindRuntimeLocked("s", "r")
	d.rtMu.Unlock()
	// session.info lands at settle, when the turn is already gone — it must
	// still relay (this is what feeds the tok/s readout).
	d.onServeEvent(ev("r", "session.info", 1, `{"model":"m1","provider":"p1","reasoning_effort":"high","usage":{"avg_tps":41.7,"avg_latency_s":2.3,"context_used":5000,"context_max":100000,"context_percent":5}}`))
	got := drain(ch)
	if len(got) != 2 || got[0].Kind != "info" || got[1].Kind != "stats" || got[1].TPS != 41.7 || got[1].Latency != 2.3 {
		t.Fatalf("events = %+v", got)
	}
	si := got[0].Info
	if si == nil || si.Model != "m1" || si.Provider != "p1" || si.Reasoning != "high" || si.CtxPct != 5 || si.TPS != 41.7 {
		t.Fatalf("info = %+v", si)
	}
	// a mid-turn usage tick without throughput keeps the last real reading
	d.onServeEvent(ev("r", "session.usage", 2, `{"usage":{"context_used":6000,"context_max":100000,"context_percent":6}}`))
	got = drain(ch)
	if len(got) != 1 || got[0].Info == nil || got[0].Info.TPS != 41.7 || got[0].Info.CtxPct != 6 {
		t.Fatalf("usage tick = %+v", got)
	}
}

func TestFailedTurnIsRemembered(t *testing.T) {
	d := newTestService()
	ch, cancel := d.Subscribe()
	defer cancel()
	d.rtMu.Lock()
	d.bindRuntimeLocked("s", "r")
	d.rtMu.Unlock()
	d.turns["s"] = &activeTurn{runtime: "r", done: make(chan struct{}), lastEvent: time.Now()}
	d.onServeEvent(ev("r", "message.complete", 1,
		`{"status":"error","error":"404 model","error_surface":{"code":"model_not_found","provider":"claude-sub","model":"deepseek-flash"}}`))
	f := d.LastFailure("s")
	if f == nil || f.Code != "model_not_found" || f.Error != "404 model (route: deepseek-flash · claude-sub)" {
		t.Fatalf("failure = %+v", f)
	}
	var done *TurnEvent
	for _, e := range drain(ch) {
		e := e
		if e.Kind == "done" {
			done = &e
		}
	}
	if done == nil || done.OK || done.Code != "model_not_found" || done.Error == "" {
		t.Fatalf("done = %+v", done)
	}
	// a later successful turn clears it
	d.turns["s"] = &activeTurn{runtime: "r", done: make(chan struct{}), lastEvent: time.Now()}
	d.onServeEvent(ev("r", "message.complete", 2, `{"status":"complete"}`))
	if d.LastFailure("s") != nil {
		t.Fatal("success should clear the remembered failure")
	}
}

func TestSlowSubscriberGetsResync(t *testing.T) {
	d := newTestService()
	ch, cancel := d.Subscribe()
	defer cancel()
	for i := 0; i < 2048+10; i++ { // overflow the buffer: frames drop, sub turns lossy
		d.emit(TurnEvent{Kind: "delta", SessionID: "s", Text: "x"})
	}
	for i := 0; i < 2048; i++ { // reader catches up
		<-ch
	}
	d.emit(TurnEvent{Kind: "done", SessionID: "s"})
	got := drain(ch)
	if len(got) != 2 || got[0].Kind != "resync" || got[1].Kind != "done" {
		t.Fatalf("after loss: %+v", got)
	}
}

func TestRouteDrift(t *testing.T) {
	mk := func(model, topModel, topProv, nestedProv string) *hermes.SessionRoute {
		return &hermes.SessionRoute{OK: true, Model: model,
			Top:    hermes.RouteSide{Model: topModel, Provider: topProv},
			Nested: hermes.RouteSide{Provider: nestedProv}}
	}
	for _, tc := range []struct {
		name                string
		rt                  *hermes.SessionRoute
		liveModel, liveProv string
		want                bool
	}{
		// the incident: Atlas picked deepseek-flash/deepseek, the Discord-era
		// nested route (claude-sub) won the resume -> 404 every turn
		{"atlas pick lost to nested", mk("deepseek-flash", "deepseek-flash", "deepseek", "claude-sub"), "deepseek-flash", "claude-sub", true},
		// the gateway wrote last (model column is its model): nested is the truth
		{"gateway wrote last", mk("gpt-6.1-sol", "deepseek-flash", "deepseek", "openai-codex"), "gpt-6.1-sol", "openai-codex", false},
		// routes agree
		{"consistent", mk("m", "m", "p", "p"), "m", "p", false},
		// live already on the picked provider (resume did the right thing)
		{"live already right", mk("m", "m", "p", "q"), "m", "p", false},
		// no nested route at all (pure Atlas/desktop chat)
		{"no nested", mk("m", "m", "p", ""), "m", "p", false},
		// no top-level pick (pure gateway chat)
		{"no top", mk("m", "", "", "q"), "m", "q", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, got := routeDrift(tc.rt, tc.liveModel, tc.liveProv)
			if got != tc.want {
				t.Fatalf("drift = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRuntimeRebindForgetsOldWatermark(t *testing.T) {
	d := newTestService()
	d.rtMu.Lock()
	d.bindRuntimeLocked("s", "old")
	d.lastSeq["old"] = 99
	d.bindRuntimeLocked("s", "new") // serve minted a new runtime (restart / reap)
	d.rtMu.Unlock()
	if d.byRuntime["old"] != "" || d.lastSeq["old"] != 0 || d.byRuntime["new"] != "s" || d.runtimes["s"] != "new" {
		t.Fatalf("rebind left stale state: %+v %+v %+v", d.byRuntime, d.lastSeq, d.runtimes)
	}
}

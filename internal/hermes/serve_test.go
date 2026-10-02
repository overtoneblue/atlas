package hermes

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// TestServeLive exercises the hermes-serve command surface end-to-end
// against a live local service. Opt-in: ATLAS_SERVE_LIVE=1.
//
//	ATLAS_SERVE_LIVE=1 ATLAS_TEST_SESSION=<idle-session-id> \
//	  go test ./internal/hermes -run TestServeLive -v -count=1
func TestServeLive(t *testing.T) {
	if os.Getenv("ATLAS_SERVE_LIVE") == "" {
		t.Skip("set ATLAS_SERVE_LIVE=1 to run against a live hermes-serve")
	}
	s := NewServeFromEnv()
	if !s.Configured() {
		t.Fatal("no serve token resolved")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cat, err := s.Catalog(ctx)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	t.Logf("catalog: pairs=%d categories=%d skills=%d url=%s", len(cat.Pairs), len(cat.Categories), len(cat.Skills), s.BaseURL)
	if len(cat.Pairs) == 0 {
		t.Error("catalog empty")
	}
	found := false
	for _, p := range cat.Pairs {
		if len(p) > 0 && p[0] == "/model" {
			found = true
		}
	}
	if !found {
		t.Error("catalog missing /model")
	}

	items, rf, err := s.CompleteSlash(ctx, "/mo", "")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if rf != 1 {
		t.Errorf("command-stage replace_from = %d, want 1", rf)
	}
	// argument stage: hermes knows a command's argument words
	args, arf, err := s.CompleteSlash(ctx, "/reasoning ", "")
	if err != nil {
		t.Fatalf("complete args: %v", err)
	}
	if arf != len("/reasoning ") || len(args) == 0 {
		t.Errorf("argument stage: rf=%d items=%d, want rf=%d and some items", arf, len(args), len("/reasoning "))
	}
	texts := make([]string, 0, len(items))
	for _, it := range items {
		texts = append(texts, it.Text)
	}
	t.Logf("complete /mo -> %s", strings.Join(texts, " "))
	if len(items) == 0 {
		t.Error("no completions for /mo")
	}

	sid := os.Getenv("ATLAS_TEST_SESSION")
	if sid == "" {
		t.Log("ATLAS_TEST_SESSION unset; skipping exec")
		return
	}

	res, err := s.ExecSlash(ctx, sid, "/help")
	var rpc *RPCError
	if errors.As(err, &rpc) && rpc.Code == 4001 {
		t.Logf("exec: session not live (4001); attempting resume")
		runtime, rerr := s.Resume(ctx, sid)
		if rerr != nil {
			t.Fatalf("resume: %v", rerr)
		}
		t.Logf("resume -> runtime=%q", runtime)
		if runtime == "" {
			t.Fatal("resume returned no runtime session id")
		}
		res, err = s.ExecSlash(ctx, runtime, "/help")
	}
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.Output == "" {
		t.Errorf("exec returned no output: %+v", res)
	}
	if len(res.Output) > 200 {
		t.Logf("exec /help output (%.200s...)", res.Output)
	} else {
		t.Logf("exec /help output: %s", res.Output)
	}
}

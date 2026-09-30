package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The restart endpoint's three guards: header, supervision, live turns.
func TestRestartDaemon(t *testing.T) {
	cases := []struct {
		name       string
		header     bool
		supervised bool
		turns      int
		body       string
		want       int
		restarts   bool
	}{
		{"no header is refused", false, true, 0, "", http.StatusForbidden, false},
		{"unsupervised daemon refuses", true, false, 0, "", http.StatusConflict, false},
		{"live turns refuse", true, true, 2, "", http.StatusConflict, false},
		{"live turns + malformed body still refuse", true, true, 1, "{nope", http.StatusConflict, false},
		{"idle + supervised restarts", true, true, 0, "", http.StatusOK, true},
		{"force overrides live turns", true, true, 3, `{"force":true}`, http.StatusOK, true},
		{"force cannot override missing supervision", true, false, 3, `{"force":true}`, http.StatusConflict, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fired := make(chan struct{}, 1)
			a := &api{
				supervised: tc.supervised,
				turns:      func() int { return tc.turns },
				restart:    func() { fired <- struct{}{} },
			}
			req := httptest.NewRequest(http.MethodPost, "/api/restart", strings.NewReader(tc.body))
			if tc.header {
				req.Header.Set("X-Atlas-Action", "restart")
			}
			rec := httptest.NewRecorder()
			a.restartDaemon(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
			select {
			case <-fired:
				if !tc.restarts {
					t.Fatal("restart fired but should have been refused")
				}
			case <-time.After(150 * time.Millisecond):
				if tc.restarts {
					t.Fatal("restart did not fire")
				}
			}
		})
	}
}

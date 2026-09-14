package hermes

import "testing"

func TestAPIPath(t *testing.T) {
	cases := []struct {
		profile string
		p       string
		want    string
	}{
		{"", "/api/sessions", "/api/sessions"},
		{"default", "/api/sessions", "/api/sessions"},
		{"debbie", "/api/sessions", "/p/debbie/api/sessions"},
		{"debbie", "/v1/runs/run_1/stop", "/p/debbie/v1/runs/run_1/stop"},
	}
	for _, c := range cases {
		if got := apiPath(c.profile, c.p); got != c.want {
			t.Errorf("apiPath(%q, %q) = %q, want %q", c.profile, c.p, got, c.want)
		}
	}
}

func TestKeyFor(t *testing.T) {
	c := &Client{
		Key:         "primary",
		ProfileKeys: map[string]string{"debbie": "dkey"},
	}
	if got := c.KeyFor(""); got != "primary" {
		t.Errorf("empty profile = %q", got)
	}
	if got := c.KeyFor("default"); got != "primary" {
		t.Errorf("default profile = %q", got)
	}
	if got := c.KeyFor("Debbie"); got != "dkey" {
		t.Errorf("case-insensitive profile lookup = %q", got)
	}
	if got := c.KeyFor("nobody"); got != "" {
		t.Errorf("unknown profile = %q", got)
	}
	if !c.ConfiguredFor("debbie") || c.ConfiguredFor("nobody") {
		t.Error("ConfiguredFor mismatch")
	}
}

package cmd

import "testing"

func TestExecWSURL(t *testing.T) {
	cases := []struct{ base, app, pod, want string }{
		{"https://astrolift.example.net", "web", "web-abc", "wss://astrolift.example.net/app/exec/web/web-abc"},
		{"http://localhost:8000", "api", "api-1", "ws://localhost:8000/app/exec/api/api-1"},
		{"https://host/app", "w", "p", "wss://host/app/exec/w/p"},   // base path ignored — only scheme+host used
		{"https://h", "a b", "p/q", "wss://h/app/exec/a%20b/p%2Fq"}, // path-escaped
	}
	for _, c := range cases {
		got, err := execWSURL(c.base, c.app, c.pod)
		if err != nil {
			t.Fatalf("execWSURL(%q): %v", c.base, err)
		}
		if got != c.want {
			t.Errorf("execWSURL(%q,%q,%q)=%q want %q", c.base, c.app, c.pod, got, c.want)
		}
	}
}

package config

import "testing"

func TestGitRemoteWebURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"https with .git", "https://github.com/you/memory.git", "https://github.com/you/memory"},
		{"https without .git", "https://github.com/you/memory", "https://github.com/you/memory"},
		{"https trailing slash", "https://github.com/you/memory.git/", "https://github.com/you/memory"},
		{"https with credentials", "https://ada:secret-token@github.com/you/memory.git", "https://github.com/you/memory"},
		{"https with query and fragment", "https://github.com/you/memory.git?ref=main#readme", "https://github.com/you/memory"},
		{"http preserved", "http://git.internal/team/repo.git", "http://git.internal/team/repo"},
		{"scp-style", "git@github.com:you/memory.git", "https://github.com/you/memory"},
		{"scp-style no user", "github.com:you/memory.git", "https://github.com/you/memory"},
		{"ssh scheme", "ssh://git@github.com/you/memory.git", "https://github.com/you/memory"},
		{"ssh scheme with port", "ssh://git@github.com:2222/you/memory.git", "https://github.com/you/memory"},
		{"whitespace trimmed", "  https://github.com/you/memory.git  ", "https://github.com/you/memory"},
		{"empty", "", ""},
		{"absolute local path", "/home/user/repo", ""},
		{"file scheme", "file:///home/user/repo", ""},
		{"windows path not mangled", `C:\repos\memory`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := GitRemoteWebURL(tc.raw); got != tc.want {
				t.Fatalf("GitRemoteWebURL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

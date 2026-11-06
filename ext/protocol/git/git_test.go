package git

import "testing"

func TestGitURL(t *testing.T) {
	tests := []struct{ repo, want string }{
		{"Shougo/dpp.vim", "https://github.com/Shougo/dpp.vim.git"},
		{"gitlab.com/user/repo", "https://gitlab.com/user/repo.git"},
		{"git@github.com:user/repo.git", "git@github.com:user/repo.git"},
		{"https://example.com/user/repo", "https://example.com/user/repo.git"},
		{"https://git.sr.ht/~user/repo", "https://git.sr.ht/~user/repo"},
		{"ftp://example.com/user/repo", ""},
		{"repo", ""},
	}
	for _, tt := range tests {
		if got := GitURL(tt.repo, "github.com", "https"); got != tt.want {
			t.Errorf("GitURL(%q) = %q, want %q", tt.repo, got, tt.want)
		}
	}
	if got := GitURL("user/repo", "github.com", "ssh"); got != "git@github.com:user/repo.git" {
		t.Errorf("ssh: %q", got)
	}
}

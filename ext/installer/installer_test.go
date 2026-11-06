package installer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Shougo/goda-pm.vim/pm"
)

func TestTimeAgo(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "just now"},
		{time.Second, "1 second ago"},
		{2 * time.Minute, "2 minutes ago"},
		{time.Hour, "1 hour ago"},
		{3 * 24 * time.Hour, "3 days ago"},
		{60 * 24 * time.Hour, "2 months ago"},
		{800 * 24 * time.Hour, "2 years ago"},
	}
	for _, tt := range tests {
		if got := TimeAgo(now.Add(-tt.d), now); got != tt.want {
			t.Errorf("%v: got %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestFormatPlugin(t *testing.T) {
	u := &updated{plugin: pm.Plugin{"name": "foo"}, url: "https://github.com/a/foo", oldRev: "1", newRev: "2", changesCount: 2}
	if got := formatPlugin(u, time.Now()); got != "  foo(2 changes)\n    https://github.com/a/foo/compare/1...2" {
		t.Errorf("got %q", got)
	}
}

func TestCheckInstalledFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("a\n see https://example.com/x.zip now\n"), 0o644)
	m := checkInstalledFiles(DefaultParams(), pm.Plugin{"path": dir})
	if len(m) != 1 || m[0].line != 2 || m[0].column != 6 || m[0].url != "https://example.com/x.zip" {
		t.Errorf("got %+v", m)
	}
}

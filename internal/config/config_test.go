package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLoadOverlay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("AppData", home) // Windows
	user := UserPath()
	if err := os.MkdirAll(filepath.Dir(user), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(user, []byte("fetch:\n  interval: 10m\n  remotes: [origin, upstream]\n"), 0o644)

	gitDir := t.TempDir()
	os.WriteFile(filepath.Join(gitDir, "gituy.yaml"), []byte("fetch:\n  enabled: false\n"), 0o644)

	cfg, err := Load(gitDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Fetch.Interval != 10*time.Minute || cfg.Fetch.Enabled || !cfg.Fetch.Prune {
		t.Fatalf("got %+v", cfg.Fetch)
	}
}

func TestFetchRemotes(t *testing.T) {
	avail := []string{"origin", "upstream", "mirror"}
	cases := []struct {
		f    Fetch
		want []string
	}{
		{Fetch{}, nil},
		{Fetch{Remotes: []string{"*"}}, nil},
		{Fetch{Exclude: []string{"mirror"}}, []string{"origin", "upstream"}},
		{Fetch{Remotes: []string{"upstream", "gone"}}, []string{"upstream"}},
	}
	for _, c := range cases {
		if got := c.f.FetchRemotes(avail); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%+v: got %v, want %v", c.f, got, c.want)
		}
	}
}

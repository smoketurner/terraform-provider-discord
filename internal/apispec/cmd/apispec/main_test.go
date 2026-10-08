package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const spec = `{"paths": {"/guilds/{guild_id}": {"get": {"operationId": "get_guild"}}}}`

func TestRunErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no command":      {nil, "usage"},
		"unknown command": {[]string{"nope"}, `unknown command "nope"`},
		"fetch without o": {[]string{"fetch"}, "-o is required"},
		"fetch bad flag":  {[]string{"fetch", "-bogus"}, "flag provided but not defined"},
		"watch no flags":  {[]string{"watch"}, "are required"},
		"watch bad flag":  {[]string{"watch", "-bogus"}, "flag provided but not defined"},
		"watch no spec":   {[]string{"watch", "-pinned", "missing.json", "-latest", "missing.json", "-commit", "abc"}, "reading spec"},
	} {
		t.Run(name, func(t *testing.T) {
			var out strings.Builder
			err := run(t.Context(), tc.args, &out)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestWatch(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	pinned := write("pinned.json", spec)
	latest := write("latest.json", strings.Replace(spec, `}}}}`, `}}, "/users/@me": {"get": {"operationId": "get_my_user"}}}}`, 1))
	manifest := write("coverage.yaml", "operations:\n  get_guild: {status: pending_docs}\n")

	var out strings.Builder
	if err := run(t.Context(), []string{"watch", "-pinned", pinned, "-latest", latest, "-commit", "abc", "-manifest", manifest}, &out); err != nil {
		t.Fatal(err)
	}
	var findings []struct{ Title string }
	if err := json.Unmarshal([]byte(out.String()), &findings); err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Title != "API spec: new operation get_my_user" {
		t.Errorf("findings = %+v", findings)
	}

	out.Reset()
	if err := run(t.Context(), []string{"watch", "-pinned", pinned, "-latest", pinned, "-commit", "abc", "-manifest", manifest}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("output with no changes = %q, want []", out.String())
	}

	if err := run(t.Context(), []string{"watch", "-pinned", pinned, "-latest", latest, "-commit", "abc", "-manifest", filepath.Join(dir, "missing.yaml")}, &out); err == nil {
		t.Error("watch with a missing manifest succeeded")
	}
}

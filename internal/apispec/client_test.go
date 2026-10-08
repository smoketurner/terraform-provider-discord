package apispec

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const fixtureClient = `package fake

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type Client struct{}

func (c *Client) GetGuild(ctx context.Context, guildID string) error {
	return c.do(ctx, http.MethodGet, "/guilds/"+guildID, nil, nil)
}

func (c *Client) SearchMembers(ctx context.Context, guildID, query string) error {
	path := "/guilds/" + guildID + "/members/search?limit=1000&query=" + url.QueryEscape(query)
	return c.do(ctx, http.MethodGet, path, nil, nil)
}

func (c *Client) DeleteInvite(ctx context.Context, code string) error {
	return c.do(ctx, http.MethodDelete, ("/invites/" + url.PathEscape(code)), nil, nil)
}

func (c *Client) ListBans(ctx context.Context, guildID string, limit int) error {
	p := "/guilds/" + guildID + "/bans"
	if limit > 0 {
		p += "?limit=" + strconv.Itoa(limit)
	}
	return c.do(ctx, http.MethodGet, p, nil, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	return c.send(ctx, method, path)
}

func (c *Client) send(context.Context, string, string) error { return nil }
`

func writeClient(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestClientCalls(t *testing.T) {
	dir := writeClient(t, map[string]string{
		"client.go": fixtureClient,
		// Test files are not part of the client.
		"client_test.go": "package fake\nfunc x() { c.do(nil, http.MethodPut, \"/test\", nil, nil) }\n",
	})
	calls, err := ClientCalls(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range calls {
		got = append(got, c.Func+" "+c.Method+" "+c.Path+" "+strings.Join(c.Query, ","))
	}
	want := []string{
		"GetGuild GET /guilds/{} ",
		"SearchMembers GET /guilds/{}/members/search limit,query",
		"DeleteInvite DELETE /invites/{} ",
		"ListBans GET /guilds/{}/bans limit",
	}
	if !slices.Equal(got, want) {
		t.Errorf("ClientCalls() =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if calls[0].Pos.Line != 13 {
		t.Errorf("GetGuild call is reported at line %d, want 13", calls[0].Pos.Line)
	}
}

func TestClientCallsErrors(t *testing.T) {
	call := func(arg string) string {
		return "package fake\nimport \"net/http\"\nfunc (c *Client) F(id string) { c.do(nil, http.MethodGet, " + arg + ", nil, nil) }\n"
	}
	for name, tc := range map[string]struct {
		files map[string]string
		want  string
	}{
		"no requests":     {map[string]string{"a.go": "package fake\n"}, "no client requests found"},
		"syntax error":    {map[string]string{"a.go": "package fake\nfunc {"}, "parsing client sources"},
		"non-string path": {map[string]string{"a.go": call("5")}, "is not a string"},
		"other operator":  {map[string]string{"a.go": call(`"/a" - id`)}, "unsupported operator -"},
		"computed path":   {map[string]string{"a.go": call(`func() string { return "/a" }()`)}, ""},
		"composite path":  {map[string]string{"a.go": call(`[]string{"/a"}`)}, "unsupported path expression"},
		"bad query":       {map[string]string{"a.go": call(`"/a?b=%zz"`)}, "invalid URL escape"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ClientCalls(writeClient(t, tc.files))
			if tc.want == "" {
				if err != nil {
					t.Errorf("got %v, want no error", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

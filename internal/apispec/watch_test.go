package apispec

import (
	"strings"
	"testing"
)

func TestWatch(t *testing.T) {
	pinned := fixture(t)
	latestDoc := strings.NewReplacer(
		// A new operation.
		`"/users/@me": {`, `"/users/{user_id}": {"get": {"operationId": "get_user", "summary": "Get User", "security": [{}, {"BotToken": []}]}}, "/users/@me": {`,
		// A new query parameter on a covered operation.
		`{"name": "limit", "in": "query"}`, `{"name": "limit", "in": "query"}, {"name": "after", "in": "query"}`,
		// A new body field on a covered operation.
		`"position": {"type": "integer"}`, `"position": {"type": "integer"}, "parent_id": {"type": "string"}`,
		// A new field on a planned operation is not reported.
		`"file": {"type": "string"}`, `"file": {"type": "string"}, "tags": {"type": "string"}`,
	).Replace(fixtureSpec)
	latest, err := ParseSpec([]byte(latestDoc))
	if err != nil {
		t.Fatal(err)
	}

	findings := Watch(pinned, latest, fixtureManifestParsed(t), "abc123")
	var titles []string
	for _, f := range findings {
		titles = append(titles, f.Title)
	}
	want := []string{
		"API spec: new operation get_user",
		"API spec: new query parameter after on search_guild_members",
		"API spec: new request field parent_id on bulk_update_guild_roles",
	}
	if strings.Join(titles, "\n") != strings.Join(want, "\n") {
		t.Fatalf("titles =\n%s\nwant\n%s", strings.Join(titles, "\n"), strings.Join(want, "\n"))
	}

	for _, s := range []string{"`GET /users/{user_id}`", "Summary: Get User", "Authentication: BotToken, none", "/blob/abc123/specs/openapi.json"} {
		if !strings.Contains(findings[0].Body, s) {
			t.Errorf("new operation body does not contain %q:\n%s", s, findings[0].Body)
		}
	}
	if !strings.Contains(findings[2].Body, "Used by: `discord_role_positions`") {
		t.Errorf("new field body does not name the resources:\n%s", findings[2].Body)
	}
}

func TestWatchNoChanges(t *testing.T) {
	s := fixture(t)
	if got := Watch(s, s, fixtureManifestParsed(t), "abc123"); len(got) != 0 {
		t.Errorf("Watch() = %v, want no findings", got)
	}
}

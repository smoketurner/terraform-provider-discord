package apispec

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const fixtureSpec = `{
  "paths": {
    "/guilds/{guild_id}": {
      "parameters": [{"name": "guild_id", "in": "path"}],
      "get": {"operationId": "get_guild", "summary": "Get Guild", "security": [{"BotToken": []}]},
      "patch": {"operationId": "update_guild", "requestBody": {"content": {
        "application/json": {"schema": {"$ref": "#/components/schemas/GuildPatch"}}}}}
    },
    "/guilds/{guild_id}/members/search": {
      "get": {"operationId": "search_guild_members", "parameters": [
        {"name": "query", "in": "query"}, {"name": "limit", "in": "query"}]}
    },
    "/guilds/{guild_id}/members/{user_id}": {"get": {"operationId": "get_guild_member"}},
    "/guilds/{guild_id}/roles": {
      "patch": {"operationId": "bulk_update_guild_roles", "requestBody": {"content": {
        "application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/Position"}}}}}}
    },
    "/guilds/{guild_id}/stickers": {
      "post": {"operationId": "create_guild_sticker", "requestBody": {"content": {
        "multipart/form-data": {"schema": {"type": "object", "properties": {"name": {"type": "string"}, "file": {"type": "string"}}}}}}}
    },
    "/users/@me": {"get": {"operationId": "get_my_user", "security": [{}, {"BotToken": []}]}}
  },
  "components": {"schemas": {
    "GuildPatch": {"anyOf": [
      {"$ref": "#/components/schemas/GuildFields"},
      {"type": "object", "properties": {"banner": {"type": ["string", "null"]}}}
    ]},
    "GuildFields": {"type": "object", "properties": {
      "name": {"type": "string"},
      "verification_level": {"$ref": "#/components/schemas/Level"}
    }},
    "Level": {"type": "integer", "oneOf": [{"const": 0}, {"const": 1}]},
    "Kind": {"oneOf": [{"const": "a"}, {"const": "b"}]},
    "Flag": {"const": true},
    "Position": {"type": "object", "properties": {
      "id": {"oneOf": [{"type": "null"}, {"$ref": "#/components/schemas/Snowflake"}]},
      "position": {"type": "integer"}
    }},
    "Snowflake": {"type": "string"},
    "Roles": {"oneOf": [{"type": "null"}, {"type": "array", "items": {"$ref": "#/components/schemas/Snowflake"}}]},
    "Loop": {"$ref": "#/components/schemas/Loop"},
    "Dangling": {"$ref": "#/components/schemas/Missing"}
  }}
}`

func fixture(t *testing.T) *Spec {
	t.Helper()
	s, err := ParseSpec([]byte(fixtureSpec))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func schema(t *testing.T, s *Spec, name string) *Schema {
	t.Helper()
	sc, ok := s.Schema(name)
	if !ok {
		t.Fatalf("schema %s not found", name)
	}
	return sc
}

func TestParseSpec(t *testing.T) {
	s := fixture(t)
	if len(s.Operations) != 7 {
		t.Fatalf("got %d operations, want 7", len(s.Operations))
	}
	op := s.Operations["update_guild"]
	if op.Method != "PATCH" || op.Path != "/guilds/{guild_id}" {
		t.Errorf("update_guild is %s %s", op.Method, op.Path)
	}
}

func TestParseSpecErrors(t *testing.T) {
	for name, tc := range map[string]struct{ doc, want string }{
		"invalid JSON":        {`{`, "parsing spec"},
		"no paths":            {`{"paths": {}}`, "no paths"},
		"no operationId":      {`{"paths": {"/a": {"get": {}}}}`, "has no operationId"},
		"bad operation":       {`{"paths": {"/a": {"get": []}}}`, "parsing GET /a"},
		"bad schema type":     {`{"paths": {"/a": {"get": {"operationId": "a"}}}, "components": {"schemas": {"A": {"type": 5}}}}`, "schema type"},
		"duplicate operation": {`{"paths": {"/a": {"get": {"operationId": "a"}}, "/b": {"get": {"operationId": "a"}}}}`, "operationId a is used by"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSpec([]byte(tc.doc))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestLoadSpec(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openapi.json")
	if _, err := LoadSpec(path); err == nil {
		t.Error("loading a missing file succeeded")
	}
	if err := os.WriteFile(path, []byte(fixtureSpec), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSpec(path); err != nil {
		t.Error(err)
	}
}

func TestMatch(t *testing.T) {
	s := fixture(t)
	for _, tc := range []struct{ method, path, want string }{
		{"GET", "/guilds/{}", "get_guild"},
		{"PATCH", "/guilds/{}", "update_guild"},
		{"GET", "/guilds/{}/members/search", "search_guild_members"},
		{"GET", "/guilds/{}/members/{}", "get_guild_member"},
		{"GET", "/users/@me", "get_my_user"},
		// A literal segment never matches a parameter, and vice versa.
		{"GET", "/guilds/{}/members/@me", ""},
		{"GET", "/users/{}", ""},
		{"DELETE", "/guilds/{}", ""},
		{"GET", "/guilds", ""},
	} {
		op, ok := s.Match(tc.method, tc.path)
		got := ""
		if ok {
			got = op.ID
		}
		if got != tc.want {
			t.Errorf("Match(%s %s) = %q, want %q", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestTypes(t *testing.T) {
	s := fixture(t)
	for name, want := range map[string][]string{
		"Level":     {"integer"},
		"Kind":      {"string"},
		"Flag":      {"boolean"},
		"Snowflake": {"string"},
		"Roles":     {"array", "null"},
		"Loop":      nil,
		"Dangling":  nil,
	} {
		if got := s.Types(schema(t, s, name)); !slices.Equal(got, want) {
			t.Errorf("Types(%s) = %v, want %v", name, got, want)
		}
	}
	if got := s.Types(s.Properties(schema(t, s, "Position"))["id"]); !slices.Equal(got, []string{"null", "string"}) {
		t.Errorf("Types(Position.id) = %v", got)
	}
}

func TestProperties(t *testing.T) {
	s := fixture(t)
	got := slices.Sorted(maps.Keys(s.Properties(schema(t, s, "GuildPatch"))))
	if want := []string{"banner", "name", "verification_level"}; !slices.Equal(got, want) {
		t.Errorf("Properties(GuildPatch) = %v, want %v", got, want)
	}
	if got := s.Properties(schema(t, s, "Loop")); len(got) != 0 {
		t.Errorf("Properties(Loop) = %v", got)
	}
}

func TestItems(t *testing.T) {
	s := fixture(t)
	if got := s.Types(s.Items(schema(t, s, "Roles"))); !slices.Equal(got, []string{"string"}) {
		t.Errorf("Items(Roles) types = %v", got)
	}
	if got := s.Items(schema(t, s, "Snowflake")); got != nil {
		t.Errorf("Items(Snowflake) = %v, want nil", got)
	}
	if got := s.Items(schema(t, s, "Dangling")); got != nil {
		t.Errorf("Items(Dangling) = %v, want nil", got)
	}
}

func TestRequestFields(t *testing.T) {
	s := fixture(t)
	for id, want := range map[string][]string{
		"update_guild":            {"banner", "name", "verification_level"},
		"bulk_update_guild_roles": {"id", "position"},
		"search_guild_members":    {"?limit", "?query"},
		"create_guild_sticker":    {"file", "name"},
		"get_guild":               {},
	} {
		if got := s.RequestFields(s.Operations[id]); !slices.Equal(got, want) {
			t.Errorf("RequestFields(%s) = %v, want %v", id, got, want)
		}
	}
}

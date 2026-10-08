package apispec

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const fixtureManifest = `operations:
  bulk_update_guild_roles: {status: covered, by: [discord_role_positions]}
  create_guild_sticker: {status: planned, issue: 16}
  get_guild: {status: covered, by: [discord_server_settings, data.discord_server]}
  get_guild_member: {status: covered, by: [discord_member_role]}
  get_my_user: {status: pending_docs}
  search_guild_members: {status: covered, by: [data.discord_member]}
  update_guild: {status: out_of_scope, reason: "test"}
`

func fixtureManifestParsed(t *testing.T) *Manifest {
	t.Helper()
	m, err := ParseManifest([]byte(fixtureManifest))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestManifestCheck(t *testing.T) {
	if err := fixtureManifestParsed(t).Check(fixture(t)); err != nil {
		t.Error(err)
	}
}

func TestManifestCovered(t *testing.T) {
	got := fixtureManifestParsed(t).Covered()
	want := []string{"bulk_update_guild_roles", "get_guild", "get_guild_member", "search_guild_members"}
	if !slices.Equal(got, want) {
		t.Errorf("Covered() = %v, want %v", got, want)
	}
}

func TestManifestCheckErrors(t *testing.T) {
	for name, tc := range map[string]struct{ old, new, want string }{
		"unmapped operation": {
			"  get_my_user: {status: pending_docs}\n", "",
			"get_my_user (GET /users/@me) is not in the manifest",
		},
		"operation not in spec": {
			"  update_guild:", "  update_guild_widget: {status: pending_docs}\n  update_guild:",
			"update_guild_widget is in the manifest but not in the spec",
		},
		"covered without by":      {"{status: covered, by: [discord_role_positions]}", "{status: covered}", "need by"},
		"covered with issue":      {"by: [discord_role_positions]}", "by: [discord_role_positions], issue: 3}", "take only by"},
		"planned without issue":   {"{status: planned, issue: 16}", "{status: planned}", "need issue"},
		"planned with reason":     {"issue: 16}", "issue: 16, reason: x}", "take only issue"},
		"out_of_scope w/o reason": {`reason: "test"`, `issue: 1`, "need reason"},
		"out_of_scope with by":    {`reason: "test"}`, `reason: "test", by: [x]}`, "take only reason"},
		"pending_docs with issue": {"{status: pending_docs}", "{status: pending_docs, issue: 1}", "take at most reason"},
		"unknown status":          {"{status: pending_docs}", "{status: done}", `unknown status "done"`},
		"operations out of order": {"  bulk_update_guild_roles:", "  zzz_last: {status: pending_docs}\n  bulk_update_guild_roles:", "sorted"},
	} {
		t.Run(name, func(t *testing.T) {
			doc := strings.Replace(fixtureManifest, tc.old, tc.new, 1)
			if doc == fixtureManifest {
				t.Fatal("replacement did not apply")
			}
			m, err := ParseManifest([]byte(doc))
			if err != nil {
				t.Fatal(err)
			}
			err = m.Check(fixture(t))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestParseManifestErrors(t *testing.T) {
	for name, doc := range map[string]string{
		"invalid YAML":  "operations: [",
		"unknown field": "operations:\n  get_guild: {status: covered, by: [x], note: y}\n",
		"wrong type":    "operations:\n  get_guild: {status: planned, issue: soon}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(doc)); err == nil {
				t.Error("parsing succeeded")
			}
		})
	}
}

func TestLoadManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coverage.yaml")
	if _, err := LoadManifest(path); err == nil {
		t.Error("loading a missing file succeeded")
	}
	if err := os.WriteFile(path, []byte(fixtureManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(path); err != nil {
		t.Error(err)
	}
}

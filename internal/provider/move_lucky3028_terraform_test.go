package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6/tf6server"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// The tests in this file run Terraform on state written by Lucky3028/discord,
// which terraform-plugin-testing cannot set up: that provider can only create
// resources in a real Discord server. The objects are created in the fake
// server instead, and the state file is written from the fixtures in
// testdata/lucky3028. The provider is served in-process for both addresses;
// Terraform only reads the schema of the Lucky3028/discord stand-in, and
// hands the state to this provider.

const testAvatar = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

// TestTerraformMoveFromLucky3028 moves resources with moved blocks: to the
// resource types that replace Lucky3028/discord's, and to new names for the
// types whose name is unchanged, since Terraform rejects a moved block whose
// addresses are equal.
func TestTerraformMoveFromLucky3028(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	tf := terraformCLI(t)
	ctx := context.Background()
	g := env.serverID

	guild, err := env.client.GetGuild(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	general := env.mustChannel(discord.Payload{"name": "general", "type": discord.ChannelTypeText})
	news := env.mustChannel(discord.Payload{"name": "news", "type": discord.ChannelTypeAnnouncement, "topic": "Releases"})
	if _, err := env.client.ModifyGuild(ctx, g, discord.Payload{"system_channel_id": general.ID, "system_channel_flags": 5}); err != nil {
		t.Fatal(err)
	}
	mods := env.mustRole(discord.Payload{"name": "Moderators", "permissions": "8192", "hoist": true})
	helpers := env.mustRole(discord.Payload{"name": "Helpers"})
	if _, err := env.client.ModifyMember(ctx, g, env.userID, discord.Payload{"roles": []string{mods.ID}}); err != nil {
		t.Fatal(err)
	}
	webhook, err := env.client.CreateWebhook(ctx, general.ID, discord.Payload{"name": "Deploys", "avatar": testAvatar})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := env.client.CreateAutoModerationRule(ctx, g, discord.Payload{
		"name": "Block invites", "event_type": 1, "trigger_type": 1, "enabled": true,
		"trigger_metadata": map[string]any{"keyword_filter": []string{"discord.gg"}},
		"actions":          []any{map[string]any{"type": 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.client.ModifyOnboarding(ctx, g, discord.Payload{"enabled": false, "default_channel_ids": []string{general.ID}}); err != nil {
		t.Fatal(err)
	}
	rules := filepath.Join(t.TempDir(), "rules.txt")
	writeFile(t, rules, []byte("Be nice"))
	welcome, err := env.client.CreateMessage(ctx, general.ID, &discord.Multipart{
		Payload: discord.Payload{"content": "Welcome!", "attachments": []any{map[string]any{"id": 0, "filename": "rules.txt"}}},
		Files:   []discord.File{{Field: "files[0]", Name: "rules.txt", ContentType: "text/plain", Data: []byte("Be nice")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	att := welcome.Attachments[0]
	roles, err := env.client.ListRoles(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	position := map[string]int64{}
	for _, r := range roles {
		position[r.ID] = r.Position
	}
	// Discord lists roles that share a position oldest first, so Helpers is
	// below Moderators.
	order := []string{mods.ID, helpers.ID}

	env.migrateFromLucky(tf, []luckyResource{
		{"discord_news_channel", "news", map[string]any{
			"id": news.ID, "channel_id": news.ID, "server_id": g, "name": "news", "category": "", "sync_perms_with_category": false,
		}},
		{"discord_role", "moderators", map[string]any{"id": mods.ID, "server_id": g, "permissions": 8192, "color": 0}},
		{"discord_member_roles", "tester", map[string]any{
			"id": g + ":" + env.userID, "server_id": g, "user_id": env.userID,
			"role": []any{
				map[string]any{"role_id": mods.ID, "has_role": true},
				map[string]any{"role_id": helpers.ID, "has_role": false},
			},
		}},
		{"discord_role_positions", "order", map[string]any{"id": g, "server_id": g, "position": []any{
			map[string]any{"role_id": helpers.ID, "position": position[helpers.ID]},
			map[string]any{"role_id": mods.ID, "position": position[mods.ID] + 1},
		}}},
		{"discord_webhook", "deploys", map[string]any{
			"id": webhook.ID, "channel_id": general.ID, "avatar_data_uri": testAvatar, "avatar_hash": *webhook.Avatar,
			"token": webhook.Token, "url": webhookURL(webhook),
		}},
		{"discord_auto_moderation_rule", "invites", map[string]any{
			"id": rule.ID, "server_id": g, "exempt_roles": []any{}, "creator_id": rule.CreatorID,
		}},
		{"discord_message", "welcome", map[string]any{
			"id": welcome.ID, "channel_id": general.ID, "server_id": g, "author": welcome.Author.ID, "content": "Welcome!",
			"pinned": false, "embed": []any{}, "file": []any{map[string]any{
				"source": rules, "filename": "rules.txt", "content_type": "text/plain", "id": att.ID, "url": att.URL,
				"proxy_url": att.URL, "size": att.Size,
			}},
		}},
		{"discord_server", "main", map[string]any{"id": g, "server_id": g, "name": guild.Name, "afk_channel_id": ""}},
		{"discord_system_channel", "main", map[string]any{"id": g, "server_id": g, "system_channel_id": general.ID}},
		{"discord_server_onboarding", "main", map[string]any{
			"id": g, "server_id": g, "enabled": false, "mode": 0, "default_channel_ids": []any{general.ID}, "prompt": []any{},
		}},
	}, fmt.Sprintf(`
moved {
  from = discord_news_channel.news
  to   = discord_announcement_channel.news
}
resource "discord_announcement_channel" "news" {
  server_id = %[1]q
  name      = "news"
  topic     = "Releases"
}

moved {
  from = discord_role.moderators
  to   = discord_role.mods
}
resource "discord_role" "mods" {
  server_id   = %[1]q
  name        = "Moderators"
  permissions = "8192"
  hoist       = true
}

moved {
  from = discord_member_roles.tester
  to   = discord_member_roles.tester_roles
}
resource "discord_member_roles" "tester_roles" {
  server_id = %[1]q
  user_id   = %[2]q
  role_ids  = [%[3]q]
}

moved {
  from = discord_role_positions.order
  to   = discord_role_positions.roles
}
resource "discord_role_positions" "roles" {
  server_id = %[1]q
  role_ids  = [%[4]q, %[5]q]
}

moved {
  from = discord_webhook.deploys
  to   = discord_webhook.deploy
}
resource "discord_webhook" "deploy" {
  channel_id = %[6]q
  name       = "Deploys"
  avatar     = %[7]q
}

moved {
  from = discord_auto_moderation_rule.invites
  to   = discord_auto_moderation_rule.block_invites
}
resource "discord_auto_moderation_rule" "block_invites" {
  server_id        = %[1]q
  name             = "Block invites"
  event_type       = "message_send"
  trigger_type     = "keyword"
  trigger_metadata = { keyword_filter = ["discord.gg"] }
  actions          = [{ type = "block_message" }]
  enabled          = true
}

moved {
  from = discord_message.welcome
  to   = discord_message.welcome_message
}
resource "discord_message" "welcome_message" {
  channel_id  = %[6]q
  content     = "Welcome!"
  attachments = [{ filename = "rules.txt", source = %[9]q }]
}

moved {
  from = discord_server.main
  to   = discord_server_settings.main
}
resource "discord_server_settings" "main" {
  server_id = %[1]q
  name      = %[8]q
}

moved {
  from = discord_system_channel.main
  to   = discord_server_settings.system
}
resource "discord_server_settings" "system" {
  server_id            = %[1]q
  system_channel_id    = %[6]q
  system_channel_flags = 5
}

moved {
  from = discord_server_onboarding.main
  to   = discord_onboarding.main
}
resource "discord_onboarding" "main" {
  server_id           = %[1]q
  enabled             = false
  default_channel_ids = [%[6]q]
}
`, g, env.userID, mods.ID, order[0], order[1], general.ID, testAvatar, guild.Name, rules), map[string]string{
		"discord_announcement_channel.news":          "discord_news_channel.news",
		"discord_role.mods":                          "discord_role.moderators",
		"discord_member_roles.tester_roles":          "discord_member_roles.tester",
		"discord_role_positions.roles":               "discord_role_positions.order",
		"discord_webhook.deploy":                     "discord_webhook.deploys",
		"discord_auto_moderation_rule.block_invites": "discord_auto_moderation_rule.invites",
		"discord_server_settings.main":               "discord_server.main",
		"discord_server_settings.system":             "discord_system_channel.main",
		"discord_onboarding.main":                    "discord_server_onboarding.main",
		"discord_message.welcome_message":            "discord_message.welcome",
	})
	if ch, err := env.client.GetChannel(ctx, news.ID); err != nil || ch.Type != discord.ChannelTypeAnnouncement {
		t.Errorf("announcement channel after apply: %v, %v", ch, err)
	}
}

// TestTerraformSwitchFromLucky3028 changes only the provider of resources
// whose type name is unchanged. Terraform then hands their state to this
// provider without a moved block, and the refresh rebuilds it.
func TestTerraformSwitchFromLucky3028(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	tf := terraformCLI(t)
	ctx := context.Background()
	g := env.serverID

	everyone, err := env.client.GetRole(ctx, g, g)
	if err != nil {
		t.Fatal(err)
	}
	category := env.mustChannel(discord.Payload{"name": "projects", "type": discord.ChannelTypeCategory})
	general := env.mustChannel(discord.Payload{"name": "general", "type": discord.ChannelTypeText, "topic": "Chat", "parent_id": category.ID})
	voice := env.mustChannel(discord.Payload{"name": "voice", "type": discord.ChannelTypeVoice})
	forum := env.mustChannel(discord.Payload{"name": "help", "type": discord.ChannelTypeForum})
	if err := env.client.EditChannelPermission(ctx, general.ID, discord.Overwrite{ID: env.userID, Type: 1, Allow: "1024", Deny: "0"}); err != nil {
		t.Fatal(err)
	}
	invite, err := env.client.CreateInvite(ctx, general.ID, discord.Payload{"max_age": 0, "max_uses": 5, "unique": false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.client.ModifyWidgetSettings(ctx, g, discord.Payload{"enabled": true, "channel_id": general.ID}); err != nil {
		t.Fatal(err)
	}
	channel := func(ch *discord.Channel) map[string]any {
		return map[string]any{"id": ch.ID, "channel_id": ch.ID, "server_id": g, "name": ch.Name, "category": "", "sync_perms_with_category": false}
	}
	textAttrs := channel(general)
	textAttrs["category"] = category.ID

	env.migrateFromLucky(tf, []luckyResource{
		{"discord_category_channel", "projects", map[string]any{"id": category.ID, "channel_id": category.ID, "server_id": g, "name": "projects"}},
		{"discord_text_channel", "general", textAttrs},
		{"discord_voice_channel", "voice", channel(voice)},
		{"discord_forum_channel", "help", channel(forum)},
		{"discord_channel_permission", "tester", map[string]any{
			"id": general.ID + ":" + env.userID + ":user", "channel_id": general.ID, "overwrite_id": env.userID,
			"allow": 1024, "deny": 0,
		}},
		{"discord_role_everyone", "everyone", map[string]any{"id": g, "server_id": g, "permissions": 0}},
		{"discord_invite", "join", map[string]any{"id": invite.Code, "code": invite.Code, "channel_id": general.ID}},
		{"discord_server_widget", "main", map[string]any{"id": g, "server_id": g, "channel_id": general.ID}},
	}, fmt.Sprintf(`
resource "discord_category_channel" "projects" {
  server_id = %[1]q
  name      = "projects"
}
resource "discord_text_channel" "general" {
  server_id   = %[1]q
  name        = "general"
  topic       = "Chat"
  category_id = discord_category_channel.projects.id
}
resource "discord_voice_channel" "voice" {
  server_id = %[1]q
  name      = "voice"
}
resource "discord_forum_channel" "help" {
  server_id = %[1]q
  name      = "help"
}
resource "discord_channel_permission" "tester" {
  channel_id   = discord_text_channel.general.id
  overwrite_id = %[2]q
  type         = "member"
  allow        = "1024"
}
resource "discord_role_everyone" "everyone" {
  server_id   = %[1]q
  permissions = %[3]q
}
resource "discord_invite" "join" {
  channel_id = discord_text_channel.general.id
  max_age    = 0
  max_uses   = 5
  unique     = false
}
resource "discord_server_widget" "main" {
  server_id  = %[1]q
  enabled    = true
  channel_id = discord_text_channel.general.id
}
`, g, env.userID, everyone.Permissions), map[string]string{
		"discord_category_channel.projects": "",
		"discord_text_channel.general":      "",
		"discord_voice_channel.voice":       "",
		"discord_forum_channel.help":        "",
		"discord_channel_permission.tester": "",
		"discord_role_everyone.everyone":    "",
		"discord_invite.join":               "",
		"discord_server_widget.main":        "",
	})
}

func (e *testEnv) mustChannel(p discord.Payload) *discord.Channel {
	e.t.Helper()
	ch, err := e.client.CreateChannel(context.Background(), e.serverID, p)
	if err != nil {
		e.t.Fatal(err)
	}
	return ch
}

func (e *testEnv) mustRole(p discord.Payload) *discord.Role {
	e.t.Helper()
	r, err := e.client.CreateRole(context.Background(), e.serverID, p)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

// migrateFromLucky writes a state file holding the Lucky3028/discord
// resources and a configuration for this provider, and checks that the plan
// only changes the resources' provider and addresses (want maps each address
// to its previous one, "" when unchanged), that applying it leaves no
// changes, and that the state no longer refers to Lucky3028/discord.
func (e *testEnv) migrateFromLucky(tf *terraformRunner, resources []luckyResource, config string, want map[string]string) {
	t := e.t
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "terraform.tfstate"), luckyStateFile(t, resources))
	writeFile(t, filepath.Join(dir, "main.tf"), []byte(`
terraform {
  required_providers {
    discord = { source = "smoketurner/discord" }
  }
}
`+config))

	tf.run(dir, "init", "-input=false", "-no-color")
	tf.run(dir, "plan", "-input=false", "-no-color", "-out=migrate.tfplan")
	var plan struct {
		ResourceChanges []struct {
			Address         string `json:"address"`
			PreviousAddress string `json:"previous_address"`
			ProviderName    string `json:"provider_name"`
			Change          struct {
				Actions []string `json:"actions"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal(tf.run(dir, "show", "-json", "migrate.tfplan"), &plan); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, rc := range plan.ResourceChanges {
		if strings.Join(rc.Change.Actions, ",") != "no-op" {
			t.Errorf("%s: actions %v, want no-op", rc.Address, rc.Change.Actions)
		}
		if !strings.HasSuffix(rc.ProviderName, "/smoketurner/discord") {
			t.Errorf("%s: provider %s", rc.Address, rc.ProviderName)
		}
		got[rc.Address] = rc.PreviousAddress
	}
	for to, from := range want {
		if prev, ok := got[to]; !ok || prev != from {
			t.Errorf("%s: previous address %q (planned: %t), want %q", to, prev, ok, from)
		}
	}
	if t.Failed() {
		out, _, _ := tf.exec(dir, "show", "-no-color", "migrate.tfplan")
		t.Fatalf("plan:\n%s", out)
	}

	tf.run(dir, "apply", "-input=false", "-no-color", "migrate.tfplan")
	if out, errOut, code := tf.exec(dir, "plan", "-input=false", "-no-color", "-detailed-exitcode"); code != 0 {
		t.Fatalf("plan after the migration has changes (exit code %d):\n%s%s", code, out, errOut)
	}
	if strings.Contains(string(tf.run(dir, "state", "pull")), "lucky3028") {
		t.Error("state still refers to lucky3028/discord after apply")
	}
}

type luckyResource struct {
	typeName, name string
	// attrs override the fixture's attributes.
	attrs map[string]any
}

// luckyStateFile returns a Terraform state file holding the given
// Lucky3028/discord resources, each built from its fixture.
func luckyStateFile(t *testing.T, resources []luckyResource) []byte {
	t.Helper()
	out := make([]any, 0, len(resources))
	for _, r := range resources {
		var attrs map[string]any
		if err := json.Unmarshal(luckyFixture(t, r.typeName), &attrs); err != nil {
			t.Fatal(err)
		}
		for k, v := range r.attrs {
			attrs[k] = v
		}
		out = append(out, map[string]any{
			"mode": "managed", "type": r.typeName, "name": r.name,
			"provider": `provider["` + lucky3028Address + `"]`,
			"instances": []any{map[string]any{
				"schema_version": 0, "attributes": attrs, "sensitive_attributes": []any{},
			}},
		})
	}
	b, err := json.Marshal(map[string]any{
		"version": 4, "terraform_version": "1.8.0", "serial": 1, "lineage": "6c8a5c2e-4a5b-4c1d-9e0f-3a1b2c3d4e5f",
		"outputs": map[string]any{}, "resources": out,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeFile(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(name, data, 0o600); err != nil { //nolint:gosec // Tests write under t.TempDir.
		t.Fatal(err)
	}
}

type terraformRunner struct {
	t    *testing.T
	path string
	env  []string
}

// terraformCLI finds Terraform 1.8 or later, as terraform-plugin-testing
// does, and serves this provider to it in-process under its own address and
// Lucky3028/discord's.
func terraformCLI(t *testing.T) *terraformRunner {
	t.Helper()
	path := os.Getenv("TF_ACC_TERRAFORM_PATH")
	if path == "" {
		var err error
		if path, err = exec.LookPath("terraform"); err != nil {
			t.Skip("terraform is not on PATH")
		}
	}
	tf := &terraformRunner{t: t, path: path}
	var version struct {
		Version string `json:"terraform_version"`
	}
	if err := json.Unmarshal(tf.run(t.TempDir(), "version", "-json"), &version); err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(version.Version, ".", 3)
	minor, _ := strconv.Atoi(parts[1])
	if parts[0] == "1" && minor < 8 {
		t.Skipf("moving state between providers needs Terraform 1.8, have %s", version.Version)
	}

	reattach := map[string]any{}
	for _, addr := range []string{"registry.terraform.io/smoketurner/discord", lucky3028Address} {
		reattach[addr] = serveProvider(t, addr)
	}
	b, err := json.Marshal(reattach)
	if err != nil {
		t.Fatal(err)
	}
	tf.env = append(os.Environ(), "TF_REATTACH_PROVIDERS="+string(b), "CHECKPOINT_DISABLE=1", "TF_IN_AUTOMATION=1")
	return tf
}

// serveProvider serves the provider until the test ends and returns its
// reattach configuration.
func serveProvider(t *testing.T, addr string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	configCh := make(chan *plugin.ReattachConfig)
	closeCh := make(chan struct{})
	go func() {
		err := tf6server.Serve(addr, providerserver.NewProtocol6(New("test")()),
			tf6server.WithDebug(ctx, configCh, closeCh),
			tf6server.WithGoPluginLogger(hclog.NewNullLogger()),
			tf6server.WithoutLogStderrOverride(),
			tf6server.WithLoggingSink(t))
		if err != nil {
			t.Errorf("serving the provider: %v", err)
		}
	}()
	cfg := <-configCh
	t.Cleanup(func() {
		cancel()
		<-closeCh
	})
	return map[string]any{
		"Protocol": string(cfg.Protocol), "ProtocolVersion": cfg.ProtocolVersion, "Pid": cfg.Pid, "Test": true,
		"Addr": map[string]string{"Network": cfg.Addr.Network(), "String": cfg.Addr.String()},
	}
}

// exec runs Terraform and returns its output and exit code.
func (tf *terraformRunner) exec(dir string, args ...string) (stdout, stderr []byte, code int) {
	tf.t.Helper()
	cmd := exec.CommandContext(tf.t.Context(), tf.path, append([]string{"-chdir=" + dir}, args...)...) //nolint:gosec // The Terraform binary the test environment selects.
	cmd.Env = tf.env
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out.Bytes(), errOut.Bytes(), exit.ExitCode()
	}
	if err != nil {
		tf.t.Fatal(err)
	}
	return out.Bytes(), errOut.Bytes(), 0
}

// run runs Terraform, failing the test unless it succeeds, and returns its
// standard output.
func (tf *terraformRunner) run(dir string, args ...string) []byte {
	tf.t.Helper()
	out, errOut, code := tf.exec(dir, args...)
	if code != 0 {
		tf.t.Fatalf("terraform %s: exit code %d:\n%s%s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

// Terraform cannot hand an AutoMod rule to this provider without a moved
// block: Lucky3028/discord stores trigger_metadata as a list, where this
// provider has an object, so the state cannot be read with this provider's
// schema.
func TestTerraformSwitchFromLucky3028AutoModerationRule(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	tf := terraformCLI(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "terraform.tfstate"), luckyStateFile(t, []luckyResource{
		{"discord_auto_moderation_rule", "invites", map[string]any{"server_id": env.serverID}},
	}))
	writeFile(t, filepath.Join(dir, "main.tf"), []byte(`
terraform {
  required_providers {
    discord = { source = "smoketurner/discord" }
  }
}
resource "discord_auto_moderation_rule" "invites" {
  server_id    = "`+env.serverID+`"
  name         = "Block invites"
  event_type   = "message_send"
  trigger_type = "keyword"
  actions      = [{ type = "block_message" }]
}
`))
	tf.run(dir, "init", "-input=false", "-no-color")
	_, errOut, code := tf.exec(dir, "plan", "-input=false", "-no-color")
	if code == 0 || !strings.Contains(string(errOut), "Unable to Read Previously Saved State") {
		t.Fatalf("plan exit code %d, want the state read error:\n%s", code, errOut)
	}
}

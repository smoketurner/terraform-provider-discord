package provider

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/list"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/querycheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// requiresQuery skips Terraform versions without terraform query.
var requiresQuery = []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_14_0)}

// stateSnapshot holds the flatmap attributes of every resource in state,
// captured by snapshot for checks in later steps.
type stateSnapshot map[string]map[string]string

func (s stateSnapshot) capture() tfresource.TestCheckFunc {
	return func(st *terraform.State) error {
		for name, rs := range st.RootModule().Resources {
			s[name] = maps.Clone(rs.Primary.Attributes)
		}
		return nil
	}
}

// listedAsInState checks that the list block <type>.test returned the
// resource <type>.test in state: a result with its identity and, when the
// block includes resources, the same value for every primitive attribute
// that is set in state, except those in ignore.
type listedAsInState struct {
	snapshot stateSnapshot
	name     string
	identity map[string]string
	resource bool
	ignore   []string
}

func (c listedAsInState) CheckQuery(_ context.Context, req querycheck.CheckQueryRequest, resp *querycheck.CheckQueryResponse) {
	state, ok := c.snapshot[c.name]
	if !ok {
		resp.Error = fmt.Errorf("%s not in state", c.name)
		return
	}
	want := map[string]string{}
	for identityAttr, stateAttr := range c.identity {
		want[identityAttr] = state[stateAttr]
	}
	for _, found := range req.Query {
		if strings.TrimPrefix(found.Address, "list.") != c.name || !identityEquals(found.Identity, want) {
			continue
		}
		if !c.resource {
			if found.ResourceObject != nil {
				resp.Error = fmt.Errorf("%s: resource object returned without include_resource", c.name)
			}
			return
		}
		if found.ResourceObject == nil {
			resp.Error = fmt.Errorf("%s: no resource object with include_resource", c.name)
			return
		}
		var diffs []string
		for attr, v := range found.ResourceObject {
			s, ok := state[attr]
			if v == nil || !ok || slices.Contains(c.ignore, attr) {
				continue
			}
			var got string
			switch v := v.(type) {
			case string:
				got = v
			case bool:
				got = strconv.FormatBool(v)
			case float64:
				got = strconv.FormatFloat(v, 'f', -1, 64)
			default:
				continue
			}
			if got != s {
				diffs = append(diffs, fmt.Sprintf("%s: listed %q, state %q", attr, got, s))
			}
		}
		for attr, v := range state {
			if v != "" && !strings.ContainsAny(attr, ".%#") && found.ResourceObject[attr] == nil && !slices.Contains(c.ignore, attr) {
				diffs = append(diffs, fmt.Sprintf("%s: listed null, state %q", attr, v))
			}
		}
		if diffs != nil {
			slices.Sort(diffs)
			resp.Error = fmt.Errorf("%s: %s", c.name, strings.Join(diffs, "; "))
		}
		return
	}
	resp.Error = fmt.Errorf("%s: no result with identity %v", c.name, want)
}

func identityEquals(got map[string]any, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		g, _ := got[k].(string)
		if g != v {
			return false
		}
	}
	return true
}

// listBlock is a query list block for <type>.test.
func listBlock(typ string, includeResource bool, config string) string {
	return fmt.Sprintf(`
list %q "test" {
  provider         = discord
  include_resource = %t
  config {
    %s
  }
}
`, typ, includeResource, config)
}

func TestAccListResources(t *testing.T) {
	env := newTestEnv(t)
	server := fmt.Sprintf("server_id = %q", env.serverID)
	cfg := `
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-list"
  color     = 255
}
resource "discord_category_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-list"
}
resource "discord_text_channel" "test" {
  server_id   = local.server_id
  name        = "tf-acc-list"
  category_id = discord_category_channel.test.id
  topic       = "Listed"
}
resource "discord_voice_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-list"
}
resource "discord_announcement_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-list"
}
resource "discord_stage_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-list"
}
resource "discord_forum_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-list"
}
resource "discord_media_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-list"
}
resource "discord_channel_permission" "test" {
  channel_id   = discord_text_channel.test.id
  overwrite_id = discord_role.test.id
  type         = "role"
  allow        = provider::discord::permissions(["VIEW_CHANNEL"])
}
resource "discord_channel_follower" "test" {
  source_channel_id = discord_announcement_channel.test.id
  channel_id        = discord_text_channel.test.id
}
resource "discord_invite" "test" {
  channel_id = discord_text_channel.test.id
  max_uses   = 5
}
resource "discord_thread" "test" {
  channel_id = discord_text_channel.test.id
  name       = "tf-acc-list"
}
resource "discord_webhook" "test" {
  channel_id    = discord_text_channel.test.id
  name          = "tf-acc-list"
  store_secrets = false
}
resource "discord_emoji" "test" {
  server_id = local.server_id
  name      = "tf_acc_list"
  image     = "` + onePixelPNG + `"
}
resource "discord_sticker" "test" {
  server_id = local.server_id
  name      = "tf-acc-list"
  tags      = "wave"
  file      = "` + stickerPNG + `"
}
resource "discord_soundboard_sound" "test" {
  server_id = local.server_id
  name      = "tf-acc-list"
  sound     = "` + soundMP3 + `"
}
resource "discord_auto_moderation_rule" "test" {
  server_id        = local.server_id
  name             = "tf-acc-list"
  event_type       = "message_send"
  trigger_type     = "keyword"
  trigger_metadata = { keyword_filter = ["tf-acc-list"] }
  actions          = [{ type = "block_message" }]
}
resource "discord_scheduled_event" "test" {
  server_id            = local.server_id
  name                 = "tf-acc-list"
  entity_type          = "external"
  location             = "Online"
  scheduled_start_time = "` + eventTime(24*time.Hour) + `"
  scheduled_end_time   = "` + eventTime(25*time.Hour) + `"
}
resource "discord_application_command" "test" {
  server_id = local.server_id
  type      = "user"
  name      = "tf-acc-list"
}
resource "discord_application_emoji" "test" {
  name  = "tf_acc_list"
  image = "` + onePixelPNG + `"
}
`
	snapshot := stateSnapshot{}
	// Discord does not return uploaded files, so an import leaves them null.
	images := []string{"image", "file", "sound"}
	type listed struct {
		typ      string
		config   string
		identity map[string]string
		ignore   []string
	}
	lists := []listed{
		{"discord_role", server, map[string]string{"server_id": "server_id", "role_id": "id"}, nil},
		{"discord_category_channel", server, map[string]string{"channel_id": "id"}, nil},
		{"discord_text_channel", server, map[string]string{"channel_id": "id"}, nil},
		{"discord_voice_channel", server, map[string]string{"channel_id": "id"}, nil},
		{"discord_announcement_channel", server, map[string]string{"channel_id": "id"}, nil},
		{"discord_stage_channel", server, map[string]string{"channel_id": "id"}, nil},
		{"discord_forum_channel", server, map[string]string{"channel_id": "id"}, nil},
		{"discord_media_channel", server, map[string]string{"channel_id": "id"}, nil},
		{"discord_channel_permission", server, map[string]string{"channel_id": "channel_id", "overwrite_id": "overwrite_id"}, nil},
		{"discord_channel_follower", server, map[string]string{"webhook_id": "id"}, nil},
		{"discord_invite", server, map[string]string{"channel_id": "channel_id", "code": "id"}, nil},
		{"discord_thread", server, map[string]string{"thread_id": "id"}, nil},
		{"discord_webhook", server, map[string]string{"webhook_id": "id"}, nil},
		{"discord_emoji", server, map[string]string{"server_id": "server_id", "emoji_id": "id"}, images},
		{"discord_sticker", server, map[string]string{"server_id": "server_id", "sticker_id": "id"}, images},
		{"discord_soundboard_sound", server, map[string]string{"server_id": "server_id", "sound_id": "id"}, images},
		{"discord_auto_moderation_rule", server, map[string]string{"server_id": "server_id", "rule_id": "id"}, nil},
		{"discord_scheduled_event", server, map[string]string{"server_id": "server_id", "event_id": "id"}, nil},
		{"discord_application_command", server, map[string]string{"application_id": "application_id", "server_id": "server_id", "command_id": "id"}, nil},
		{"discord_application_emoji", "", map[string]string{"application_id": "application_id", "emoji_id": "id"}, images},
	}
	if env.userID != "" {
		cfg += `
resource "discord_member" "test" {
  server_id = local.server_id
  user_id   = local.user_id
  nick      = "tf-acc-list"
}
`
		lists = append(lists, listed{"discord_member", server, map[string]string{"server_id": "server_id", "user_id": "user_id"}, nil})
	}
	// Banning removes the member and a server has one template, so only the
	// fake runs these.
	if !env.live {
		cfg += `
resource "discord_ban" "test" {
  server_id = local.server_id
  user_id   = "` + env.fake.AddMember(env.serverID, "banned") + `"
}
resource "discord_server_template" "test" {
  server_id = local.server_id
  name      = "tf-acc-list"
}
`
		lists = append(lists,
			listed{"discord_ban", server, map[string]string{"server_id": "server_id", "user_id": "user_id"}, nil},
			listed{"discord_server_template", server, map[string]string{"server_id": "server_id", "code": "code"}, nil},
		)
	}
	query := func(includeResource bool) tfresource.TestStep {
		var blocks strings.Builder
		blocks.WriteString("provider \"discord\" {}\n")
		checks := make([]querycheck.QueryResultCheck, 0, len(lists))
		for _, l := range lists {
			blocks.WriteString(listBlock(l.typ, includeResource, l.config))
			checks = append(checks, listedAsInState{
				snapshot: snapshot, name: l.typ + ".test", identity: l.identity, resource: includeResource, ignore: l.ignore,
			})
		}
		return tfresource.TestStep{Query: true, Config: blocks.String(), QueryResultChecks: checks}
	}
	env.run(tfresource.TestCase{
		TerraformVersionChecks: requiresQuery,
		Steps: []tfresource.TestStep{
			{Config: env.config(cfg), Check: snapshot.capture()},
			query(false),
			query(true),
		},
	})
}

// TestAccListResourcesFilters covers what each list leaves out, the
// channel_id filters and the result limit.
func TestAccListResourcesFilters(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	managed := env.fake.AddManagedRole(env.serverID, "integration")
	text := env.seedChannel("tf-acc-list-text", discord.ChannelTypeText, 0, "")
	other := env.seedChannel("tf-acc-list-other", discord.ChannelTypeText, 1, "")
	ctx := context.Background()
	for _, ch := range []string{text, other} {
		if err := env.client.EditChannelPermission(ctx, ch, discord.Overwrite{ID: env.serverID, Type: 0, Deny: "1024"}); err != nil {
			t.Fatal(err)
		}
		if _, err := env.client.CreateWebhook(ctx, ch, discord.Payload{"name": "tf-acc-list"}); err != nil {
			t.Fatal(err)
		}
		if _, err := env.client.StartThread(ctx, ch, discord.Payload{"name": "tf-acc-list", "type": discord.ChannelTypePublicThread}); err != nil {
			t.Fatal(err)
		}
	}
	env.fake.AddChannelFollowerWebhook(text)
	appID, err := env.client.ApplicationID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	global, err := env.client.CreateApplicationCommand(ctx, appID, "", discord.Payload{"type": 2, "name": "tf-acc-global"})
	if err != nil {
		t.Fatal(err)
	}
	roles, err := env.client.ListRoles(ctx, env.serverID)
	if err != nil {
		t.Fatal(err)
	}
	unmanaged := len(roles) - 2
	channels, err := env.client.ListChannels(ctx, env.serverID)
	if err != nil {
		t.Fatal(err)
	}
	textChannels := 0
	for _, ch := range channels {
		if ch.Type == discord.ChannelTypeText {
			textChannels++
		}
	}
	server := fmt.Sprintf("server_id = %q", env.serverID)
	channel := fmt.Sprintf("server_id = %q\n    channel_id = %q", env.serverID, text)
	env.run(tfresource.TestCase{
		TerraformVersionChecks: requiresQuery,
		Steps: []tfresource.TestStep{
			{Config: env.config("")},
			{
				Query: true,
				Config: `provider "discord" {}` +
					listBlock("discord_role", false, server) +
					strings.Replace(listBlock("discord_text_channel", false, server), `"test"`, `"all"`, 1) +
					strings.Replace(listBlock("discord_text_channel", false, server), "include_resource", "limit            = 1\n  include_resource", 1) +
					listBlock("discord_channel_permission", false, channel) +
					strings.Replace(listBlock("discord_channel_permission", false, server), `"test"`, `"all"`, 1) +
					listBlock("discord_webhook", false, channel) +
					strings.Replace(listBlock("discord_webhook", false, server), `"test"`, `"all"`, 1) +
					listBlock("discord_channel_follower", false, server) +
					listBlock("discord_application_command", false, "") +
					listBlock("discord_thread", false, channel),
				QueryResultChecks: []querycheck.QueryResultCheck{
					querycheck.ExpectLength("discord_role.test", unmanaged),
					querycheck.ExpectNoIdentity("discord_role.test", map[string]knownvalue.Check{
						"server_id": knownvalue.StringExact(env.serverID), "role_id": knownvalue.StringExact(env.serverID),
					}),
					querycheck.ExpectNoIdentity("discord_role.test", map[string]knownvalue.Check{
						"server_id": knownvalue.StringExact(env.serverID), "role_id": knownvalue.StringExact(managed),
					}),
					querycheck.ExpectLength("discord_text_channel.all", textChannels),
					querycheck.ExpectLength("discord_text_channel.test", 1),
					querycheck.ExpectLength("discord_channel_permission.test", 1),
					querycheck.ExpectIdentity("discord_channel_permission.test", map[string]knownvalue.Check{
						"channel_id": knownvalue.StringExact(text), "overwrite_id": knownvalue.StringExact(env.serverID),
					}),
					querycheck.ExpectLength("discord_channel_permission.all", 2),
					querycheck.ExpectLength("discord_webhook.test", 1),
					querycheck.ExpectLength("discord_webhook.all", 2),
					querycheck.ExpectLength("discord_channel_follower.test", 1),
					querycheck.ExpectLength("discord_thread.test", 1),
					querycheck.ExpectLength("discord_application_command.test", 1),
					querycheck.ExpectIdentity("discord_application_command.test", map[string]knownvalue.Check{
						"application_id": knownvalue.StringExact(appID),
						"server_id":      knownvalue.Null(),
						"command_id":     knownvalue.StringExact(global.ID),
					}),
				},
			},
		},
	})
}

func TestAccListResourcesUnknownServer(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(tfresource.TestCase{
		TerraformVersionChecks: requiresQuery,
		Steps: []tfresource.TestStep{
			{Config: env.config("")},
			{
				Query:       true,
				Config:      `provider "discord" {}` + listBlock("discord_role", false, `server_id = "999999999999999999"`),
				ExpectError: regexp.MustCompile(`Unknown Guild`),
			},
		},
	})
}

// TestListResourceSkipsDeleted lists an object that is gone by the time its
// resource is read, which Discord's eventual consistency allows.
func TestListResourceSkipsDeleted(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	ctx := context.Background()
	role, err := env.client.CreateRole(ctx, env.serverID, discord.Payload{"name": "kept"})
	if err != nil {
		t.Fatal(err)
	}
	res := newRoleResource()
	l := newListResource(res, "", listServerIDAttr("roles"),
		func(context.Context, *discord.Client, map[string]string, int64) ([]listItem, error) {
			return []listItem{
				{identity: []string{env.serverID, "999999999999999999"}, name: "deleted"},
				{identity: []string{env.serverID, role.ID}, name: "kept"},
			}, nil
		})
	l.Configure(ctx, resource.ConfigureRequest{ProviderData: env.client}, &resource.ConfigureResponse{})
	var schemaResp resource.SchemaResponse
	res.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	var identityResp resource.IdentitySchemaResponse
	l.res.IdentitySchema(ctx, resource.IdentitySchemaRequest{}, &identityResp)
	var configResp list.ListResourceSchemaResponse
	l.ListResourceConfigSchema(ctx, list.ListResourceSchemaRequest{}, &configResp)
	configType := configResp.Schema.Type().TerraformType(ctx)
	req := list.ListRequest{
		Config: tfsdk.Config{Schema: configResp.Schema, Raw: tftypes.NewValue(configType, map[string]tftypes.Value{
			"server_id": tftypes.NewValue(tftypes.String, env.serverID),
		})},
		IncludeResource:        true,
		ResourceSchema:         schemaResp.Schema,
		ResourceIdentitySchema: identityResp.IdentitySchema,
	}
	var stream list.ListResultsStream
	l.List(ctx, req, &stream)
	var names []string
	for result := range stream.Results {
		if result.Diagnostics.HasError() {
			t.Fatalf("diagnostics: %v", result.Diagnostics)
		}
		names = append(names, result.DisplayName)
	}
	if !slices.Equal(names, []string{"kept"}) {
		t.Errorf("results = %v, want only the role that exists", names)
	}
}

func TestNewListResourceRequiresIdentity(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("want a panic for a resource without identity")
		}
	}()
	newListResource(newRolePositionsResource(), "", nil, nil)
}

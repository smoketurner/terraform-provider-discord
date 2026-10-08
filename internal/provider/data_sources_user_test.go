package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
	"github.com/smoketurner/terraform-provider-discord/internal/discord/discordtest"
)

func TestAccUserDataSources(t *testing.T) {
	env := newTestEnv(t)
	env.requireUser()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`
data "discord_user" "test" {
  id = local.user_id
}
data "discord_current_user" "bot" {}
data "discord_current_application" "app" {}
data "discord_user" "bot" {
  id = data.discord_current_user.bot.id
}
`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.discord_user.test", "id", env.userID),
				resource.TestCheckResourceAttrSet("data.discord_user.test", "username"),
				resource.TestCheckResourceAttr("data.discord_user.test", "bot", "false"),
				resource.TestCheckResourceAttr("data.discord_current_user.bot", "bot", "true"),
				resource.TestCheckResourceAttrSet("data.discord_current_user.bot", "username"),
				resource.TestCheckResourceAttrPair("data.discord_user.bot", "username", "data.discord_current_user.bot", "username"),
				resource.TestCheckResourceAttrPair("data.discord_current_application.app", "bot_id", "data.discord_current_user.bot", "id"),
				resource.TestCheckResourceAttrSet("data.discord_current_application.app", "id"),
				resource.TestCheckResourceAttrSet("data.discord_current_application.app", "name"),
				resource.TestCheckResourceAttrSet("data.discord_current_application.app", "verify_key"),
				resource.TestCheckResourceAttrSet("data.discord_current_application.app", "owner_id"),
			),
		}},
	})
}

// TestAccUserDataSourcesDrift checks that each read reflects changes made
// outside Terraform and that unset optional fields are null.
func TestAccUserDataSourcesDrift(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	cfg := env.config(`
data "discord_user" "test" {
  id = local.user_id
}
data "discord_current_user" "bot" {}
data "discord_current_application" "app" {}
`)
	attr := tfjsonpath.New[string]
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.discord_user.test", attr("username"), knownvalue.StringExact("tester")),
					statecheck.ExpectKnownValue("data.discord_user.test", attr("global_name"), knownvalue.Null()),
					statecheck.ExpectKnownValue("data.discord_user.test", attr("accent_color"), knownvalue.Null()),
					statecheck.ExpectKnownValue("data.discord_current_user.bot", attr("id"), knownvalue.StringExact("100000000000000003")),
					statecheck.ExpectKnownValue("data.discord_current_application.app", attr("id"), knownvalue.StringExact(discordtest.ApplicationID)),
					statecheck.ExpectKnownValue("data.discord_current_application.app", attr("owner_id"), knownvalue.StringExact(env.userID)),
					statecheck.ExpectKnownValue("data.discord_current_application.app", attr("server_id"), knownvalue.Null()),
					statecheck.ExpectKnownValue("data.discord_current_application.app", attr("tags"), knownvalue.SetSizeExact(0)),
				},
			},
			{
				PreConfig: func() {
					env.fake.UpdateUser(env.userID, func(u *discord.User) {
						name, banner, color := "Tester", "abc123", int64(0x5865F2)
						u.GlobalName, u.Banner, u.AccentColor, u.PublicFlags = &name, &banner, &color, 64
					})
					env.fake.UpdateUser("100000000000000003", func(u *discord.User) { u.Username = "renamed-bot" })
					env.fake.UpdateApplication(func(a *discord.Application) {
						url := "https://example.com/interactions"
						a.Name, a.GuildID, a.Tags, a.InteractionsEndpointURL = "Renamed", discordtest.GuildID, []string{"moderation"}, &url
						a.BotPublic, a.ApproximateGuildCount = true, 3
					})
				},
				Config: cfg,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.discord_user.test", attr("global_name"), knownvalue.StringExact("Tester")),
					statecheck.ExpectKnownValue("data.discord_user.test", attr("banner_hash"), knownvalue.StringExact("abc123")),
					statecheck.ExpectKnownValue("data.discord_user.test", attr("accent_color"), knownvalue.Int64Exact(0x5865F2)),
					statecheck.ExpectKnownValue("data.discord_user.test", attr("public_flags"), knownvalue.Int64Exact(64)),
					statecheck.ExpectKnownValue("data.discord_current_user.bot", attr("username"), knownvalue.StringExact("renamed-bot")),
					statecheck.ExpectKnownValue("data.discord_current_application.app", attr("name"), knownvalue.StringExact("Renamed")),
					statecheck.ExpectKnownValue("data.discord_current_application.app", attr("server_id"), knownvalue.StringExact(discordtest.GuildID)),
					statecheck.ExpectKnownValue("data.discord_current_application.app", attr("tags"), knownvalue.SetExact([]knownvalue.Check{knownvalue.StringExact("moderation")})),
					statecheck.ExpectKnownValue("data.discord_current_application.app", attr("interactions_endpoint_url"), knownvalue.StringExact("https://example.com/interactions")),
					statecheck.ExpectKnownValue("data.discord_current_application.app", attr("bot_public"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue("data.discord_current_application.app", attr("approximate_server_count"), knownvalue.Int64Exact(3)),
				},
			},
		},
	})
}

func TestAccUserDataSourceErrors(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: `
data "discord_user" "test" {
  id = "999999999999999999"
}`,
				ExpectError: regexp.MustCompile(`Unknown User`),
			},
			{
				Config: `
data "discord_user" "test" {
  id = "not-a-snowflake"
}`,
				ExpectError: regexp.MustCompile(`must be a Discord snowflake ID`),
			},
		},
	})
}

func TestAccPermissionsAndColorDataSources(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: `
data "discord_permissions" "test" {
  allow = ["view_channel", "SEND_MESSAGES"]
  deny  = [" ADMINISTRATOR "]
}
data "discord_permissions" "empty" {}
data "discord_color" "hex" {
  hex = "#5865F2"
}
data "discord_color" "short" {
  hex = "fff"
}
data "discord_color" "rgb" {
  rgb = [88, 101, 242]
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.discord_permissions.test", tfjsonpath.New("allow_bits"), knownvalue.StringExact("3072")),
					statecheck.ExpectKnownValue("data.discord_permissions.test", tfjsonpath.New("deny_bits"), knownvalue.StringExact("8")),
					statecheck.ExpectKnownValue("data.discord_permissions.empty", tfjsonpath.New("allow_bits"), knownvalue.StringExact("0")),
					statecheck.ExpectKnownValue("data.discord_permissions.empty", tfjsonpath.New("deny_bits"), knownvalue.StringExact("0")),
					statecheck.ExpectKnownValue("data.discord_color.hex", tfjsonpath.New("color"), knownvalue.Int64Exact(5793266)),
					statecheck.ExpectKnownValue("data.discord_color.short", tfjsonpath.New("color"), knownvalue.Int64Exact(16777215)),
					statecheck.ExpectKnownValue("data.discord_color.rgb", tfjsonpath.New("color"), knownvalue.Int64Exact(5793266)),
				},
			},
		},
	})
}

func TestAccPermissionsAndColorDataSourceErrors(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      `data "discord_permissions" "x" { allow = ["NOPE"] }`,
				ExpectError: regexp.MustCompile(`unknown permission "NOPE"`),
			},
			{
				Config:      `data "discord_permissions" "x" { deny = ["NOPE"] }`,
				ExpectError: regexp.MustCompile(`unknown permission "NOPE"`),
			},
			{
				Config: `
data "discord_permissions" "x" {
  allow = ["SEND_MESSAGES", "VIEW_CHANNEL"]
  deny  = ["send_messages", "view_channel", "CONNECT"]
}`,
				ExpectError: regexp.MustCompile(`Permissions in both allow and deny: SEND_MESSAGES, VIEW_CHANNEL\.`),
			},
			{
				Config:      `data "discord_color" "x" { hex = "#12345" }`,
				ExpectError: regexp.MustCompile(`invalid hex color`),
			},
			{
				Config:      `data "discord_color" "x" { rgb = [1, 2] }`,
				ExpectError: regexp.MustCompile(`list must contain at least 3 elements`),
			},
			{
				Config:      `data "discord_color" "x" { rgb = [1, 2, 256] }`,
				ExpectError: regexp.MustCompile(`value must be between 0 and 255`),
			},
			{
				Config:      `data "discord_color" "x" {}`,
				ExpectError: regexp.MustCompile(`Exactly one of these attributes must be configured`),
			},
			{
				Config: `
data "discord_color" "x" {
  hex = "#fff"
  rgb = [1, 2, 3]
}`,
				ExpectError: regexp.MustCompile(`Invalid Attribute Combination`),
			},
		},
	})
}

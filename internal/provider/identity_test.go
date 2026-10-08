package provider

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// identityImportStep imports a resource with an import block using the
// identity in state, and expects a no-op plan with the same identity.
func identityImportStep(name string) resource.TestStep {
	return resource.TestStep{
		ResourceName:    name,
		ImportState:     true,
		ImportStateKind: resource.ImportBlockWithResourceIdentity,
	}
}

// expectIdentity checks that a resource's identity has exactly the given
// attributes, each equal to the state attribute it maps to.
func expectIdentity(name string, attrs map[string]string) []statecheck.StateCheck {
	values := make(map[string]knownvalue.Check, len(attrs))
	checks := make([]statecheck.StateCheck, 0, len(attrs)+1)
	for identityAttr, stateAttr := range attrs {
		values[identityAttr] = knownvalue.NotNull()
		checks = append(checks, statecheck.ExpectIdentityValueMatchesStateAtPath(name, tfjsonpath.New(identityAttr), tfjsonpath.New(stateAttr)))
	}
	return append(checks, statecheck.ExpectIdentity(name, values))
}

// requiresIdentity skips Terraform versions without resource identity.
var requiresIdentity = []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_12_0)}

func TestAccResourceIdentity(t *testing.T) {
	env := newTestEnv(t)
	guild, err := env.client.GetGuild(context.Background(), env.serverID)
	if err != nil {
		t.Fatal(err)
	}
	everyone, err := env.client.GetRole(context.Background(), env.serverID, env.serverID)
	if err != nil {
		t.Fatal(err)
	}
	// The server-wide resources re-apply the server's current values, so
	// live runs change nothing.
	cfg := `
resource "discord_server_settings" "test" {
  server_id = local.server_id
  name      = "` + guild.Name + `"
}
resource "discord_role_everyone" "test" {
  server_id   = local.server_id
  permissions = "` + everyone.Permissions + `"
}
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-identity"
}
resource "discord_category_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-identity"
}
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-identity"
}
resource "discord_voice_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-identity"
}
resource "discord_announcement_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-identity"
}
resource "discord_stage_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-identity"
}
resource "discord_forum_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-identity"
}
resource "discord_media_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-identity"
}
resource "discord_channel_permission" "test" {
  channel_id   = discord_text_channel.test.id
  overwrite_id = discord_role.test.id
  type         = "role"
  allow        = provider::discord::permissions(["VIEW_CHANNEL"])
}
resource "discord_message" "test" {
  channel_id = discord_text_channel.test.id
  content    = "Identity"
}
resource "discord_invite" "test" {
  channel_id = discord_text_channel.test.id
}
resource "discord_thread" "test" {
  channel_id = discord_text_channel.test.id
  name       = "tf-acc-identity"
}
resource "discord_webhook" "test" {
  channel_id = discord_text_channel.test.id
  name       = "tf-acc-identity"
}
resource "discord_emoji" "test" {
  server_id = local.server_id
  name      = "tf_acc_identity"
  image     = "` + onePixelPNG + `"
}
resource "discord_auto_moderation_rule" "test" {
  server_id        = local.server_id
  name             = "tf-acc-identity"
  event_type       = "message_send"
  trigger_type     = "keyword"
  trigger_metadata = { keyword_filter = ["tf-acc-identity"] }
  actions          = [{ type = "block_message" }]
}
`
	identities := map[string]map[string]string{
		"discord_server_settings.test":      {"server_id": "server_id"},
		"discord_role_everyone.test":        {"server_id": "server_id"},
		"discord_role.test":                 {"server_id": "server_id", "role_id": "id"},
		"discord_category_channel.test":     {"channel_id": "id"},
		"discord_text_channel.test":         {"channel_id": "id"},
		"discord_voice_channel.test":        {"channel_id": "id"},
		"discord_announcement_channel.test": {"channel_id": "id"},
		"discord_stage_channel.test":        {"channel_id": "id"},
		"discord_forum_channel.test":        {"channel_id": "id"},
		"discord_media_channel.test":        {"channel_id": "id"},
		"discord_channel_permission.test":   {"channel_id": "channel_id", "overwrite_id": "overwrite_id"},
		"discord_message.test":              {"channel_id": "channel_id", "message_id": "id"},
		"discord_invite.test":               {"channel_id": "channel_id", "code": "id"},
		"discord_webhook.test":              {"webhook_id": "id"},
		"discord_thread.test":               {"thread_id": "id"},
		"discord_emoji.test":                {"server_id": "server_id", "emoji_id": "id"},
		"discord_auto_moderation_rule.test": {"server_id": "server_id", "rule_id": "id"},
	}
	if env.userID != "" {
		cfg += `
resource "discord_member_role" "test" {
  server_id = local.server_id
  user_id   = local.user_id
  role_id   = discord_role.test.id
}
resource "discord_member_roles" "test" {
  server_id = local.server_id
  user_id   = local.user_id
  role_ids  = [discord_role.test.id]
}
resource "discord_member" "test" {
  server_id = local.server_id
  user_id   = local.user_id
  nick      = "tf-acc-identity"
}
`
		identities["discord_member_role.test"] = map[string]string{"server_id": "server_id", "user_id": "user_id", "role_id": "role_id"}
		identities["discord_member_roles.test"] = map[string]string{"server_id": "server_id", "user_id": "user_id"}
		identities["discord_member.test"] = map[string]string{"server_id": "server_id", "user_id": "user_id"}
	}
	// These disable the server's onboarding, welcome screen and widget on
	// destroy, so live runs leave them out.
	if !env.live {
		cfg += `
resource "discord_onboarding" "test" {
  server_id = local.server_id
  enabled   = false
}
resource "discord_welcome_screen" "test" {
  server_id = local.server_id
  enabled   = false
}
resource "discord_server_widget" "test" {
  server_id = local.server_id
  enabled   = false
}
`
		for _, name := range []string{"discord_onboarding.test", "discord_welcome_screen.test", "discord_server_widget.test"} {
			identities[name] = map[string]string{"server_id": "server_id"}
		}
	}

	first := resource.TestStep{Config: env.config(cfg)}
	var steps []resource.TestStep
	for name, attrs := range identities {
		first.ConfigStateChecks = append(first.ConfigStateChecks, expectIdentity(name, attrs)...)
		step := identityImportStep(name)
		// The emoji image cannot be read back, so the imported emoji plans
		// to set it.
		step.ExpectNonEmptyPlan = name == "discord_emoji.test"
		steps = append(steps, step)
	}
	env.run(resource.TestCase{
		TerraformVersionChecks: requiresIdentity,
		Steps:                  append([]resource.TestStep{first}, steps...),
	})
}

func TestAccResourceIdentityFromImportID(t *testing.T) {
	env := newTestEnv(t)
	role, err := env.client.CreateRole(context.Background(), env.serverID, discord.Payload{"name": "tf-acc-identity"})
	if err != nil {
		t.Fatal(err)
	}
	env.run(resource.TestCase{
		TerraformVersionChecks: requiresIdentity,
		Steps: []resource.TestStep{{
			// A resource imported by its string ID gets its identity from
			// the read that follows the import.
			Config: env.config(`
import {
  to = discord_role.test
  id = "${local.server_id}/` + role.ID + `"
}
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-identity"
}`),
			ConfigStateChecks: expectIdentity("discord_role.test", map[string]string{
				"server_id": "server_id", "role_id": "id",
			}),
		}},
	})
}

func TestAccResourceIdentityImportErrors(t *testing.T) {
	env := newTestEnv(t)
	importRole := func(identity string) string {
		return env.config(`
import {
  to       = discord_role.test
  identity = ` + identity + `
}
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-identity"
}`)
	}
	env.run(resource.TestCase{
		TerraformVersionChecks: requiresIdentity,
		Steps: []resource.TestStep{
			{
				Config:      importRole(`{ server_id = local.server_id, role_id = "" }`),
				ExpectError: regexp.MustCompile(`Identity attribute "role_id" must not be empty`),
			},
			{
				Config:      importRole(`{ server_id = local.server_id }`),
				ExpectError: regexp.MustCompile(`role_id`),
			},
		},
	})
}

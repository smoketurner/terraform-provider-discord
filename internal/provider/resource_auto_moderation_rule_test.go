package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

const automodChannels = `
resource "discord_text_channel" "alerts" {
  server_id = local.server_id
  name      = "tf-acc-automod-alerts"
}
resource "discord_role" "mods" {
  server_id = local.server_id
  name      = "tf-acc-automod-mods"
}
`

// automodImportStep imports a rule by "server_id/rule_id".
func automodImportStep(name string, ignore ...string) resource.TestStep {
	step := importStep(name, ignore...)
	step.ImportStateIdFunc = func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return "", fmt.Errorf("resource %s not found in state", name)
		}
		return rs.Primary.Attributes["server_id"] + "/" + rs.Primary.ID, nil
	}
	return step
}

func TestAccAutoModerationRuleKeyword(t *testing.T) {
	env := newTestEnv(t)
	var ruleID string
	g := "/guilds/" + env.serverID
	created := env.config(automodChannels + `
resource "discord_auto_moderation_rule" "test" {
  server_id        = local.server_id
  name             = "tf-acc-keywords"
  event_type       = "message_send"
  trigger_type     = "keyword"
  enabled          = true
  audit_log_reason = "Block scams"
  trigger_metadata = {
    keyword_filter = ["free nitro", "*scam*"]
    regex_patterns = ["(b|c)at"]
    allow_list     = ["scampi"]
  }
  actions = [
    { type = "block_message", custom_message = "Scams are not allowed." },
    { type = "send_alert_message", channel_id = discord_text_channel.alerts.id },
    { type = "timeout", duration_seconds = 60 },
  ]
  exempt_role_ids    = [discord_role.mods.id]
  exempt_channel_ids = [discord_text_channel.alerts.id]
}`)
	updated := env.config(automodChannels + `
resource "discord_auto_moderation_rule" "test" {
  server_id        = local.server_id
  name             = "tf-acc-keywords-v2"
  event_type       = "message_send"
  trigger_type     = "keyword"
  audit_log_reason = "Block scams"
  trigger_metadata = {
    keyword_filter = ["free nitro"]
  }
  actions = [
    { type = "block_message" },
  ]
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: created,
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("discord_auto_moderation_rule.test", "id", &ruleID),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "server_id", env.serverID),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "trigger_type", "keyword"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "event_type", "message_send"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "enabled", "true"),
					resource.TestCheckTypeSetElemAttr("discord_auto_moderation_rule.test", "trigger_metadata.keyword_filter.*", "*scam*"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "trigger_metadata.keyword_filter.#", "2"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "trigger_metadata.regex_patterns.#", "1"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "trigger_metadata.allow_list.#", "1"),
					resource.TestCheckNoResourceAttr("discord_auto_moderation_rule.test", "trigger_metadata.presets"),
					resource.TestCheckNoResourceAttr("discord_auto_moderation_rule.test", "trigger_metadata.mention_total_limit"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "actions.#", "3"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "actions.0.custom_message", "Scams are not allowed."),
					resource.TestCheckResourceAttrPair("discord_auto_moderation_rule.test", "actions.1.channel_id", "discord_text_channel.alerts", "id"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "actions.2.duration_seconds", "60"),
					resource.TestCheckNoResourceAttr("discord_auto_moderation_rule.test", "actions.2.channel_id"),
					resource.TestCheckResourceAttrPair("discord_auto_moderation_rule.test", "exempt_role_ids.0", "discord_role.mods", "id"),
					resource.TestCheckResourceAttrPair("discord_auto_moderation_rule.test", "exempt_channel_ids.0", "discord_text_channel.alerts", "id"),
					resource.TestCheckResourceAttrSet("discord_auto_moderation_rule.test", "creator_id"),
					func(*terraform.State) error {
						if env.live {
							return nil
						}
						return env.expectReasons(map[string]string{"POST " + g + "/auto-moderation/rules": "Block scams"})
					},
				),
			},
			automodImportStep("discord_auto_moderation_rule.test", "audit_log_reason"),
			{
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_auto_moderation_rule.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "name", "tf-acc-keywords-v2"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "enabled", "false"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "trigger_metadata.keyword_filter.#", "1"),
					resource.TestCheckNoResourceAttr("discord_auto_moderation_rule.test", "trigger_metadata.regex_patterns"),
					resource.TestCheckNoResourceAttr("discord_auto_moderation_rule.test", "trigger_metadata.allow_list"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "actions.#", "1"),
					resource.TestCheckNoResourceAttr("discord_auto_moderation_rule.test", "actions.0.custom_message"),
					resource.TestCheckNoResourceAttr("discord_auto_moderation_rule.test", "exempt_role_ids"),
					resource.TestCheckNoResourceAttr("discord_auto_moderation_rule.test", "exempt_channel_ids"),
					func(*terraform.State) error {
						if env.live {
							return nil
						}
						return env.expectReasons(map[string]string{"PATCH " + g + "/auto-moderation/rules/" + ruleID: "Block scams"})
					},
				),
			},
			{
				// Changed outside Terraform: restored.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyAutoModerationRule(ctx, env.serverID, ruleID, discord.Payload{
						"enabled":          true,
						"trigger_metadata": discord.Payload{"keyword_filter": []string{"edited"}, "allow_list": []string{"ok"}},
					})
					return err
				}),
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_auto_moderation_rule.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "enabled", "false"),
					resource.TestCheckTypeSetElemAttr("discord_auto_moderation_rule.test", "trigger_metadata.keyword_filter.*", "free nitro"),
					resource.TestCheckNoResourceAttr("discord_auto_moderation_rule.test", "trigger_metadata.allow_list"),
				),
			},
			{
				// Deleted outside Terraform: created again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteAutoModerationRule(ctx, env.serverID, ruleID)
				}),
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_auto_moderation_rule.test", plancheck.ResourceActionCreate)},
				},
			},
			{
				// The trigger type cannot be changed in place.
				Config: env.config(automodChannels + `
resource "discord_auto_moderation_rule" "test" {
  server_id    = local.server_id
  name         = "tf-acc-profiles"
  event_type   = "member_update"
  trigger_type = "member_profile"
  trigger_metadata = {
    keyword_filter = ["admin"]
  }
  actions = [{ type = "block_member_interaction" }]
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_auto_moderation_rule.test", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "trigger_type", "member_profile"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "event_type", "member_update"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "actions.0.type", "block_member_interaction"),
				),
			},
		},
	})
}

func TestAccAutoModerationRuleOtherTriggers(t *testing.T) {
	env := newTestEnv(t)
	// A server has at most one rule of each of these trigger types, and
	// Community servers already have a mention_spam rule.
	env.requireFake()
	// keyword_preset rules accept up to 1000 allow_list entries, more than
	// the 100 keyword rules do.
	allow := make([]string, 150)
	for i := range allow {
		allow[i] = fmt.Sprintf("%q", fmt.Sprintf("word%d", i))
	}
	rules := env.config(`
resource "discord_auto_moderation_rule" "preset" {
  server_id    = local.server_id
  name         = "tf-acc-presets"
  event_type   = "message_send"
  trigger_type = "keyword_preset"
  trigger_metadata = {
    presets    = ["sexual_content", "slurs"]
    allow_list = [` + strings.Join(allow, ", ") + `]
  }
  actions = [{ type = "block_message" }]
}
resource "discord_auto_moderation_rule" "spam" {
  server_id    = local.server_id
  name         = "tf-acc-spam"
  event_type   = "message_send"
  trigger_type = "spam"
  actions      = [{ type = "block_message", custom_message = "Looks like spam." }]
}
resource "discord_auto_moderation_rule" "mentions" {
  server_id    = local.server_id
  name         = "tf-acc-mentions"
  event_type   = "message_send"
  trigger_type = "mention_spam"
  trigger_metadata = {
    mention_total_limit             = 10
    mention_raid_protection_enabled = true
  }
  actions = [{ type = "timeout", duration_seconds = 2419200 }]
}
`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: rules,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.preset", "trigger_metadata.presets.#", "2"),
					resource.TestCheckTypeSetElemAttr("discord_auto_moderation_rule.preset", "trigger_metadata.presets.*", "slurs"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.preset", "trigger_metadata.allow_list.#", "150"),
					resource.TestCheckNoResourceAttr("discord_auto_moderation_rule.preset", "trigger_metadata.keyword_filter"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.spam", "actions.0.custom_message", "Looks like spam."),
					resource.TestCheckNoResourceAttr("discord_auto_moderation_rule.spam", "trigger_metadata.mention_total_limit"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.mentions", "trigger_metadata.mention_total_limit", "10"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.mentions", "trigger_metadata.mention_raid_protection_enabled", "true"),
					resource.TestCheckResourceAttr("discord_auto_moderation_rule.mentions", "actions.0.duration_seconds", "2419200"),
				),
			},
			automodImportStep("discord_auto_moderation_rule.preset"),
			automodImportStep("discord_auto_moderation_rule.spam"),
			automodImportStep("discord_auto_moderation_rule.mentions"),
			{
				// Omitting the mention settings keeps Discord's values.
				Config: strings.Replace(rules, `
    mention_raid_protection_enabled = true`, "", 1),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// A server has at most one spam rule.
				Config: rules + `
resource "discord_auto_moderation_rule" "spam2" {
  server_id    = local.server_id
  name         = "tf-acc-spam-2"
  event_type   = "message_send"
  trigger_type = "spam"
  actions      = [{ type = "block_message" }]
  depends_on   = [discord_auto_moderation_rule.spam]
}`,
				ExpectError: regexp.MustCompile(`HTTP 400`),
			},
		},
	})
}

func TestAccAutoModerationRuleAdoptDefault(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	// Discord creates a mention spam rule in Community servers.
	rule, err := env.client.CreateAutoModerationRule(context.Background(), env.serverID, discord.Payload{
		"name": "Block Mention Spam", "event_type": 1, "trigger_type": 5, "enabled": true,
		"trigger_metadata": discord.Payload{"mention_total_limit": 20},
		"actions":          []discord.Payload{{"type": 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`
import {
  to = discord_auto_moderation_rule.test
  id = "${local.server_id}/` + rule.ID + `"
}
resource "discord_auto_moderation_rule" "test" {
  server_id    = local.server_id
  name         = "Block Mention Spam"
  event_type   = "message_send"
  trigger_type = "mention_spam"
  enabled      = true
  actions      = [{ type = "block_message" }]
}`),
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_auto_moderation_rule.test", plancheck.ResourceActionNoop)},
			},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "id", rule.ID),
				resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "trigger_metadata.mention_total_limit", "20"),
				resource.TestCheckResourceAttr("discord_auto_moderation_rule.test", "trigger_metadata.mention_raid_protection_enabled", "false"),
			),
		}},
	})
}

func TestAccAutoModerationRuleValidation(t *testing.T) {
	env := newTestEnv(t)
	rule := func(trigger, metadata, actions string) string {
		return env.config(fmt.Sprintf(`
resource "discord_auto_moderation_rule" "test" {
  server_id        = local.server_id
  name             = "tf-acc-invalid"
  event_type       = "message_send"
  trigger_type     = %q
  trigger_metadata = %s
  actions          = %s
}`, trigger, metadata, actions))
	}
	block := `[{ type = "block_message" }]`
	hundredOne := make([]string, 101)
	for i := range hundredOne {
		hundredOne[i] = fmt.Sprintf(`"w%d"`, i)
	}
	cases := []struct {
		config string
		err    string
	}{
		{rule("harmful_link", "null", block), `value must be one of`},
		{rule("spam", `{ keyword_filter = ["x"] }`, block), `keyword_filter does not apply to spam rules`},
		{rule("keyword", `{ presets = ["slurs"] }`, block), `presets does not apply to keyword rules`},
		{rule("keyword_preset", `{ mention_total_limit = 5 }`, block), `mention_total_limit does not apply to`},
		{rule("member_profile", `{ allow_list = [`+strings.Join(hundredOne, ", ")+`] }`, block), `at most 100 allow_list entries`},
		{rule("keyword", `{ keyword_filter = ["`+strings.Repeat("a", 61)+`"] }`, block), `string length must be between 1 and 60`},
		{rule("keyword", `{ regex_patterns = ["`+strings.Repeat("a", 261)+`"] }`, block), `string length must be between 1 and 260`},
		{rule("keyword", `{ keyword_filter = [] }`, block), `set must contain at least 1 elements`},
		{rule("keyword_preset", `{ presets = ["rude"] }`, block), `value must be one of`},
		{rule("mention_spam", `{ mention_total_limit = 51 }`, block), `value must be between 0 and 50`},
		{rule("keyword", `{ keyword_filter = ["x"] }`, `[]`), `list must contain at least 1 elements`},
		{rule("keyword", `{ keyword_filter = ["x"] }`, `[{ type = "send_alert_message" }]`), `A send_alert_message action requires channel_id`},
		{rule("keyword", `{ keyword_filter = ["x"] }`, `[{ type = "timeout" }]`), `A timeout action requires duration_seconds`},
		{rule("keyword", `{ keyword_filter = ["x"] }`, `[{ type = "timeout", duration_seconds = 2419201 }]`), `value must be between 0 and 2419200`},
		{rule("keyword", `{ keyword_filter = ["x"] }`, `[{ type = "timeout", duration_seconds = 60, custom_message = "no" }]`), `custom_message does not apply to a timeout action`},
		{rule("keyword", `{ keyword_filter = ["x"] }`, `[{ type = "block_message", custom_message = "`+strings.Repeat("a", 151)+`" }]`), `string length must be between 1 and 150`},
		{rule("keyword_preset", `{ presets = ["slurs"] }`, `[{ type = "timeout", duration_seconds = 60 }]`), `only allowed in keyword and mention_spam rules`},
		{rule("member_profile", `{ keyword_filter = ["x"] }`, `[{ type = "timeout", duration_seconds = 60 }]`), `only allowed in keyword and mention_spam rules`},
	}
	steps := make([]resource.TestStep, 0, len(cases))
	for _, c := range cases {
		steps = append(steps, resource.TestStep{Config: c.config, ExpectError: regexp.MustCompile(strings.ReplaceAll(c.err, " ", `\s+`))})
	}
	env.run(resource.TestCase{Steps: steps})
}

package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// onboardingConfig declares seven channels, a role and a custom emoji for
// onboarding to use, followed by the onboarding resource with body.
func (e *testEnv) onboardingConfig(body string) string {
	return e.config(`
resource "discord_text_channel" "test" {
  count     = 7
  server_id = local.server_id
  name      = "tf-acc-onboarding-${count.index}"
}
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-onboarding"
}
resource "discord_emoji" "test" {
  server_id = local.server_id
  name      = "tf_acc_onboarding"
  image     = "` + onePixelPNG + `"
}
resource "discord_onboarding" "test" {
  server_id = local.server_id
` + body + `
}`)
}

const (
	promptGames = `{
      title         = "What do you play?"
      single_select = true
      options = [
        {
          title       = "Chess"
          description = "Board games"
          emoji_name  = "♟️"
          channel_ids = [discord_text_channel.test[0].id]
        },
        {
          title    = "Cards"
          emoji_id = discord_emoji.test.id
          role_ids = [discord_role.test.id]
        },
      ]
    }`
	promptGamesReordered = `{
      title         = "What do you play?"
      single_select = true
      options = [
        {
          title    = "Cards"
          emoji_id = discord_emoji.test.id
          role_ids = [discord_role.test.id]
        },
        {
          title       = "Chess"
          description = "Board games"
          emoji_name  = "♟️"
          channel_ids = [discord_text_channel.test[0].id]
        },
      ]
    }`
	promptPronouns = `{
      title         = "Pronouns"
      type          = "dropdown"
      in_onboarding = false
      options       = [{ title = "Ask me", role_ids = [discord_role.test.id] }]
    }`
)

func TestAccOnboarding(t *testing.T) {
	env := newTestEnv(t)
	onboarding := "/guilds/" + env.serverID + "/onboarding"
	const name = "discord_onboarding.test"
	enabled := `
  enabled             = true
  default_channel_ids = discord_text_channel.test[*].id
  audit_log_reason    = "Set up onboarding"`
	var gamesID, pronounsID, chessID, cardsID string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.onboardingConfig(enabled + `
  prompts = [` + promptGames + `, ` + promptPronouns + `]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "id", env.serverID),
					resource.TestCheckResourceAttr(name, "enabled", "true"),
					resource.TestCheckResourceAttr(name, "mode", "default"),
					resource.TestCheckResourceAttr(name, "default_channel_ids.#", "7"),
					resource.TestCheckResourceAttr(name, "prompts.#", "2"),
					resource.TestCheckResourceAttr(name, "prompts.0.title", "What do you play?"),
					resource.TestCheckResourceAttr(name, "prompts.0.type", "multiple_choice"),
					resource.TestCheckResourceAttr(name, "prompts.0.single_select", "true"),
					resource.TestCheckResourceAttr(name, "prompts.0.required", "false"),
					resource.TestCheckResourceAttr(name, "prompts.0.in_onboarding", "true"),
					resource.TestCheckResourceAttr(name, "prompts.0.options.0.description", "Board games"),
					resource.TestCheckResourceAttr(name, "prompts.0.options.0.emoji_name", "♟️"),
					resource.TestCheckNoResourceAttr(name, "prompts.0.options.0.emoji_id"),
					resource.TestCheckResourceAttr(name, "prompts.0.options.0.channel_ids.#", "1"),
					resource.TestCheckNoResourceAttr(name, "prompts.0.options.0.role_ids"),
					resource.TestCheckResourceAttrPair(name, "prompts.0.options.1.emoji_id", "discord_emoji.test", "id"),
					// Only the ID was configured, so the name and animated
					// flag Discord reads back are not stored.
					resource.TestCheckNoResourceAttr(name, "prompts.0.options.1.emoji_name"),
					resource.TestCheckNoResourceAttr(name, "prompts.0.options.1.emoji_animated"),
					resource.TestCheckNoResourceAttr(name, "prompts.0.options.1.description"),
					resource.TestCheckResourceAttr(name, "prompts.1.type", "dropdown"),
					resource.TestCheckResourceAttr(name, "prompts.1.in_onboarding", "false"),
					captureAttr(name, "prompts.0.id", &gamesID),
					captureAttr(name, "prompts.1.id", &pronounsID),
					captureAttr(name, "prompts.0.options.0.id", &chessID),
					captureAttr(name, "prompts.0.options.1.id", &cardsID),
					func(*terraform.State) error {
						if env.live {
							return nil
						}
						return env.expectReasons(map[string]string{"PUT " + onboarding: "Set up onboarding"})
					},
				),
			},
			{
				ResourceName:            name,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"audit_log_reason"},
				ImportStateId:           env.serverID,
			},
			{
				// Reordering prompts and options keeps their IDs.
				Config: env.onboardingConfig(enabled + `
  prompts = [` + promptPronouns + `, ` + promptGamesReordered + `]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					checkOnboardingAttr("prompts.0.id", &pronounsID),
					checkOnboardingAttr("prompts.1.id", &gamesID),
					checkOnboardingAttr("prompts.1.options.0.id", &cardsID),
					checkOnboardingAttr("prompts.1.options.1.id", &chessID),
					resource.TestCheckResourceAttr(name, "prompts.1.options.1.title", "Chess"),
				),
			},
			{
				// Renaming a prompt replaces it with a new one.
				Config: env.onboardingConfig(enabled + `
  prompts = [` + strings.Replace(promptPronouns, `"Pronouns"`, `"Your pronouns"`, 1) + `, ` + promptGamesReordered + `]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "prompts.0.title", "Your pronouns"),
					checkOnboardingAttrChanged("prompts.0.id", &pronounsID),
					checkOnboardingAttr("prompts.1.id", &gamesID),
				),
			},
			{
				// Drift: onboarding is disabled and its prompts removed in the
				// Discord client.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyOnboarding(ctx, env.serverID, discord.Payload{"enabled": false, "prompts": []any{}})
					return err
				}),
				Config: env.onboardingConfig(enabled + `
  prompts = [` + promptGamesReordered + `]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "enabled", "true"),
					resource.TestCheckResourceAttr(name, "prompts.#", "1"),
					resource.TestCheckResourceAttr(name, "prompts.0.options.#", "2"),
					checkOnboardingAttrChanged("prompts.0.id", &gamesID),
				),
			},
			{
				// Removing prompts and default channels removes them all.
				Config: env.onboardingConfig(`
  enabled = false`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "enabled", "false"),
					resource.TestCheckNoResourceAttr(name, "prompts"),
					resource.TestCheckNoResourceAttr(name, "default_channel_ids"),
					func(*terraform.State) error {
						o, err := env.client.GetOnboarding(context.Background(), env.serverID)
						if err != nil {
							return err
						}
						if len(o.Prompts) != 0 || len(o.DefaultChannelIDs) != 0 {
							return fmt.Errorf("onboarding kept %d prompts and %d default channels", len(o.Prompts), len(o.DefaultChannelIDs))
						}
						return nil
					},
				),
			},
			{
				Config: env.onboardingConfig(enabled + `
  prompts = [` + promptPronouns + `]`),
			},
		},
		// Destroying disables onboarding and leaves its prompts.
		CheckDestroy: func(*terraform.State) error {
			o, err := env.client.GetOnboarding(context.Background(), env.serverID)
			if err != nil {
				return err
			}
			if o.Enabled {
				return errors.New("onboarding is still enabled after destroy")
			}
			if len(o.Prompts) != 1 {
				return fmt.Errorf("onboarding has %d prompts after destroy, want 1", len(o.Prompts))
			}
			return nil
		},
	})
}

// In advanced mode, channels of prompt options count towards the seven
// channels onboarding requires.
func TestAccOnboardingAdvancedMode(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.onboardingConfig(`
  enabled             = true
  mode                = "advanced"
  default_channel_ids = slice(discord_text_channel.test[*].id, 0, 4)
  prompts = [{
    title = "Topics"
    options = [{
      title       = "Everything else"
      channel_ids = slice(discord_text_channel.test[*].id, 4, 7)
    }]
  }]`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("discord_onboarding.test", "mode", "advanced"),
				resource.TestCheckResourceAttr("discord_onboarding.test", "default_channel_ids.#", "4"),
			),
		}},
	})
}

func TestAccOnboardingRequirements(t *testing.T) {
	env := newTestEnv(t)
	const ids = `"100000000000000011", "100000000000000012", "100000000000000013"`
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				// Known channels are checked at plan time.
				Config: env.config(`
resource "discord_onboarding" "test" {
  server_id           = local.server_id
  enabled             = true
  default_channel_ids = [` + ids + `]
}`),
				ExpectError: regexp.MustCompile(`requires at least 7 channels; 3 are configured`),
			},
			{
				Config: env.config(`
resource "discord_onboarding" "test" {
  server_id           = local.server_id
  enabled             = true
  mode                = "advanced"
  default_channel_ids = [` + ids + `]
  prompts = [{
    title   = "Topics"
    options = [{ title = "Chess", channel_ids = [` + ids + `, "100000000000000014"] }]
  }]
}`),
				ExpectError: regexp.MustCompile(`requires at least 7 channels; 4 are configured`),
			},
			{
				// Channels unknown at plan time are checked once known,
				// before onboarding is changed.
				Config: env.onboardingConfig(`
  enabled             = true
  default_channel_ids = slice(discord_text_channel.test[*].id, 0, 3)`),
				ExpectError: regexp.MustCompile(`(?s)Error running apply.*requires at least 7 channels; 3 are configured`),
			},
		},
	})
}

func TestAccOnboardingValidation(t *testing.T) {
	env := newTestEnv(t)
	config := func(prompts string) string {
		return env.config(`
resource "discord_onboarding" "test" {
  server_id = local.server_id
  enabled   = false
  prompts   = ` + prompts + `
}`)
	}
	option := `{ title = "Option", role_ids = ["100000000000000011"] }`
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      config(`[{ title = "Empty", options = [] }]`),
				ExpectError: regexp.MustCompile(`list must contain at least 1 elements and at\s+most 50`),
			},
			{
				Config:      config(`[` + strings.Repeat(`{ title = "P", options = [`+option+`] },`, 16) + `]`),
				ExpectError: regexp.MustCompile(`list must contain at most 15 elements`),
			},
			{
				Config:      config(`[{ title = "` + strings.Repeat("x", 101) + `", options = [` + option + `] }]`),
				ExpectError: regexp.MustCompile(`string length must be between 1 and\s+100`),
			},
			{
				Config:      config(`[{ title = "P", type = "checkbox", options = [` + option + `] }]`),
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
			{
				Config:      config(`[{ title = "P", options = [{ title = "` + strings.Repeat("x", 51) + `" }] }]`),
				ExpectError: regexp.MustCompile(`string length must be between 1 and\s+50`),
			},
			{
				// Discord rejects an option with neither roles nor channels.
				Config:      config(`[{ title = "P", options = [` + option + `, { title = "Nothing", role_ids = [] }] }]`),
				ExpectError: regexp.MustCompile(`Option "Nothing" of prompt "P" needs at least one of role_ids or\s+channel_ids`),
			},
			{
				Config:      config(`[{ title = "P", options = [{ title = "O", role_ids = ["admins"] }] }]`),
				ExpectError: regexp.MustCompile(`must be a Discord\s+snowflake ID`),
			},
		},
	})
}

// checkOnboardingAttr checks an attribute of discord_onboarding.test against
// a value captured in an earlier step.
func checkOnboardingAttr(attr string, want *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		return resource.TestCheckResourceAttr("discord_onboarding.test", attr, *want)(s)
	}
}

// checkOnboardingAttrChanged checks that an attribute of
// discord_onboarding.test differs from a value captured in an earlier step.
func checkOnboardingAttrChanged(attr string, old *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		var got string
		if err := captureAttr("discord_onboarding.test", attr, &got)(s); err != nil {
			return err
		}
		if got == *old {
			return fmt.Errorf("discord_onboarding.test %s is still %s", attr, got)
		}
		return nil
	}
}

func TestPlaceholderPromptID(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	first, second := placeholderPromptID(now, 0), placeholderPromptID(now, 1)
	if first == second {
		t.Fatalf("placeholder IDs are not unique: %s", first)
	}
	id, err := strconv.ParseInt(first, 10, 64)
	if err != nil || id <= 0 {
		t.Fatalf("placeholder ID %q is not a positive snowflake", first)
	}
	if ms := id>>22 + discordEpoch; ms != now.UnixMilli() {
		t.Errorf("placeholder ID timestamp = %d, want %d", ms, now.UnixMilli())
	}
}

func TestTakeByTitle(t *testing.T) {
	type item struct{ title, id string }
	titleOf := func(i item) types.String { return types.StringValue(i.title) }
	items := []item{{"a", "1"}, {"b", "2"}, {"a", "3"}}
	for _, want := range []string{"1", "3"} {
		got, ok := takeByTitle(&items, types.StringValue("a"), titleOf)
		if !ok || got.id != want {
			t.Fatalf("takeByTitle(a) = %v, %v; want %s", got, ok, want)
		}
	}
	if _, ok := takeByTitle(&items, types.StringValue("a"), titleOf); ok {
		t.Error("takeByTitle matched a title twice")
	}
	if len(items) != 1 || items[0].id != "2" {
		t.Errorf("remaining items = %v, want [b]", items)
	}
}

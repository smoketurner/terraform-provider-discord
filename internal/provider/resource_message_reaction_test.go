package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// reactionUserIDs returns the IDs of every user who reacted with emoji.
func reactionUserIDs(ctx context.Context, c *discord.Client, channelID, messageID, emoji string) ([]string, error) {
	var ids []string
	after := ""
	for {
		users, err := c.ListReactions(ctx, channelID, messageID, emoji, after)
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			ids = append(ids, u.ID)
		}
		if len(users) < discord.MaxReactionsPage {
			return ids, nil
		}
		after = users[len(users)-1].ID
	}
}

// checkOwnReaction checks whether the bot has reacted with emoji to
// discord_message.test.
func (e *testEnv) checkOwnReaction(emoji string, want bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs := s.RootModule().Resources["discord_message.test"]
		ctx := context.Background()
		me, err := e.client.GetCurrentUser(ctx)
		if err != nil {
			return err
		}
		ids, err := reactionUserIDs(ctx, e.client, rs.Primary.Attributes["channel_id"], rs.Primary.ID, emoji)
		if err != nil {
			return err
		}
		if got := slices.Contains(ids, me.ID); got != want {
			return fmt.Errorf("bot reacted with %s: %t, want %t", emoji, got, want)
		}
		return nil
	}
}

func TestAccMessageReaction(t *testing.T) {
	env := newTestEnv(t)
	var channelID, messageID, emojiID string
	base := `
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-reaction"
}
resource "discord_message" "test" {
  channel_id = discord_text_channel.test.id
  content    = "React to get a role"
}
resource "discord_emoji" "test" {
  server_id = local.server_id
  name      = "tf_acc_react"
  image     = "` + onePixelPNG + `"
}
`
	reactions := func(unicode string) string {
		return env.config(base + `
resource "discord_message_reaction" "unicode" {
  channel_id = discord_text_channel.test.id
  message_id = discord_message.test.id
  emoji      = "` + unicode + `"
}
resource "discord_message_reaction" "custom" {
  channel_id = discord_text_channel.test.id
  message_id = discord_message.test.id
  emoji      = "${discord_emoji.test.name}:${discord_emoji.test.id}"
}`)
	}
	cfg := reactions("👍")
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_message_reaction" "test" {
  channel_id = "1"
  message_id = "2"
  emoji      = "thumbsup"
}`),
				ExpectError: regexp.MustCompile(`must be a unicode emoji or a custom emoji as "name:id"`),
			},
			{
				Config: env.config(`
resource "discord_message_reaction" "test" {
  channel_id = "1"
  message_id = "2"
  emoji      = "a/👍"
}`),
				ExpectError: regexp.MustCompile(`must be a unicode emoji`),
			},
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("discord_text_channel.test", "id", &channelID),
					captureAttr("discord_message.test", "id", &messageID),
					captureAttr("discord_emoji.test", "id", &emojiID),
					resource.TestCheckResourceAttrPair("discord_message_reaction.unicode", "message_id", "discord_message.test", "id"),
					resource.TestCheckResourceAttr("discord_message_reaction.unicode", "emoji", "👍"),
					func(s *terraform.State) error {
						want := channelID + "/" + messageID + "/tf_acc_react:" + emojiID
						if got := s.RootModule().Resources["discord_message_reaction.custom"].Primary.ID; got != want {
							return fmt.Errorf("id = %q, want %q", got, want)
						}
						return nil
					},
					env.checkOwnReaction("👍", true),
				),
			},
			importStep("discord_message_reaction.unicode"),
			importStep("discord_message_reaction.custom"),
			{
				// The reaction is removed in the Discord client: added again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteOwnReaction(ctx, channelID, messageID, "👍")
				}),
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("discord_message_reaction.unicode", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction("discord_message_reaction.custom", plancheck.ResourceActionNoop),
					},
				},
				Check: env.checkOwnReaction("👍", true),
			},
			{
				// Changing the emoji removes the old reaction.
				Config: reactions("🎉"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("discord_message_reaction.unicode", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					env.checkOwnReaction("👍", false),
					env.checkOwnReaction("🎉", true),
				),
			},
			{
				// The custom emoji is deleted: the reaction went with it.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteEmoji(ctx, env.serverID, emojiID)
				}),
				Config: reactions("🎉"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("discord_message_reaction.custom", plancheck.ResourceActionCreate),
					},
				},
			},
			{
				// The message is deleted: the reactions went with it.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteMessage(ctx, channelID, messageID)
				}),
				Config: reactions("🎉"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("discord_message_reaction.unicode", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction("discord_message_reaction.custom", plancheck.ResourceActionCreate),
					},
				},
			},
		},
	})
}

func TestAccMessageReactionErrors(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-reaction"
}
resource "discord_message" "test" {
  channel_id = discord_text_channel.test.id
  content    = "React"
}
resource "discord_message_reaction" "test" {
  channel_id = discord_text_channel.test.id
  message_id = discord_message.test.id
  emoji      = "missing:123456"
}`),
				ExpectError: regexp.MustCompile(`(?s)Unable to add reaction:.*Unknown Emoji`),
			},
		},
	})
}

// TestAccMessageReactionPagination finds the bot's reaction past the first
// page of users.
func TestAccMessageReactionPagination(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	ctx := context.Background()
	ch, err := env.client.CreateChannel(ctx, env.serverID, discord.Payload{"name": "tf-acc-reaction", "type": discord.ChannelTypeText})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := env.client.CreateMessage(ctx, ch.ID, discord.Payload{"content": "Popular"})
	if err != nil {
		t.Fatal(err)
	}
	// These user IDs sort before the bot's, which lands on the third page.
	for i := range 2*discord.MaxReactionsPage + 2 {
		env.fake.AddReaction(msg.ID, "👍", strconv.Itoa(10000000000000000+i))
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_message_reaction" "test" {
  channel_id = "` + ch.ID + `"
  message_id = "` + msg.ID + `"
  emoji      = "👍"
}`),
				Check: env.checkReactionCount(ch.ID, msg.ID, "👍", 2*discord.MaxReactionsPage+3),
			},
		},
	})
}

func (e *testEnv) checkReactionCount(channelID, messageID, emoji string, want int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		ids, err := reactionUserIDs(context.Background(), e.client, channelID, messageID, emoji)
		if err != nil {
			return err
		}
		if len(ids) != want {
			return fmt.Errorf("%d users reacted, want %d", len(ids), want)
		}
		return nil
	}
}

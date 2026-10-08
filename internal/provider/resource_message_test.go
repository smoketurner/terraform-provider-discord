package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

const messageChannel = `
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-message-parts"
}
`

// messageImportStep imports discord_message.test by its channel and message
// IDs, which earlier steps capture.
func messageImportStep(channelID, messageID *string, ignore ...string) resource.TestStep {
	return resource.TestStep{
		ResourceName:            "discord_message.test",
		ImportState:             true,
		ImportStateVerify:       true,
		ImportStateVerifyIgnore: append([]string{"allowed_mentions"}, ignore...),
		ImportStateIdFunc:       func(*terraform.State) (string, error) { return *channelID + "/" + *messageID, nil },
	}
}

func expectMessageAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_message.test", action)}}
}

// lastEdit returns a field of the most recent message edit the fake received.
func lastEdit(env *testEnv, field string) (json.RawMessage, bool) {
	edits := env.fake.MessageEdits()
	if len(edits) == 0 {
		return nil, false
	}
	raw, ok := edits[len(edits)-1][field]
	return raw, ok
}

func TestAccMessageFlags(t *testing.T) {
	env := newTestEnv(t)
	var channelID, messageID string
	message := func(attrs string) string {
		return env.config(messageChannel + `
resource "discord_message" "test" {
  channel_id = discord_text_channel.test.id
  content    = "Docs: https://example.com/docs"
` + attrs + `
}`)
	}
	suppressed := message(`  suppress_embeds        = true
  suppress_notifications = true`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      message(`  suppress_embeds = true` + "\n" + `  embeds = [{ title = "Rules" }]`),
				ExpectError: regexp.MustCompile(`cannot be set with\s+embeds`),
			},
			{
				Config: suppressed,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_message.test", "suppress_embeds", "true"),
					resource.TestCheckResourceAttr("discord_message.test", "suppress_notifications", "true"),
					resource.TestCheckResourceAttr("discord_message.test", "components_v2", "false"),
					resource.TestCheckNoResourceAttr("discord_message.test", "embeds"),
					captureAttr("discord_message.test", "channel_id", &channelID),
					captureAttr("discord_message.test", "id", &messageID),
				),
			},
			messageImportStep(&channelID, &messageID),
			{
				// Link previews are shown again by editing the flags.
				Config:           message(`  suppress_notifications = true`),
				ConfigPlanChecks: expectMessageAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_message.test", "suppress_embeds", "false"),
					resource.TestCheckResourceAttr("discord_message.test", "suppress_notifications", "true"),
					captureAttr("discord_message.test", "id", &messageID),
					func(*terraform.State) error {
						if env.fake == nil {
							return nil
						}
						if raw, ok := lastEdit(env, "flags"); !ok || string(raw) != "4096" {
							return fmt.Errorf("edit sent flags %s, want 4096", raw)
						}
						return nil
					},
				),
			},
			{
				// Previews are hidden in the Discord client: hidden no longer.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.EditMessage(ctx, channelID, messageID, discord.Payload{
						"flags": discord.MessageFlagSuppressEmbeds | discord.MessageFlagSuppressNotifications,
					})
					return err
				}),
				Config:           message(`  suppress_notifications = true`),
				ConfigPlanChecks: expectMessageAction(plancheck.ResourceActionUpdate),
				Check:            resource.TestCheckResourceAttr("discord_message.test", "suppress_embeds", "false"),
			},
			{
				// Notifications can only be suppressed when posting.
				Config:           message(``),
				ConfigPlanChecks: expectMessageAction(plancheck.ResourceActionDestroyBeforeCreate),
				Check:            resource.TestCheckResourceAttr("discord_message.test", "suppress_notifications", "false"),
			},
		},
	})
}

func TestAccMessageAttachments(t *testing.T) {
	env := newTestEnv(t)
	dir := t.TempDir()
	rules := filepath.Join(dir, "rules.txt")
	if err := os.WriteFile(rules, []byte("Be nice."), 0o600); err != nil {
		t.Fatal(err)
	}
	var channelID, messageID, rulesID, bannerID string
	message := func(attachments string) string {
		return env.config(messageChannel + `
resource "discord_message" "test" {
  channel_id  = discord_text_channel.test.id
  content     = "Rules attached"
  attachments = [` + attachments + `]
}`)
	}
	rulesFile := `{
    filename    = "rules.txt"
    source      = "` + filepath.ToSlash(rules) + `"
    source_hash = filesha256("` + filepath.ToSlash(rules) + `")
    description = "Server rules"
  }`
	banner := `{
    filename       = "banner.png"
    content_base64 = "` + stickerPNG + `"
    spoiler        = true
  }`
	both := message(rulesFile + ", " + banner)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      message(`{ filename = "a.txt" }`),
				ExpectError: regexp.MustCompile(`(?s)No attribute specified when one \(and only one\) of.*source`),
			},
			{
				Config:      message(`{ filename = "a.txt", source = "` + filepath.ToSlash(filepath.Join(dir, "missing.txt")) + `" }`),
				ExpectError: regexp.MustCompile(`Unable to read attachment "a.txt"`),
			},
			{
				Config: env.config(messageChannel + `
resource "discord_message" "test" {
  channel_id  = discord_text_channel.test.id
  attachments = [` + rulesFile + ", " + banner + `]
  embeds      = [{ title = "Banner", image_url = "attachment://banner.png" }]
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_message.test", "attachments.#", "2"),
					resource.TestCheckResourceAttr("discord_message.test", "attachments.0.filename", "rules.txt"),
					resource.TestCheckResourceAttr("discord_message.test", "attachments.0.size", "8"),
					resource.TestCheckResourceAttr("discord_message.test", "attachments.0.description", "Server rules"),
					resource.TestCheckResourceAttr("discord_message.test", "attachments.0.spoiler", "false"),
					resource.TestCheckResourceAttr("discord_message.test", "attachments.1.spoiler", "true"),
					resource.TestCheckResourceAttr("discord_message.test", "attachments.1.content_type", "image/png"),
					resource.TestCheckResourceAttr("discord_message.test", "embeds.0.image_url", "attachment://banner.png"),
					captureAttr("discord_message.test", "attachments.0.id", &rulesID),
					captureAttr("discord_message.test", "attachments.1.id", &bannerID),
					captureAttr("discord_message.test", "channel_id", &channelID),
					captureAttr("discord_message.test", "id", &messageID),
				),
			},
			messageImportStep(&channelID, &messageID,
				"attachments.0.source", "attachments.0.source_hash", "attachments.1.content_base64",
				// Discord returns the URL an attachment:// reference resolves to.
				"embeds.0.image_url"),
			{
				// The banner is removed and the rules description edited in
				// place, keeping the rules file without uploading it again.
				Config: message(strings.Replace(rulesFile, "Server rules", "The rules", 1)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("discord_message.test", plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue("discord_message.test", tfjsonpath.New("attachments").AtSliceIndex(0).AtMapKey("id"),
							knownvalue.StringFunc(func(id string) error {
								if id != rulesID {
									return fmt.Errorf("planned ID %s, want the kept %s", id, rulesID)
								}
								return nil
							})),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_message.test", "attachments.#", "1"),
					resource.TestCheckResourceAttr("discord_message.test", "attachments.0.description", "The rules"),
					resource.TestCheckResourceAttrPtr("discord_message.test", "attachments.0.id", &rulesID),
					func(*terraform.State) error {
						if env.fake == nil {
							return nil
						}
						raw, _ := lastEdit(env, "attachments")
						if want := `[{"description":"The rules","id":"` + rulesID + `","is_spoiler":false}]`; string(raw) != want {
							return fmt.Errorf("edit sent attachments %s, want %s", raw, want)
						}
						if _, ok := lastEdit(env, "files[0]"); ok {
							return errors.New("edit uploaded a file it kept")
						}
						return nil
					},
				),
			},
			{
				// The banner is added again as a new upload.
				Config:           both,
				ConfigPlanChecks: expectMessageAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_message.test", "attachments.#", "2"),
					resource.TestCheckResourceAttrPtr("discord_message.test", "attachments.0.id", &rulesID),
					resource.TestCheckResourceAttrSet("discord_message.test", "attachments.1.id"),
					func(s *terraform.State) error {
						if id := s.RootModule().Resources["discord_message.test"].Primary.Attributes["attachments.1.id"]; id == bannerID {
							return fmt.Errorf("banner kept its old ID %s", id)
						}
						return nil
					},
				),
			},
			{
				// Changing the file's hash uploads it again.
				Config:           strings.ReplaceAll(both, `filesha256("`+filepath.ToSlash(rules)+`")`, `"v2"`),
				ConfigPlanChecks: expectMessageAction(plancheck.ResourceActionUpdate),
				Check: func(s *terraform.State) error {
					if id := s.RootModule().Resources["discord_message.test"].Primary.Attributes["attachments.0.id"]; id == rulesID {
						return fmt.Errorf("rules file kept its old ID %s", id)
					}
					return nil
				},
			},
			{
				// Every attachment is removed in the Discord client: uploaded
				// again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.EditMessage(ctx, channelID, messageID, discord.Payload{"attachments": []any{}})
					return err
				}),
				Config:           both,
				ConfigPlanChecks: expectMessageAction(plancheck.ResourceActionUpdate),
				Check:            resource.TestCheckResourceAttr("discord_message.test", "attachments.#", "2"),
			},
		},
	})
}

func TestAccMessageComponents(t *testing.T) {
	env := newTestEnv(t)
	var channelID, messageID string
	message := func(attrs string) string {
		return env.config(messageChannel + `
resource "discord_message" "test" {
  channel_id = discord_text_channel.test.id
` + attrs + `
}`)
	}
	button := func(label string) string {
		return `  content    = "Pick a role"
  components = jsonencode([{
    type = 1
    components = [{ type = 2, style = 1, label = "` + label + `", custom_id = "pick" }]
  }])`
	}
	v2 := message(`  components_v2 = true
  attachments   = [{ filename = "guide.pdf", content_base64 = "` + stickerPNG + `" }]
  components = jsonencode([
    { type = 10, content = "# Welcome" },
    { type = 13, file = { url = "attachment://guide.pdf" } },
  ])`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      message(`  components = "{}"`),
				ExpectError: regexp.MustCompile(`must be a JSON array of component objects`),
			},
			{
				Config:      message(`  components = jsonencode([{ type = 10, content = "hi" }])`),
				ExpectError: regexp.MustCompile(`only action rows \(type 1\)\s+can\s+be\s+top-level`),
			},
			{
				Config:      message(`  components = jsonencode([for i in range(6) : { type = 1, components = [] }])`),
				ExpectError: regexp.MustCompile(`at most 5 action rows,\s+got 6`),
			},
			{
				Config: message(`  components_v2 = true
  components    = jsonencode([for i in range(41) : { type = 10, content = "line" }])`),
				ExpectError: regexp.MustCompile(`at most 40 components in\s+total, got 41`),
			},
			{
				Config:      message(`  components_v2 = true` + "\n" + `  content = "text"` + "\n" + `  components = jsonencode([{ type = 10, content = "hi" }])`),
				ExpectError: regexp.MustCompile(`content cannot be set with components_v2`),
			},
			{
				Config:      message(`  components_v2 = true` + "\n" + `  content = "text"`),
				ExpectError: regexp.MustCompile(`components_v2 requires components`),
			},
			{
				Config: message(button("Red")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_message.test", "components",
						`[{"components":[{"custom_id":"pick","label":"Red","style":1,"type":2}],"type":1}]`),
					captureAttr("discord_message.test", "channel_id", &channelID),
					captureAttr("discord_message.test", "id", &messageID),
				),
			},
			// Discord adds component IDs, which the imported JSON includes.
			messageImportStep(&channelID, &messageID, "components"),
			{
				Config:           message(button("Blue")),
				ConfigPlanChecks: expectMessageAction(plancheck.ResourceActionUpdate),
			},
			{
				// The button is relabeled outside Terraform: changed back.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.EditMessage(ctx, channelID, messageID, discord.Payload{"components": json.RawMessage(
						`[{"type":1,"components":[{"type":2,"style":1,"label":"Green","custom_id":"pick"}]}]`)})
					return err
				}),
				Config:           message(button("Blue")),
				ConfigPlanChecks: expectMessageAction(plancheck.ResourceActionUpdate),
			},
			{
				// Turning Components V2 on edits the message and clears its
				// content.
				Config:           v2,
				ConfigPlanChecks: expectMessageAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_message.test", "components_v2", "true"),
					resource.TestCheckNoResourceAttr("discord_message.test", "content"),
					resource.TestCheckResourceAttr("discord_message.test", "attachments.0.filename", "guide.pdf"),
					resource.TestCheckResourceAttrPtr("discord_message.test", "id", &messageID),
					func(*terraform.State) error {
						if env.fake == nil {
							return nil
						}
						if raw, _ := lastEdit(env, "content"); string(raw) != "null" {
							return fmt.Errorf("edit sent content %s, want null", raw)
						}
						return nil
					},
				),
			},
			{
				// Components V2 cannot be turned off.
				Config:           message(button("Blue")),
				ConfigPlanChecks: expectMessageAction(plancheck.ResourceActionDestroyBeforeCreate),
				Check:            resource.TestCheckResourceAttr("discord_message.test", "components_v2", "false"),
			},
		},
	})
}

func TestAccMessageStickers(t *testing.T) {
	env := newTestEnv(t)
	var channelID, messageID string
	message := func(stickers string) string {
		return env.config(messageChannel + `
resource "discord_sticker" "test" {
  count     = 2
  server_id = local.server_id
  name      = "tf-acc-msg-${count.index}"
  tags      = "wave"
  file      = "` + stickerPNG + `"
}
resource "discord_message" "test" {
  channel_id  = discord_text_channel.test.id
  sticker_ids = ` + stickers + `
}`)
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      message(`["1", "2", "3", "4"]`),
				ExpectError: regexp.MustCompile(`list must contain at least 1 elements and at most 3`),
			},
			{
				Config: message(`[discord_sticker.test[0].id]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("discord_message.test", "sticker_ids.0", "discord_sticker.test.0", "id"),
					captureAttr("discord_message.test", "channel_id", &channelID),
					captureAttr("discord_message.test", "id", &messageID),
				),
			},
			messageImportStep(&channelID, &messageID),
			{
				Config:           message(`discord_sticker.test[*].id`),
				ConfigPlanChecks: expectMessageAction(plancheck.ResourceActionDestroyBeforeCreate),
				Check:            resource.TestCheckResourceAttr("discord_message.test", "sticker_ids.#", "2"),
			},
		},
	})
}

func TestAccMessagePoll(t *testing.T) {
	env := newTestEnv(t)
	var channelID, messageID string
	message := func(content, question string) string {
		return env.config(messageChannel + `
resource "discord_message" "test" {
  channel_id = discord_text_channel.test.id
  content    = "` + content + `"
  poll = {
    question = "` + question + `"
    answers = [
      { text = "Pizza", emoji_name = "🍕" },
      { text = "Tacos", emoji_id = "123456789012345678" },
      { text = "Salad" },
    ]
    duration          = 48
    allow_multiselect = true
  }
}`)
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(messageChannel + `
resource "discord_message" "test" {
  channel_id = discord_text_channel.test.id
  poll = {
    question = "Lunch?"
    answers  = [for i in range(11) : { text = "Answer ${i}" }]
  }
}`),
				ExpectError: regexp.MustCompile(`list must contain at least 1 elements and at most 10`),
			},
			{
				Config: env.config(messageChannel + `
resource "discord_message" "test" {
  channel_id = discord_text_channel.test.id
  poll = {
    question = "Lunch?"
    answers  = [{ text = "Pizza", emoji_name = "🍕", emoji_id = "123456789012345678" }]
  }
}`),
				ExpectError: regexp.MustCompile(`Invalid Attribute Combination`),
			},
			{
				Config: message("Vote below", "Lunch?"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_message.test", "poll.question", "Lunch?"),
					resource.TestCheckResourceAttr("discord_message.test", "poll.answers.#", "3"),
					resource.TestCheckResourceAttr("discord_message.test", "poll.answers.0.emoji_name", "🍕"),
					resource.TestCheckResourceAttr("discord_message.test", "poll.answers.1.emoji_id", "123456789012345678"),
					resource.TestCheckNoResourceAttr("discord_message.test", "poll.answers.1.emoji_name"),
					resource.TestCheckResourceAttr("discord_message.test", "poll.duration", "48"),
					resource.TestCheckResourceAttr("discord_message.test", "poll.allow_multiselect", "true"),
					captureAttr("discord_message.test", "channel_id", &channelID),
					captureAttr("discord_message.test", "id", &messageID),
				),
			},
			messageImportStep(&channelID, &messageID),
			{
				// A message with a poll cannot be edited.
				Config:           message("Vote now", "Lunch?"),
				ConfigPlanChecks: expectMessageAction(plancheck.ResourceActionDestroyBeforeCreate),
			},
			{
				Config:           message("Vote now", "Dinner?"),
				ConfigPlanChecks: expectMessageAction(plancheck.ResourceActionDestroyBeforeCreate),
				Check:            resource.TestCheckResourceAttr("discord_message.test", "poll.question", "Dinner?"),
			},
		},
	})
}

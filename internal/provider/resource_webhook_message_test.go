package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

const webhookMessageChannel = `
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-webhook-message"
}
resource "discord_webhook" "test" {
  channel_id    = discord_text_channel.test.id
  name          = "tf-acc-poster"
  store_secrets = false
}
`

// webhookMessageImportID returns the "webhook_id/channel_id/message_id"
// import ID of a discord_webhook_message.
func webhookMessageImportID(address string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[address]
		if !ok {
			return "", fmt.Errorf("resource %s not found in state", address)
		}
		a := rs.Primary.Attributes
		return a["webhook_id"] + "/" + a["channel_id"] + "/" + a["id"], nil
	}
}

func TestAccWebhookMessage(t *testing.T) {
	env := newTestEnv(t)
	message := func(attrs string) string {
		return env.config(webhookMessageChannel + `
resource "discord_webhook_message" "test" {
  webhook_id = discord_webhook.test.id
` + attrs + `
}`)
	}
	initial := message(`  content  = "Deploy started"
  username = "Deployer"
  embeds = [{
    title  = "Build"
    color  = 65280
    fields = [{ name = "Status", value = "running" }]
  }]`)
	edited := message(`  content          = "Deploy finished <@&123456789012345678>"
  username         = "Deployer"
  allowed_mentions = ["roles"]`)
	var webhookID, messageID string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      message(``),
				ExpectError: regexp.MustCompile(`At least one attribute out of`),
			},
			{
				Config: message(`  content     = "x"
  thread_id   = "123456789012345678"
  thread_name = "tf-acc-post"`),
				ExpectError: regexp.MustCompile(`cannot be specified when`),
			},
			{
				Config: message(`  content     = "x"
  thread_name = "tf-acc-post"`),
				ExpectError: regexp.MustCompile(`create threads in forum channels`),
			},
			{
				Config: initial,
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("discord_webhook.test", "id", &webhookID),
					captureAttr("discord_webhook_message.test", "id", &messageID),
					resource.TestCheckResourceAttrPair("discord_webhook_message.test", "channel_id", "discord_text_channel.test", "id"),
					resource.TestCheckResourceAttr("discord_webhook_message.test", "content", "Deploy started"),
					resource.TestCheckResourceAttr("discord_webhook_message.test", "embeds.0.title", "Build"),
					resource.TestCheckResourceAttr("discord_webhook_message.test", "embeds.0.fields.0.inline", "false"),
					env.noWebhookToken(),
					func(*terraform.State) error {
						token, err := env.webhookToken(webhookID)
						if err != nil {
							return err
						}
						msg, err := env.client.GetWebhookMessage(context.Background(), webhookID, token, messageID, "")
						if err != nil {
							return err
						}
						if msg.Author == nil || msg.Author.ID != webhookID || msg.Author.Username != "Deployer" {
							return fmt.Errorf("author = %+v, want the webhook named Deployer", msg.Author)
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "discord_webhook_message.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateIdFunc:       webhookMessageImportID("discord_webhook_message.test"),
				ImportStateVerifyIgnore: []string{"username"},
			},
			{
				ResourceName:  "discord_webhook_message.test",
				ImportState:   true,
				ImportStateId: "123",
				ExpectError:   regexp.MustCompile(`Invalid import ID`),
			},
			{
				// Content edits are in place and send allowed_mentions.
				Config: edited,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_webhook_message.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_webhook_message.test", "content", "Deploy finished <@&123456789012345678>"),
					resource.TestCheckNoResourceAttr("discord_webhook_message.test", "embeds"),
					func(*terraform.State) error {
						if !env.live {
							edits := env.fake.MessageEdits()
							last := edits[len(edits)-1]
							if got := string(last["allowed_mentions"]); got != `{"parse":["roles"]}` {
								return fmt.Errorf("allowed_mentions = %s", got)
							}
						}
						return nil
					},
				),
			},
			{
				// The message is edited in the Discord client: edited back.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					token, err := env.webhookToken(webhookID)
					if err != nil {
						return err
					}
					_, err = c.EditWebhookMessage(ctx, webhookID, token, messageID, "", discord.Payload{"content": "tampered"})
					return err
				}),
				Config: edited,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_webhook_message.test", plancheck.ResourceActionUpdate)},
				},
			},
			{
				// The message is deleted in the Discord client: posted again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					token, err := env.webhookToken(webhookID)
					if err != nil {
						return err
					}
					return c.DeleteWebhookMessage(ctx, webhookID, token, messageID, "")
				}),
				Config: edited,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_webhook_message.test", plancheck.ResourceActionCreate)},
				},
			},
			{
				// The username cannot be edited, so changing it posts again.
				Config: message(`  content          = "Deploy finished <@&123456789012345678>"
  username         = "Releaser"
  avatar_url       = "https://example.com/avatar.png"
  allowed_mentions = ["roles"]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_webhook_message.test", plancheck.ResourceActionReplace)},
				},
				Check: captureAttr("discord_webhook_message.test", "id", &messageID),
			},
			{
				// The webhook is deleted in the Discord client: both are
				// created again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteWebhook(ctx, webhookID)
				}),
				Config: edited,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("discord_webhook.test", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction("discord_webhook_message.test", plancheck.ResourceActionCreate),
					},
				},
			},
		},
	})
}

func TestAccWebhookMessageOmitsBotToken(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	cfg := env.config(webhookMessageChannel + `
resource "discord_webhook_message" "test" {
  webhook_id = discord_webhook.test.id
  content    = "hello"
}`)
	var webhookID, messageID string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("discord_webhook.test", "id", &webhookID),
					captureAttr("discord_webhook_message.test", "id", &messageID),
				),
			},
			{
				// By now the message has been posted and read back.
				Config: cfg,
				Check: func(*terraform.State) error {
					token, err := env.webhookToken(webhookID)
					if err != nil {
						return err
					}
					for _, key := range []string{
						"POST /webhooks/" + webhookID + "/" + token,
						"GET /webhooks/" + webhookID + "/" + token + "/messages/" + messageID,
					} {
						headers := env.fake.RequestHeaders(key)
						if len(headers) == 0 {
							return fmt.Errorf("%s was not requested", key)
						}
						for _, h := range headers {
							if h.Get("Authorization") != "" {
								return fmt.Errorf("%s sent the bot token", key)
							}
						}
					}
					return nil
				},
			},
		},
	})
}

func TestAccWebhookMessageWithoutToken(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	ch, err := env.client.CreateChannel(context.Background(), env.serverID, discord.Payload{"name": "tf-acc-follower", "type": 0})
	if err != nil {
		t.Fatal(err)
	}
	id := env.fake.AddChannelFollowerWebhook(ch.ID)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: `
resource "discord_webhook_message" "test" {
  webhook_id = "` + id + `"
  content    = "hello"
}`,
			ExpectError: regexp.MustCompile(`only\s+incoming\s+webhooks\s+can\s+post\s+messages`),
		}},
	})
}

func TestAccWebhookMessageForum(t *testing.T) {
	env := newTestEnv(t)
	forum := webhookMessageForum(env)
	post := forum + `
resource "discord_webhook_message" "post" {
  webhook_id  = discord_webhook.test.id
  thread_name = "tf-acc-release-notes"
  content     = "Version 1.0"
}`
	reply := func(content string) string {
		return post + `
resource "discord_webhook_message" "reply" {
  webhook_id = discord_webhook.test.id
  thread_id  = discord_webhook_message.post.channel_id
  content    = "` + content + `"
}`
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: forum + `
resource "discord_webhook_message" "post" {
  webhook_id = discord_webhook.test.id
  content    = "Version 1.0"
}`,
				ExpectError: regexp.MustCompile(`must have a thread_name or thread_id`),
			},
			{
				Config: reply("Changelog"),
				Check: resource.ComposeAggregateTestCheckFunc(
					// A post's ID is the ID of its starter message.
					resource.TestCheckResourceAttrPair("discord_webhook_message.post", "channel_id", "discord_webhook_message.post", "id"),
					resource.TestCheckResourceAttrPair("discord_webhook_message.reply", "channel_id", "discord_webhook_message.post", "id"),
				),
			},
			{
				ResourceName:      "discord_webhook_message.reply",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: webhookMessageImportID("discord_webhook_message.reply"),
			},
			{
				// Edits to messages in a post address the post's thread.
				Config: reply("Full changelog"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("discord_webhook_message.reply", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("discord_webhook_message.post", plancheck.ResourceActionNoop),
					},
				},
				Check: resource.TestCheckResourceAttr("discord_webhook_message.reply", "content", "Full changelog"),
			},
			{
				Config: forum + `
resource "discord_webhook_message" "post" {
  webhook_id  = discord_webhook.test.id
  thread_name = "tf-acc-release-notes"
  content     = "Version 1.0.1"
}`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("discord_webhook_message.post", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("discord_webhook_message.reply", plancheck.ResourceActionDestroy),
					},
				},
				Check: resource.TestCheckResourceAttr("discord_webhook_message.post", "content", "Version 1.0.1"),
			},
		},
	})
}

func webhookMessageForum(env *testEnv) string {
	return env.config(`
resource "discord_forum_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-webhook-forum"
}
resource "discord_webhook" "test" {
  channel_id    = discord_forum_channel.test.id
  name          = "tf-acc-forum-poster"
  store_secrets = false
}
`)
}

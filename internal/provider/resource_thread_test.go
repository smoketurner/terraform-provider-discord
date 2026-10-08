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

const threadForum = `
resource "discord_forum_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-thread-forum"
  available_tags = [
    { name = "question" },
    { name = "solved" },
  ]
}
`

func TestAccThreadForumPost(t *testing.T) {
	env := newTestEnv(t)
	var threadID string
	post := func(name, content, tag, extra string) string {
		return env.config(threadForum + fmt.Sprintf(`
resource "discord_thread" "test" {
  channel_id   = discord_forum_channel.test.id
  name         = %q
  applied_tags = [discord_forum_channel.test.available_tags[%s].id]
  message = {
    content = %q
    embeds  = [{ title = "Details", description = "Steps to reproduce." }]
  }
  %s
}`, name, tag, content, extra))
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: post("How do I deploy?", "I need help deploying.\n", "0", `
  pinned                = true
  rate_limit_per_user   = 30
  auto_archive_duration = 1440`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_thread.test", "type", "public_thread"),
					resource.TestCheckResourceAttr("discord_thread.test", "private", "false"),
					resource.TestCheckResourceAttr("discord_thread.test", "pinned", "true"),
					resource.TestCheckResourceAttr("discord_thread.test", "archived", "false"),
					resource.TestCheckResourceAttr("discord_thread.test", "locked", "false"),
					resource.TestCheckResourceAttr("discord_thread.test", "rate_limit_per_user", "30"),
					resource.TestCheckResourceAttr("discord_thread.test", "auto_archive_duration", "1440"),
					resource.TestCheckResourceAttr("discord_thread.test", "message.content", "I need help deploying.\n"),
					resource.TestCheckResourceAttr("discord_thread.test", "message.embeds.0.title", "Details"),
					resource.TestCheckResourceAttrPair("discord_thread.test", "applied_tags.0", "discord_forum_channel.test", "available_tags.0.id"),
					resource.TestCheckResourceAttrPair("discord_thread.test", "channel_id", "discord_forum_channel.test", "id"),
					resource.TestCheckResourceAttr("discord_thread.test", "server_id", env.serverID),
					resource.TestCheckResourceAttrSet("discord_thread.test", "owner_id"),
					captureAttr("discord_thread.test", "id", &threadID),
				),
			},
			// Import does not read the starter message; the next apply adopts
			// the configured one.
			importStep("discord_thread.test", "message"),
			{
				Config: post("Deploying with Terraform", "Solved: use Terraform.", "1", `
  pinned                = true
  rate_limit_per_user   = 30
  auto_archive_duration = 1440`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_thread.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_thread.test", "name", "Deploying with Terraform"),
					resource.TestCheckResourceAttr("discord_thread.test", "message.content", "Solved: use Terraform."),
					resource.TestCheckResourceAttrPair("discord_thread.test", "applied_tags.0", "discord_forum_channel.test", "available_tags.1.id"),
					resource.TestCheckResourceAttr("discord_thread.test", "pinned", "true"),
				),
			},
			{
				// Archiving unpins the post, so with pinned = true configured
				// the next apply unarchives and pins it again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyThread(ctx, threadID, discord.Payload{"archived": true})
					return err
				}),
				Config: post("Deploying with Terraform", "Solved: use Terraform.", "1", `
  pinned                = true
  rate_limit_per_user   = 30
  auto_archive_duration = 1440`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_thread.test", "pinned", "true"),
					resource.TestCheckResourceAttr("discord_thread.test", "archived", "false"),
				),
			},
			{
				// With archived = true the starter message is edited between
				// unarchiving and archiving the post again.
				Config: post("Deploying with Terraform", "Archived answer.", "1", `
  archived = true
  pinned   = false`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_thread.test", "archived", "true"),
					resource.TestCheckResourceAttr("discord_thread.test", "pinned", "false"),
					resource.TestCheckResourceAttr("discord_thread.test", "message.content", "Archived answer."),
					resource.TestCheckResourceAttr("discord_thread.test", "rate_limit_per_user", "30"),
				),
			},
			{
				// The starter message is edited outside Terraform: restored.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					if _, err := c.ModifyThread(ctx, threadID, discord.Payload{"archived": false}); err != nil {
						return err
					}
					_, err := c.EditMessage(ctx, threadID, threadID, discord.Payload{"content": "edited by a moderator"})
					return err
				}),
				Config: post("Deploying with Terraform", "Archived answer.", "1", `
  archived = true
  pinned   = false`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_thread.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_thread.test", "archived", "true"),
					resource.TestCheckResourceAttr("discord_thread.test", "message.content", "Archived answer."),
				),
			},
			{
				// Deleted by a moderator: posted again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteChannel(ctx, threadID)
				}),
				Config: post("Deploying with Terraform", "Archived answer.", "1", `
  archived = true
  pinned   = false`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_thread.test", plancheck.ResourceActionCreate)},
				},
				Check: resource.TestCheckResourceAttr("discord_thread.test", "archived", "true"),
			},
		},
	})
}

func TestAccThreadMediaPostStarterMessage(t *testing.T) {
	env := newTestEnv(t)
	media := `
resource "discord_media_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-thread-media"
}
`
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(media + `
resource "discord_thread" "test" {
  channel_id = discord_media_channel.test.id
  name       = "Gallery"
  message    = { embeds = [{ title = "Screenshots" }] }
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("discord_thread.test", "message.content"),
					resource.TestCheckResourceAttr("discord_thread.test", "message.embeds.0.title", "Screenshots"),
					resource.TestCheckResourceAttr("discord_thread.test", "applied_tags.#", "0"),
				),
			},
			{
				// No longer managed: the message is left as it is.
				Config: env.config(media + `
resource "discord_thread" "test" {
  channel_id = discord_media_channel.test.id
  name       = "Gallery"
}`),
				Check: resource.TestCheckNoResourceAttr("discord_thread.test", "message"),
			},
		},
	})
}

func TestAccThreadText(t *testing.T) {
	env := newTestEnv(t)
	var threadID string
	channels := `
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-thread-text"
}
resource "discord_announcement_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-thread-news"
}
resource "discord_message" "test" {
  channel_id = discord_text_channel.test.id
  content    = "Discuss the release here."
}
resource "discord_thread" "from_message" {
  channel_id = discord_text_channel.test.id
  message_id = discord_message.test.id
  name       = "Release discussion"
}
resource "discord_thread" "news" {
  channel_id = discord_announcement_channel.test.id
  name       = "Announcement follow-up"
}
resource "discord_thread" "private" {
  channel_id = discord_text_channel.test.id
  name       = "Moderators"
  private    = true
  invitable  = false
}
`
	public := func(extra string) string {
		return env.config(channels + fmt.Sprintf(`
resource "discord_thread" "test" {
  channel_id = discord_text_channel.test.id
  name       = "Public thread"
  %s
}`, extra))
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: public(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_thread.test", "type", "public_thread"),
					resource.TestCheckNoResourceAttr("discord_thread.test", "invitable"),
					resource.TestCheckResourceAttr("discord_thread.test", "pinned", "false"),
					resource.TestCheckResourceAttrPair("discord_thread.from_message", "id", "discord_message.test", "id"),
					resource.TestCheckResourceAttr("discord_thread.from_message", "type", "public_thread"),
					resource.TestCheckResourceAttr("discord_thread.news", "type", "announcement_thread"),
					resource.TestCheckResourceAttr("discord_thread.private", "type", "private_thread"),
					resource.TestCheckResourceAttr("discord_thread.private", "private", "true"),
					resource.TestCheckResourceAttr("discord_thread.private", "invitable", "false"),
					captureAttr("discord_thread.test", "id", &threadID),
				),
			},
			importStep("discord_thread.test"),
			importStep("discord_thread.private"),
			// message_id cannot be read back; the imported thread adopts the
			// configured value without being replaced.
			importStep("discord_thread.from_message", "message_id"),
			{
				// Archived after inactivity: not drift while archived is unset.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyThread(ctx, threadID, discord.Payload{"archived": true})
					return err
				}),
				Config:   public(""),
				PlanOnly: true,
			},
			{
				// Changing an archived thread unarchives it.
				Config: public(`locked = true`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_thread.test", "archived", "false"),
					resource.TestCheckResourceAttr("discord_thread.test", "locked", "true"),
				),
			},
			{
				Config: public(`archived = true`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_thread.test", "archived", "true"),
					resource.TestCheckResourceAttr("discord_thread.test", "locked", "false"),
				),
			},
			{
				// archived = false is enforced.
				Config: public(`archived = false`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_thread.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr("discord_thread.test", "archived", "false"),
			},
			{
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyThread(ctx, threadID, discord.Payload{"archived": true})
					return err
				}),
				Config: public(`archived = false`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_thread.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr("discord_thread.test", "archived", "false"),
			},
			{
				// Switching between public and private replaces the thread.
				Config: public(`private = true`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_thread.test", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_thread.test", "type", "private_thread"),
					resource.TestCheckResourceAttr("discord_thread.test", "invitable", "true"),
				),
			},
		},
	})
}

func TestAccThreadArchivedOnCreate(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-thread-archived"
}
resource "discord_thread" "test" {
  channel_id = discord_text_channel.test.id
  name       = "Archived"
  archived   = true
  locked     = true
}`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("discord_thread.test", "archived", "true"),
				resource.TestCheckResourceAttr("discord_thread.test", "locked", "true"),
			),
		}},
	})
}

func TestAccThreadValidation(t *testing.T) {
	env := newTestEnv(t)
	text := `
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-thread-invalid"
}
resource "discord_announcement_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-thread-invalid-news"
}
`
	step := func(hcl, want string) resource.TestStep {
		return resource.TestStep{Config: env.config(text + threadForum + hcl), ExpectError: regexp.MustCompile(want)}
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			step(`
resource "discord_thread" "test" {
  channel_id = discord_text_channel.test.id
  name       = "x"
  invitable  = true
}`, `invitable requires private = true`),
			step(`
resource "discord_thread" "test" {
  channel_id = discord_text_channel.test.id
  name       = "x"
  private    = true
  message_id = "1"
}`, `message_id cannot be set on a private thread`),
			step(`
resource "discord_thread" "test" {
  channel_id = discord_text_channel.test.id
  name       = "x"
  message_id = "1"
  message    = { content = "x" }
}`, `message cannot be set with message_id`),
			step(`
resource "discord_thread" "test" {
  channel_id = discord_forum_channel.test.id
  name       = "x"
  message    = { content = "x" }
  pinned     = true
  archived   = true
}`, `archiving a thread unpins it`),
			step(`
resource "discord_thread" "test" {
  channel_id = discord_forum_channel.test.id
  name       = "x"
  message    = {}
}`, `At least one attribute out of`),
			step(`
resource "discord_thread" "test" {
  channel_id   = discord_forum_channel.test.id
  name         = "x"
  message      = { content = "x" }
  applied_tags = ["1", "2", "3", "4", "5", "6"]
}`, `set must contain at most 5 elements`),
			step(`
resource "discord_thread" "test" {
  channel_id            = discord_text_channel.test.id
  name                  = "x"
  auto_archive_duration = 30
}`, `value must be one of`),
			step(`
resource "discord_thread" "test" {
  channel_id = discord_forum_channel.test.id
  name       = "x"
}`, `whose posts\s+need a starter message`),
			step(`
resource "discord_thread" "test" {
  channel_id = discord_forum_channel.test.id
  name       = "x"
  private    = true
}`, `forum or media channel, which has no\s+private threads`),
			step(`
resource "discord_thread" "test" {
  channel_id = discord_text_channel.test.id
  name       = "x"
  message    = { content = "x" }
}`, `is not a forum or media channel`),
			step(`
resource "discord_thread" "test" {
  channel_id = discord_announcement_channel.test.id
  name       = "x"
  private    = true
}`, `announcement channel, which has no\s+private threads`),
		},
	})
}

func TestAccThreadNotAThread(t *testing.T) {
	env := newTestEnv(t)
	channel := env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-thread-not"
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{Config: channel},
			{
				Config:       channel + "\nresource \"discord_thread\" \"test\" {\n  channel_id = \"1\"\n  name = \"x\"\n}",
				ResourceName: "discord_thread.test",
				ImportState:  true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return s.RootModule().Resources["discord_text_channel.test"].Primary.ID, nil
				},
				ExpectError: regexp.MustCompile(`which is not a thread`),
			},
		},
	})
}

func TestAccThreadAuditLogReason(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	var forumID, threadID string
	config := func(content string) string {
		return env.config(threadForum + fmt.Sprintf(`
resource "discord_thread" "test" {
  channel_id       = discord_forum_channel.test.id
  name             = "Audited"
  pinned           = true
  message          = { content = %q }
  audit_log_reason = "Thread for ops"
}`, content))
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: config("first"),
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("discord_forum_channel.test", "id", &forumID),
					captureAttr("discord_thread.test", "id", &threadID),
					func(*terraform.State) error {
						return env.expectReasons(map[string]string{
							"POST /channels/" + forumID + "/threads": "Thread for ops",
							"PATCH /channels/" + threadID:            "Thread for ops",
						})
					},
				),
			},
			{
				Config: config("second"),
				Check: func(*terraform.State) error {
					return env.expectReasons(map[string]string{
						"PATCH /channels/" + threadID + "/messages/" + threadID: "",
					})
				},
			},
		},
		CheckDestroy: func(*terraform.State) error {
			return env.expectReasons(map[string]string{"DELETE /channels/" + threadID: "Thread for ops"})
		},
	})
}

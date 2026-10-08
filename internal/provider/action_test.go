package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// requiresActions skips Terraform versions without actions.
var requiresActions = []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_14_0)}

// triggerAction runs the named actions after terraform_data.trigger is
// created and each time its input changes.
func triggerAction(input string, actions ...string) string {
	for i, a := range actions {
		actions[i] = "action." + a
	}
	return `
resource "terraform_data" "trigger" {
  input = "` + input + `"
  lifecycle {
    action_trigger {
      events  = [after_create, after_update]
      actions = [` + strings.Join(actions, ", ") + `]
    }
  }
}
`
}

// actionCase runs action tests against the fake, whose state they inspect,
// and only on Terraform versions with actions.
func (e *testEnv) actionCase(steps ...resource.TestStep) {
	e.t.Helper()
	e.requireFake()
	e.run(resource.TestCase{TerraformVersionChecks: requiresActions, Steps: steps})
}

// newChannel creates a channel of the given type outside Terraform.
func (e *testEnv) newChannel(name string, channelType int) string {
	e.t.Helper()
	ch, err := e.client.CreateChannel(context.Background(), e.serverID, discord.Payload{"name": name, "type": channelType})
	if err != nil {
		e.t.Fatal(err)
	}
	e.cleanup(func(ctx context.Context, c *discord.Client) error { return c.DeleteChannel(ctx, ch.ID) })
	return ch.ID
}

// newMessage posts a message outside Terraform.
func (e *testEnv) newMessage(channelID, content string) string {
	e.t.Helper()
	m, err := e.client.CreateMessage(context.Background(), channelID, discord.Payload{"content": content})
	if err != nil {
		e.t.Fatal(err)
	}
	return m.ID
}

func (e *testEnv) channelMessages(channelID string, want ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := e.fake.ChannelMessages(channelID); !slices.Equal(got, want) {
			return fmt.Errorf("channel messages = %q, want %q", got, want)
		}
		return nil
	}
}

func TestAccActionSendMessage(t *testing.T) {
	env := newTestEnv(t)
	channelID := env.newChannel("tf-acc-send", discord.ChannelTypeText)
	cfg := func(input, attrs string) string {
		return env.config(`
action "discord_send_message" "test" {
  config {
` + attrs + `
  }
}
` + triggerAction(input, "discord_send_message.test"))
	}
	env.actionCase(
		resource.TestStep{
			Config:      cfg("v1", `channel_id = "`+channelID+`"`+"\n"+`content = ""`),
			ExpectError: regexp.MustCompile(`string length must be between 1 and 2000`),
		},
		resource.TestStep{
			Config:      cfg("v1", `channel_id = "`+channelID+`"`+"\n"+`content = "x"`+"\n"+`allowed_mentions = ["here"]`),
			ExpectError: regexp.MustCompile(`value must be one of`),
		},
		resource.TestStep{
			Config: cfg("v1", `channel_id = "`+channelID+`"`+"\n"+`content = "Deployed v1"`),
			Check: resource.ComposeAggregateTestCheckFunc(
				env.channelMessages(channelID, "Deployed v1"),
				func(*terraform.State) error {
					body := env.fake.LastRequestBody("POST /channels/" + channelID + "/messages")
					if got := string(body["allowed_mentions"]); got != `{"parse":[]}` {
						return fmt.Errorf("allowed_mentions = %s", got)
					}
					return nil
				},
			),
		},
		resource.TestStep{
			// Every run posts a new message.
			Config: cfg("v2", `channel_id = "`+channelID+`"`+"\n"+`content = "Deployed v2"`+"\n"+`allowed_mentions = ["roles"]`),
			Check: resource.ComposeAggregateTestCheckFunc(
				env.channelMessages(channelID, "Deployed v1", "Deployed v2"),
				func(*terraform.State) error {
					body := env.fake.LastRequestBody("POST /channels/" + channelID + "/messages")
					if got := string(body["allowed_mentions"]); got != `{"parse":["roles"]}` {
						return fmt.Errorf("allowed_mentions = %s", got)
					}
					return nil
				},
			),
		},
		resource.TestStep{
			Config:      cfg("v3", `content = "Deployed v3"`+"\n"+`channel_id = "1"`),
			ExpectError: regexp.MustCompile(`Unable to send message`),
		},
	)
}

func TestAccActionCrosspostMessage(t *testing.T) {
	env := newTestEnv(t)
	announcements := env.newChannel("tf-acc-news", discord.ChannelTypeAnnouncement)
	text := env.newChannel("tf-acc-text", discord.ChannelTypeText)
	news := env.newMessage(announcements, "Release notes")
	chat := env.newMessage(text, "Chat")
	cfg := func(input, channelID, messageID string) string {
		return env.config(`
action "discord_crosspost_message" "test" {
  config {
    channel_id = "` + channelID + `"
    message_id = "` + messageID + `"
  }
}
` + triggerAction(input, "discord_crosspost_message.test"))
	}
	env.actionCase(
		resource.TestStep{
			Config:      cfg("v1", announcements, "news"),
			ExpectError: regexp.MustCompile(`must be a Discord snowflake ID`),
		},
		resource.TestStep{
			Config: cfg("v1", announcements, news),
			Check: func(*terraform.State) error {
				if !env.fake.Crossposted(news) {
					return fmt.Errorf("message %s was not crossposted", news)
				}
				return nil
			},
		},
		resource.TestStep{
			Config:      cfg("v2", announcements, news),
			ExpectError: regexp.MustCompile(`already been crossposted`),
		},
		resource.TestStep{
			Config:      cfg("v3", text, chat),
			ExpectError: regexp.MustCompile(`Unable to crosspost message`),
		},
	)
}

func TestAccActionEndPoll(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	channelID := env.newChannel("tf-acc-poll", discord.ChannelTypeText)
	poll := env.fake.AddPoll(channelID)
	chat := env.newMessage(channelID, "Not a poll")
	cfg := func(input, messageID string) string {
		return env.config(`
action "discord_end_poll" "test" {
  config {
    channel_id = "` + channelID + `"
    message_id = "` + messageID + `"
  }
}
` + triggerAction(input, "discord_end_poll.test"))
	}
	env.actionCase(
		resource.TestStep{
			Config: cfg("v1", poll),
			Check: func(*terraform.State) error {
				if !env.fake.PollEnded(poll) {
					return fmt.Errorf("poll %s did not end", poll)
				}
				return nil
			},
		},
		resource.TestStep{
			Config:      cfg("v2", poll),
			ExpectError: regexp.MustCompile(`has already ended`),
		},
		resource.TestStep{
			Config:      cfg("v3", chat),
			ExpectError: regexp.MustCompile(`Cannot expire a non-poll message`),
		},
	)
}

func TestAccActionBulkDeleteMessages(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	channelID := env.newChannel("tf-acc-bulk", discord.ChannelTypeText)
	first := env.newMessage(channelID, "spam 1")
	second := env.newMessage(channelID, "spam 2")
	env.newMessage(channelID, "keep")
	fourth := env.newMessage(channelID, "spam 3")
	fifth := env.newMessage(channelID, "spam 4")
	// The snowflake of a message posted in 2015, long past the two weeks.
	const old = "100000000000000000"
	cfg := func(input string, ids ...string) string {
		return env.config(`
action "discord_bulk_delete_messages" "test" {
  config {
    channel_id       = "` + channelID + `"
    message_ids      = ["` + strings.Join(ids, `", "`) + `"]
    audit_log_reason = "cleanup"
  }
}
` + triggerAction(input, "discord_bulk_delete_messages.test"))
	}
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = first + strconv.Itoa(i)
	}
	env.actionCase(
		resource.TestStep{
			Config:      cfg("v1", first),
			ExpectError: regexp.MustCompile(`set must contain at least 2 elements and at most 100`),
		},
		resource.TestStep{
			Config:      cfg("v1", tooMany...),
			ExpectError: regexp.MustCompile(`set must contain at least 2 elements and at most 100`),
		},
		resource.TestStep{
			Config:      cfg("v1", first, old),
			ExpectError: regexp.MustCompile(`Messages too old to bulk delete`),
		},
		resource.TestStep{
			Config: cfg("v1", first, second),
			Check: resource.ComposeAggregateTestCheckFunc(
				env.channelMessages(channelID, "keep", "spam 3", "spam 4"),
				func(*terraform.State) error {
					return env.expectReasons(map[string]string{"POST /channels/" + channelID + "/messages/bulk-delete": "cleanup"})
				},
			),
		},
		resource.TestStep{
			// Messages that are recent to the provider but older than two
			// weeks to Discord fail on Discord's side.
			PreConfig:   func() { env.fake.AdvanceClock(15 * 24 * time.Hour) },
			Config:      cfg("v2", fourth, fifth),
			ExpectError: regexp.MustCompile(`too old to bulk delete`),
		},
	)
}

func TestAccActionPruneMembers(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	g := "/guilds/" + env.serverID
	idle := env.fake.AddMember(env.serverID, "idle")
	role, err := env.client.CreateRole(context.Background(), env.serverID, discord.Payload{"name": "visitor"})
	if err != nil {
		t.Fatal(err)
	}
	visitor := env.fake.AddMember(env.serverID, "visitor")
	if err := env.client.AddMemberRole(context.Background(), env.serverID, visitor, role.ID); err != nil {
		t.Fatal(err)
	}
	cfg := func(input, attrs string) string {
		return env.config(`
action "discord_prune_members" "test" {
  config {
    server_id = local.server_id
` + attrs + `
  }
}
` + triggerAction(input, "discord_prune_members.test"))
	}
	isMember := func(userID string, want bool) resource.TestCheckFunc {
		return func(*terraform.State) error {
			_, err := env.client.GetMember(context.Background(), env.serverID, userID)
			if got := err == nil; got != want {
				return fmt.Errorf("member %s present = %v, want %v (err %w)", userID, got, want, err)
			}
			return nil
		}
	}
	env.actionCase(
		resource.TestStep{
			Config:      cfg("v1", `days = 31`),
			ExpectError: regexp.MustCompile(`value must be between 1 and 30`),
		},
		resource.TestStep{
			Config: cfg("v1", `
    days             = 30
    include_role_ids = ["`+role.ID+`"]
    dry_run          = true`),
			Check: resource.ComposeAggregateTestCheckFunc(
				isMember(idle, true),
				isMember(visitor, true),
				func(*terraform.State) error {
					if !slices.Contains(env.fake.Requests(), "GET "+g+"/prune") || len(env.fake.Prunes()) != 0 {
						return fmt.Errorf("dry run pruned: %v", env.fake.Prunes())
					}
					return nil
				},
			),
		},
		resource.TestStep{
			// Members with roles are kept unless their roles are included.
			Config: cfg("v2", `
    days                = 14
    compute_prune_count = false
    audit_log_reason    = "inactive"`),
			Check: resource.ComposeAggregateTestCheckFunc(
				isMember(idle, false),
				isMember(visitor, true),
				isMember(env.userID, true),
				func(*terraform.State) error {
					prunes := env.fake.Prunes()
					if len(prunes) != 1 || prunes[0].Days != 14 || prunes[0].IncludeRoles != nil {
						return fmt.Errorf("prunes = %+v", prunes)
					}
					return env.expectReasons(map[string]string{"POST " + g + "/prune": "inactive"})
				},
			),
		},
		resource.TestStep{
			Config: cfg("v3", `include_role_ids = ["`+role.ID+`"]`),
			Check: resource.ComposeAggregateTestCheckFunc(
				isMember(visitor, false),
				func(*terraform.State) error {
					prunes := env.fake.Prunes()
					if len(prunes) != 2 || prunes[1].Days != 7 || !slices.Equal(prunes[1].IncludeRoles, []string{role.ID}) {
						return fmt.Errorf("prunes = %+v", prunes)
					}
					return nil
				},
			),
		},
		resource.TestStep{
			Config:      cfg("v4", `include_role_ids = ["1"]`),
			ExpectError: regexp.MustCompile(`Unable to prune members`),
		},
	)
}

func TestAccActionBulkBan(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	first := env.fake.AddMember(env.serverID, "raider1")
	second := env.fake.AddMember(env.serverID, "raider2")
	cfg := func(input, attrs string) string {
		return env.config(`
action "discord_bulk_ban" "test" {
  config {
    server_id = local.server_id
` + attrs + `
  }
}
` + triggerAction(input, "discord_bulk_ban.test"))
	}
	env.actionCase(
		resource.TestStep{
			Config:      cfg("v1", `user_ids = []`),
			ExpectError: regexp.MustCompile(`set must contain at least 1 elements and at most 200`),
		},
		resource.TestStep{
			Config:      cfg("v1", `user_ids = ["`+first+`"]`+"\n"+`delete_message_seconds = 604801`),
			ExpectError: regexp.MustCompile(`value must be between 0 and 604800`),
		},
		resource.TestStep{
			Config: cfg("v1", `
    user_ids               = ["`+first+`"]
    delete_message_seconds = 3600
    audit_log_reason       = "raid"`),
			Check: resource.ComposeAggregateTestCheckFunc(
				env.banReason(first, new("raid")),
				env.notBanned(second),
				func(*terraform.State) error {
					body := env.fake.LastRequestBody("POST /guilds/" + env.serverID + "/bulk-ban")
					if got := string(body["delete_message_seconds"]); got != "3600" {
						return fmt.Errorf("delete_message_seconds = %s", got)
					}
					return nil
				},
			),
		},
		resource.TestStep{
			// The user banned already fails without failing the action.
			Config: cfg("v2", `user_ids = ["`+first+`", "`+second+`"]`),
			Check:  env.banReason(second, nil),
		},
		resource.TestStep{
			Config:      cfg("v3", `user_ids = ["`+first+`", "`+second+`"]`),
			ExpectError: regexp.MustCompile(`Failed to ban users`),
		},
	)
}

func TestAccActionSetVoiceChannelStatus(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	voice := env.newChannel("tf-acc-voice", discord.ChannelTypeVoice)
	text := env.newChannel("tf-acc-text", discord.ChannelTypeText)
	cfg := func(input, channelID, attrs string) string {
		return env.config(`
provider "discord" {
  audit_log_reason = "terraform"
}
action "discord_set_voice_channel_status" "test" {
  config {
    channel_id = "` + channelID + `"
` + attrs + `
  }
}
` + triggerAction(input, "discord_set_voice_channel_status.test"))
	}
	status := func(want string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			got, ok := env.fake.VoiceStatus(voice)
			if got != want || ok != (want != "") {
				return fmt.Errorf("voice status = %q, want %q", got, want)
			}
			return nil
		}
	}
	path := "PUT /channels/" + voice + "/voice-status"
	env.actionCase(
		resource.TestStep{
			Config:      cfg("v1", voice, `status = "`+strings.Repeat("x", 501)+`"`),
			ExpectError: regexp.MustCompile(`character count must be between 1 and 500`),
		},
		resource.TestStep{
			Config: cfg("v1", voice, `status = "Playing `+strings.Repeat("é", 492)+`"`),
			Check: resource.ComposeAggregateTestCheckFunc(
				status("Playing "+strings.Repeat("é", 492)),
				func(*terraform.State) error { return env.expectReasons(map[string]string{path: "terraform"}) },
			),
		},
		resource.TestStep{
			Config: cfg("v2", voice, `audit_log_reason = "cleared"`),
			Check: resource.ComposeAggregateTestCheckFunc(
				status(""),
				func(*terraform.State) error {
					h := env.fake.RequestHeaders(path)
					if got := h[len(h)-1].Get("X-Audit-Log-Reason"); got != "cleared" {
						return fmt.Errorf("X-Audit-Log-Reason = %q", got)
					}
					if body := env.fake.LastRequestBody(path); string(body["status"]) != "null" {
						return fmt.Errorf("status = %s, want null", body["status"])
					}
					return nil
				},
			),
		},
		resource.TestStep{
			Config:      cfg("v3", text, `status = "Chatting"`),
			ExpectError: regexp.MustCompile(`Cannot\s+execute\s+action\s+on\s+this\s+channel\s+type`),
		},
	)
}

func TestAccActionSyncServerTemplate(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	var code string
	tmpl := `
resource "discord_server_template" "test" {
  server_id = local.server_id
  name      = "Community"
}
`
	sync := func(input string) string {
		return env.config(tmpl + `
action "discord_sync_server_template" "test" {
  config {
    server_id = local.server_id
    code      = discord_server_template.test.code
  }
}
` + triggerAction(input, "discord_sync_server_template.test"))
	}
	isDirty := func(want bool) resource.TestCheckFunc {
		return func(*terraform.State) error {
			templates, err := env.client.ListGuildTemplates(context.Background(), env.serverID)
			if err != nil {
				return err
			}
			if got := templates[0].IsDirty != nil && *templates[0].IsDirty; got != want {
				return fmt.Errorf("is_dirty = %v, want %v", got, want)
			}
			return nil
		}
	}
	env.actionCase(
		resource.TestStep{
			Config: env.config(tmpl),
			Check:  captureAttr("discord_server_template.test", "code", &code),
		},
		resource.TestStep{
			PreConfig: func() { env.fake.MarkTemplateDirty(code) },
			Config:    sync("v1"),
			Check:     isDirty(false),
		},
		resource.TestStep{
			Config: env.config(`
action "discord_sync_server_template" "test" {
  config {
    server_id = local.server_id
    code      = "missing"
  }
}
` + triggerAction("v2", "discord_sync_server_template.test")),
			ExpectError: regexp.MustCompile(`Unknown Guild\s+Template`),
		},
	)
}

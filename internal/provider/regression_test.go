package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// Discord normalizes text channel names; the configured spelling must not
// cause an inconsistent result or a perpetual diff.
func TestAccTextChannelNameNormalization(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "TF Acc Mixed Case"
}
data "discord_channel" "test" {
  server_id = local.server_id
  id        = discord_text_channel.test.id
}`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("discord_text_channel.test", "name", "TF Acc Mixed Case"),
				resource.TestCheckResourceAttr("data.discord_channel.test", "name", "tf-acc-mixed-case"),
			),
		}},
	})
}

// Discord trims message text and unfurls links into extra embeds; neither may
// cause an inconsistent result or a perpetual diff. Edits must always carry
// allowed_mentions so they never ping.
func TestAccMessageNormalization(t *testing.T) {
	env := newTestEnv(t)
	cfg := func(content string) string {
		return env.config(fmt.Sprintf(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-normalize"
}
resource "discord_message" "test" {
  channel_id = discord_text_channel.test.id
  content    = <<-EOT
    %s
  EOT
  embeds = [{
    description = "  padded  "
    fields      = [{ name = "a ", value = " b" }]
  }]
}`, content))
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg("See https://example.com @everyone"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_message.test", "content", "See https://example.com @everyone\n"),
					resource.TestCheckResourceAttr("discord_message.test", "embeds.#", "1"),
					resource.TestCheckResourceAttr("discord_message.test", "embeds.0.description", "  padded  "),
				),
			},
			{
				Config: cfg("Edited https://example.com @everyone"),
				Check: func(*terraform.State) error {
					if env.fake == nil {
						return nil
					}
					edits := env.fake.MessageEdits()
					if len(edits) == 0 {
						return errors.New("no message edits recorded")
					}
					var am struct {
						Parse []string `json:"parse"`
					}
					raw, ok := edits[len(edits)-1]["allowed_mentions"]
					if !ok || json.Unmarshal(raw, &am) != nil || am.Parse == nil || len(am.Parse) != 0 {
						return fmt.Errorf("edit did not suppress mentions: %s", raw)
					}
					return nil
				},
			},
		},
	})
}

// Reordering forum tags without changing their count must keep each tag's ID
// attached to its name rather than renaming tags by position.
func TestAccForumTagReorder(t *testing.T) {
	env := newTestEnv(t)
	questionID := statecheck.CompareValue(compare.ValuesSame())
	cfg := func(tags string) string {
		return env.config(`
resource "discord_forum_channel" "test" {
  server_id      = local.server_id
  name           = "tf-acc-tag-reorder"
  available_tags = ` + tags + `
}`)
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg(`[{ name = "question" }, { name = "staff" }]`),
				ConfigStateChecks: []statecheck.StateCheck{
					questionID.AddStateValue("discord_forum_channel.test", tfjsonpath.New("available_tags").AtSliceIndex(0).AtMapKey("id")),
				},
			},
			{
				Config: cfg(`[{ name = "staff" }, { name = "question" }]`),
				Check:  resource.TestCheckResourceAttr("discord_forum_channel.test", "available_tags.1.name", "question"),
				ConfigStateChecks: []statecheck.StateCheck{
					questionID.AddStateValue("discord_forum_channel.test", tfjsonpath.New("available_tags").AtSliceIndex(1).AtMapKey("id")),
				},
			},
		},
	})
}

// Creating a role shifts the positions of existing roles; updating another
// role in the same apply must not fail with an inconsistent position.
func TestAccRoleCreateAndUpdateTogether(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_role" "a" {
  server_id = local.server_id
  name      = "tf-acc-a"
}`),
			},
			{
				Config: env.config(`
resource "discord_role" "a" {
  server_id = local.server_id
  name      = "tf-acc-a-renamed"
}
resource "discord_role" "b" {
  server_id = local.server_id
  name      = "tf-acc-b"
}`),
			},
		},
	})
}

func TestAccStageChannelBitrateLimit(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`
resource "discord_stage_channel" "test" {
  server_id = local.server_id
  name      = "stage"
  bitrate   = 96000
}`),
			ExpectError: regexp.MustCompile(`value must be between 8000 and 64000`),
		}},
	})
}

// Changing require_tag must keep channel flags set outside Terraform.
func TestAccForumChannelKeepsUnmanagedFlags(t *testing.T) {
	env := newTestEnv(t)
	// Discord may refuse arbitrary flags on a live forum channel.
	env.requireFake()
	const spoiler = 1 << 21 // IS_SPOILER_CHANNEL
	var id string
	cfg := func(requireTag bool) string {
		return env.config(fmt.Sprintf(`
resource "discord_forum_channel" "test" {
  server_id   = local.server_id
  name        = "tf-acc-flags"
  require_tag = %t
}`, requireTag))
	}
	wantFlags := func(want int64) resource.TestCheckFunc {
		return func(*terraform.State) error {
			ch, err := env.client.GetChannel(context.Background(), id)
			if err != nil {
				return err
			}
			if ch.Flags != want {
				return fmt.Errorf("flags = %d, want %d", ch.Flags, want)
			}
			return nil
		}
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg(true),
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("discord_forum_channel.test", "id", &id),
					wantFlags(discord.ChannelFlagRequireTag),
				),
			},
			{
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyChannel(ctx, id, discord.Payload{"flags": discord.ChannelFlagRequireTag | spoiler})
					return err
				}),
				Config: cfg(false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_forum_channel.test", "require_tag", "false"),
					wantFlags(spoiler),
				),
			},
			{
				Config: cfg(true),
				Check:  wantFlags(discord.ChannelFlagRequireTag | spoiler),
			},
		},
	})
}

// Changing hide_media_download_options or require_tag must keep the other
// managed flag and channel flags set outside Terraform.
func TestAccMediaChannelKeepsUnmanagedFlags(t *testing.T) {
	env := newTestEnv(t)
	env.requireMediaChannels()
	// Discord may refuse arbitrary flags on a live media channel.
	env.requireFake()
	const (
		spoiler = 1 << 21 // IS_SPOILER_CHANNEL
		hide    = discord.ChannelFlagHideMediaDownloadOptions
		require = discord.ChannelFlagRequireTag
	)
	var id string
	cfg := func(requireTag, hideDownloads bool) string {
		return env.config(fmt.Sprintf(`
resource "discord_media_channel" "test" {
  server_id                   = local.server_id
  name                        = "tf-acc-media-flags"
  require_tag                 = %t
  hide_media_download_options = %t
}`, requireTag, hideDownloads))
	}
	wantFlags := func(want int64) resource.TestCheckFunc {
		return func(*terraform.State) error {
			ch, err := env.client.GetChannel(context.Background(), id)
			if err != nil {
				return err
			}
			if ch.Flags != want {
				return fmt.Errorf("flags = %d, want %d", ch.Flags, want)
			}
			return nil
		}
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg(true, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("discord_media_channel.test", "id", &id),
					wantFlags(require|hide),
				),
			},
			{
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyChannel(ctx, id, discord.Payload{"flags": require | hide | spoiler})
					return err
				}),
				Config: cfg(true, false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_media_channel.test", "hide_media_download_options", "false"),
					wantFlags(require|spoiler),
				),
			},
			{
				Config: cfg(false, true),
				Check:  wantFlags(hide | spoiler),
			},
		},
	})
}

// Reordering tied roles must not leave a listed role sharing a position with
// an unlisted one, which would move the unlisted role in the display order.
func TestAccRolePositionsKeepUnlistedOrder(t *testing.T) {
	env := newTestEnv(t)
	// Discord does not let a client create tied role positions.
	env.requireFake()
	// depends_on creates the roles in order so the unlisted role has the
	// lowest snowflake and sorts first among tied roles.
	roles := `
resource "discord_role" "unlisted" {
  server_id = local.server_id
  name      = "tf-acc-unlisted"
}
resource "discord_role" "a" {
  server_id  = local.server_id
  name       = "tf-acc-a"
  depends_on = [discord_role.unlisted]
}
resource "discord_role" "b" {
  server_id  = local.server_id
  name       = "tf-acc-b"
  depends_on = [discord_role.a]
}
`
	var unlisted, a, b string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(roles),
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("discord_role.unlisted", "id", &unlisted),
					captureAttr("discord_role.a", "id", &a),
					captureAttr("discord_role.b", "id", &b),
				),
			},
			{
				// From the bottom: a and b tied, then the unlisted role.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.ModifyRolePositions(ctx, env.serverID, []discord.PositionUpdate{
						{ID: a, Position: 1}, {ID: b, Position: 1}, {ID: unlisted, Position: 2},
					})
				}),
				Config: env.config(roles + `
resource "discord_role_positions" "test" {
  server_id = local.server_id
  role_ids  = [discord_role.a.id, discord_role.b.id]
}`),
				Check: func(*terraform.State) error {
					roles, err := env.client.ListRoles(context.Background(), env.serverID)
					if err != nil {
						return err
					}
					var current []discord.Positioned
					for _, r := range roles {
						current = append(current, discord.Positioned{ID: r.ID, Position: r.Position})
					}
					got := discord.OrderOf(current, []string{a, b, unlisted})
					if want := []string{b, a, unlisted}; !slices.Equal(got, want) {
						return fmt.Errorf("role order from the bottom = %v, want %v", got, want)
					}
					return nil
				},
			},
		},
	})
}

package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

const (
	botUserAddress   = "discord_bot_user.test"
	botMemberAddress = "discord_bot_member.test"
)

// checkBotUser runs f on the bot's user as Discord reports it.
func (e *testEnv) checkBotUser(f func(u *discord.User) error) resource.TestCheckFunc {
	return func(*terraform.State) error {
		u, err := e.client.GetCurrentUser(context.Background())
		if err != nil {
			return err
		}
		return f(u)
	}
}

// checkBotMember runs f on the bot's membership in the test server.
func (e *testEnv) checkBotMember(f func(m *discord.Member) error) resource.TestCheckFunc {
	return func(*terraform.State) error {
		u, err := e.client.GetCurrentUser(context.Background())
		if err != nil {
			return err
		}
		m, err := e.client.GetMember(context.Background(), e.serverID, u.ID)
		if err != nil {
			return err
		}
		return f(m)
	}
}

func expectUsername(want string) func(u *discord.User) error {
	return func(u *discord.User) error {
		if u.Username != want {
			return fmt.Errorf("username = %q, want %q", u.Username, want)
		}
		return nil
	}
}

func TestAccBotUser(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	botUser := func(attrs string) string {
		return `
resource "discord_bot_user" "test" {
` + attrs + `
}`
	}
	var botID, drifted string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      botUser(`  username = "b"`),
				ExpectError: regexp.MustCompile(`character count must be between 2 and 32`),
			},
			{
				Config:      botUser(`  avatar = "https://example.com/a.png"`),
				ExpectError: regexp.MustCompile(`base64 image data URI`),
			},
			{
				Config: botUser(`  username = "helper"
  avatar   = "` + onePixelPNG + `"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(botUserAddress, "username", "helper"),
					resource.TestCheckResourceAttrSet(botUserAddress, "avatar_hash"),
					resource.TestCheckNoResourceAttr(botUserAddress, "banner_hash"),
					captureAttr(botUserAddress, "id", &botID),
					env.checkBotUser(expectUsername("helper")),
				),
			},
			{
				// Changed in the Discord client: set again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					u, err := c.ModifyCurrentUser(ctx, discord.Payload{"username": "renamed", "avatar": otherPNG})
					if err == nil {
						drifted = *u.Avatar
					}
					return err
				}),
				Config: botUser(`  username = "helper"
  avatar   = "` + onePixelPNG + `"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(botUserAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					attrDiffers(botUserAddress, "avatar_hash", &drifted),
					env.checkBotUser(expectUsername("helper")),
				),
			},
			{
				ResourceName:            botUserAddress,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"avatar"},
			},
			{
				ResourceName:  botUserAddress,
				ImportState:   true,
				ImportStateId: "123456789012345678",
				ExpectError:   regexp.MustCompile(`token belongs to bot user`),
			},
			{
				// Omitted settings are left unmanaged.
				Config: botUser(``),
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyCurrentUser(ctx, discord.Payload{"username": "unmanaged"})
					return err
				}),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(botUserAddress, "username", "unmanaged"),
					resource.TestCheckResourceAttrPtr(botUserAddress, "id", &botID),
				),
			},
		},
		// Destroying leaves the profile as it is.
		CheckDestroy: env.checkBotUser(expectUsername("unmanaged")),
	})
}

func TestAccBotUserWriteOnlyBanner(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	withBanner := func(image, version string) string {
		return `
resource "discord_bot_user" "test" {
  banner_wo         = "` + image + `"
  banner_wo_version = ` + version + `
}`
	}
	hashes := statecheck.CompareValue(compare.ValuesDiffer())
	var kept string
	env.run(resource.TestCase{
		TerraformVersionChecks: writeOnlySupported,
		Steps: []resource.TestStep{
			{
				Config: withBanner(onePixelPNG, "1"),
				ConfigStateChecks: append(expectNull(botUserAddress, "banner_wo", "banner"),
					hashes.AddStateValue(botUserAddress, tfjsonpath.New("banner_hash")),
					statecheck.ExpectKnownValue(botUserAddress, tfjsonpath.New("banner_hash"), knownvalue.NotNull()),
				),
			},
			{
				// A new value under the same version is not sent.
				Config: withBanner(otherPNG, "1"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: withBanner(otherPNG, "2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(botUserAddress, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{hashes.AddStateValue(botUserAddress, tfjsonpath.New("banner_hash"))},
				Check:             captureAttr(botUserAddress, "banner_hash", &kept),
			},
			{
				// Removing the version leaves the banner in place.
				Config:            `resource "discord_bot_user" "test" {}`,
				Check:             resource.TestCheckResourceAttrPtr(botUserAddress, "banner_hash", &kept),
				ConfigStateChecks: expectNull(botUserAddress, "banner_wo_version"),
			},
		},
	})
}

func TestAccBotMember(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	member := func(attrs string) string {
		return env.config(`
resource "discord_bot_member" "test" {
  server_id = local.server_id
` + attrs + `
}`)
	}
	expectProfile := func(nick, bio string, images bool) resource.TestCheckFunc {
		return env.checkBotMember(func(m *discord.Member) error {
			if got := stringPtrValue(m.Nick).ValueString(); got != nick {
				return fmt.Errorf("nick = %q, want %q", got, nick)
			}
			if got := env.fake.BotBio(env.serverID); got != bio {
				return fmt.Errorf("bio = %q, want %q", got, bio)
			}
			if (m.Avatar != nil) != images || (m.Banner != nil) != images {
				return fmt.Errorf("avatar %v and banner %v, want set = %t", m.Avatar, m.Banner, images)
			}
			return nil
		})
	}
	full := member(`  nick             = "helper"
  bio              = "Ask me"
  avatar           = "` + onePixelPNG + `"
  banner           = "` + onePixelPNG + `"
  audit_log_reason = "bot profile"`)
	var drifted string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      member(`  nick = " helper"`),
				ExpectError: regexp.MustCompile(`must not start or end with whitespace`),
			},
			{
				Config:      member(`  bio = ""`),
				ExpectError: regexp.MustCompile(`character count must be between 1 and 300`),
			},
			{
				Config: full,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(botMemberAddress, "id", env.serverID),
					resource.TestCheckResourceAttrSet(botMemberAddress, "user_id"),
					resource.TestCheckResourceAttrSet(botMemberAddress, "avatar_hash"),
					resource.TestCheckResourceAttrSet(botMemberAddress, "banner_hash"),
					expectProfile("helper", "Ask me", true),
					func(*terraform.State) error {
						return env.expectReasons(map[string]string{"PATCH /guilds/" + env.serverID + "/members/@me": "bot profile"})
					},
				),
			},
			{
				// Changed in the Discord client: set again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					m, err := c.ModifyCurrentMember(ctx, env.serverID, discord.Payload{"nick": "renamed", "avatar": otherPNG})
					if err == nil {
						drifted = *m.Avatar
					}
					return err
				}),
				Config: full,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(botMemberAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					attrDiffers(botMemberAddress, "avatar_hash", &drifted),
					expectProfile("helper", "Ask me", true),
				),
			},
			{
				ResourceName:            botMemberAddress,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"avatar", "banner", "bio", "audit_log_reason"},
			},
			{
				// Removing nick and bio clears them; removing the images
				// leaves them in place.
				Config: member(`  bio = "Ask me later"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(botMemberAddress, "nick"),
					expectProfile("", "Ask me later", true),
				),
			},
			{
				Config: member(``),
				Check:  expectProfile("", "", true),
			},
			{
				Config: member(`  nick   = "helper"
  avatar = "` + onePixelPNG + `"`),
				Check: expectProfile("helper", "", true),
			},
		},
		// Destroying clears what the resource manages: the nickname and the
		// avatar, but not the banner it no longer configures.
		CheckDestroy: env.checkBotMember(func(m *discord.Member) error {
			if m.Nick != nil || m.Avatar != nil || m.Banner == nil {
				return fmt.Errorf("after destroy nick %v, avatar %v, banner %v; want only the banner", m.Nick, m.Avatar, m.Banner)
			}
			return nil
		}),
	})
}

func TestAccBotMemberServerRemoved(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	cfg := env.config(`
resource "discord_bot_member" "test" {
  server_id = local.server_id
  nick      = "helper"
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				// The bot is removed from the server: planned again.
				PreConfig:          func() { env.fake.RemoveGuild(env.serverID) },
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

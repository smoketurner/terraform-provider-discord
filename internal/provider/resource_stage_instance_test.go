package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

const stageAddress = "discord_stage_instance.test"

func TestAccStageInstance(t *testing.T) {
	env := newTestEnv(t)
	stage := func(attrs string) string {
		return env.config(`
resource "discord_stage_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-stage"
}
resource "discord_stage_instance" "test" {
  channel_id = discord_stage_channel.test.id
` + attrs + `
}`)
	}
	created := stage(`  topic                   = "Town hall"
  send_start_notification = true`)
	renamed := stage(`  topic                   = "Town hall, part 2"
  send_start_notification = true`)
	ids := statecheck.CompareValue(compare.ValuesSame())
	var channelID, id string
	var writes int
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: created,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(stageAddress, "topic", "Town hall"),
					resource.TestCheckResourceAttr(stageAddress, "server_id", env.serverID),
					resource.TestCheckResourceAttr(stageAddress, "send_start_notification", "true"),
					resource.TestCheckNoResourceAttr(stageAddress, "scheduled_event_id"),
					resource.TestCheckResourceAttrPair(stageAddress, "channel_id", "discord_stage_channel.test", "id"),
					captureAttr(stageAddress, "channel_id", &channelID),
					captureAttr(stageAddress, "id", &id),
				),
				ConfigStateChecks: []statecheck.StateCheck{ids.AddStateValue(stageAddress, tfjsonpath.New("id"))},
			},
			{
				ResourceName:                         stageAddress,
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "channel_id",
				ImportStateVerifyIgnore:              []string{"send_start_notification"},
				ImportStateIdFunc:                    func(*terraform.State) (string, error) { return channelID, nil },
			},
			{
				Config: renamed,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(stageAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(stageAddress, "topic", "Town hall, part 2"),
					func(*terraform.State) error {
						if env.fake != nil {
							writes = env.writes()
						}
						return nil
					},
				),
				ConfigStateChecks: []statecheck.StateCheck{ids.AddStateValue(stageAddress, tfjsonpath.New("id"))},
			},
			{
				// send_start_notification only matters when the stage opens.
				Config: stage(`  topic = "Town hall, part 2"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(stageAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(stageAddress, "send_start_notification"),
					func(*terraform.State) error {
						// Only the fake records requests.
						if env.fake == nil {
							return nil
						}
						if n := env.writes(); n != writes {
							return fmt.Errorf("changing send_start_notification sent %d requests", n-writes)
						}
						return nil
					},
				),
				ConfigStateChecks: []statecheck.StateCheck{ids.AddStateValue(stageAddress, tfjsonpath.New("id"))},
			},
			{
				// The topic is changed in the Discord client: changed back.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyStageInstance(ctx, channelID, discord.Payload{"topic": "elsewhere"})
					return err
				}),
				Config: renamed,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(stageAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr(stageAddress, "topic", "Town hall, part 2"),
			},
			{
				// Discord closes stages with no speakers: opened again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteStageInstance(ctx, channelID)
				}),
				Config: renamed,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(stageAddress, plancheck.ResourceActionCreate)},
				},
				Check: attrDiffers(stageAddress, "id", &id),
			},
		},
		CheckDestroy: func(*terraform.State) error {
			if _, err := env.client.GetStageInstance(context.Background(), channelID); !discord.IsNotFound(err) {
				return fmt.Errorf("stage on %s is still open: %w", channelID, err)
			}
			return nil
		},
	})
}

func TestAccStageInstanceScheduledEvent(t *testing.T) {
	env := newTestEnv(t)
	cfg := func(eventRef string) string {
		return env.config(fmt.Sprintf(`
resource "discord_stage_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-stage"
}
resource "discord_scheduled_event" "a" {
  server_id            = local.server_id
  name                 = "tf-acc-stage-a"
  entity_type          = "stage_instance"
  channel_id           = discord_stage_channel.test.id
  scheduled_start_time = %[1]q
}
resource "discord_scheduled_event" "b" {
  server_id            = local.server_id
  name                 = "tf-acc-stage-b"
  entity_type          = "stage_instance"
  channel_id           = discord_stage_channel.test.id
  scheduled_start_time = %[1]q
}
resource "discord_stage_instance" "test" {
  channel_id         = discord_stage_channel.test.id
  topic              = "Event stage"
  scheduled_event_id = %[2]s
}`, eventTime(time.Hour), eventRef))
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg("discord_scheduled_event.a.id"),
				Check:  resource.TestCheckResourceAttrPair(stageAddress, "scheduled_event_id", "discord_scheduled_event.a", "id"),
			},
			{
				// Discord only takes the event when the stage opens. Closing
				// the stage completes event a, which then drops out of state
				// and is planned for creation again.
				Config: cfg("discord_scheduled_event.b.id"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(stageAddress, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check:              resource.TestCheckResourceAttrPair(stageAddress, "scheduled_event_id", "discord_scheduled_event.b", "id"),
				ExpectNonEmptyPlan: true,
			},
			{
				// Removing it from the configuration keeps the stage open.
				Config: cfg("null"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(stageAddress, plancheck.ResourceActionNoop),
						plancheck.ExpectResourceAction("discord_scheduled_event.a", plancheck.ResourceActionCreate),
					},
				},
			},
		},
	})
}

func TestAccStageInstanceErrors(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_stage_instance" "test" {
  channel_id = "123456789012345678"
  topic      = "` + strings.Repeat("x", 121) + `"
}`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`character count must be between 1 and 120`),
			},
			{
				Config: env.config(`
resource "discord_voice_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-not-stage"
}
resource "discord_stage_instance" "test" {
  channel_id = discord_voice_channel.test.id
  topic      = "Wrong channel"
}`),
				ExpectError: regexp.MustCompile(`Unable to create stage instance`),
			},
			{
				Config: env.config(`
resource "discord_stage_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-stage"
}
resource "discord_stage_instance" "a" {
  channel_id = discord_stage_channel.test.id
  topic      = "First"
}
resource "discord_stage_instance" "b" {
  channel_id = discord_stage_channel.test.id
  topic      = "Second"
  depends_on = [discord_stage_instance.a]
}`),
				ExpectError: regexp.MustCompile(`already open`),
			},
		},
	})
}

func TestAccStageInstanceAuditLogReason(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	stage := func(topic string) string {
		return env.config(`
resource "discord_stage_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-stage"
}
resource "discord_stage_instance" "test" {
  channel_id       = discord_stage_channel.test.id
  topic            = "` + topic + `"
  audit_log_reason = "Stage reason"
}`)
	}
	var channelID string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{Config: stage("One"), Check: captureAttr(stageAddress, "channel_id", &channelID)},
			{
				Config: stage("Two"),
				Check: func(*terraform.State) error {
					return env.expectReasons(map[string]string{
						"POST /stage-instances":               "Stage reason",
						"PATCH /stage-instances/" + channelID: "Stage reason",
					})
				},
			},
		},
		CheckDestroy: func(*terraform.State) error {
			return env.expectReasons(map[string]string{"DELETE /stage-instances/" + channelID: "Stage reason"})
		},
	})
}

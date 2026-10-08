package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// eventTime returns an RFC 3339 "Z" timestamp d from now, which Discord
// echoes back as "+00:00".
func eventTime(d time.Duration) string {
	return time.Now().Add(d).UTC().Truncate(time.Second).Format(time.RFC3339)
}

const eventAddress = "discord_scheduled_event.test"

func TestAccScheduledEventExternal(t *testing.T) {
	env := newTestEnv(t)
	start, end := eventTime(48*time.Hour), eventTime(50*time.Hour)
	external := func(attrs string) string {
		return env.config(fmt.Sprintf(`
resource "discord_scheduled_event" "test" {
  server_id            = local.server_id
  entity_type          = "external"
  scheduled_start_time = %q
  scheduled_end_time   = %q
%s
}`, start, end, attrs))
	}
	created := external(`
  name        = "tf-acc-event"
  description = "First"
  location    = "https://example.com/a"`)
	updated := external(`
  name     = "tf-acc-event-renamed"
  location = "Town hall"`)
	ids := statecheck.CompareValue(compare.ValuesSame())
	var id string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: created,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(eventAddress, "server_id", env.serverID),
					resource.TestCheckResourceAttr(eventAddress, "name", "tf-acc-event"),
					resource.TestCheckResourceAttr(eventAddress, "description", "First"),
					resource.TestCheckResourceAttr(eventAddress, "location", "https://example.com/a"),
					// The configured form of the timestamps is kept.
					resource.TestCheckResourceAttr(eventAddress, "scheduled_start_time", start),
					resource.TestCheckResourceAttr(eventAddress, "scheduled_end_time", end),
					resource.TestCheckResourceAttr(eventAddress, "status", "scheduled"),
					resource.TestCheckNoResourceAttr(eventAddress, "channel_id"),
					resource.TestCheckNoResourceAttr(eventAddress, "recurrence_rule"),
					resource.TestCheckResourceAttrSet(eventAddress, "creator_id"),
					captureAttr(eventAddress, "id", &id),
				),
				ConfigStateChecks: []statecheck.StateCheck{ids.AddStateValue(eventAddress, tfjsonpath.New("id"))},
			},
			{
				ResourceName:      eventAddress,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return env.serverID + "/" + id, nil },
			},
			{
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(eventAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(eventAddress, "name", "tf-acc-event-renamed"),
					resource.TestCheckNoResourceAttr(eventAddress, "description"),
					resource.TestCheckResourceAttr(eventAddress, "location", "Town hall"),
				),
				ConfigStateChecks: []statecheck.StateCheck{ids.AddStateValue(eventAddress, tfjsonpath.New("id"))},
			},
			{
				// Renamed in the Discord client: renamed back.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyScheduledEvent(ctx, env.serverID, id, discord.Payload{"name": "renamed elsewhere"})
					return err
				}),
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(eventAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr(eventAddress, "name", "tf-acc-event-renamed"),
			},
			{
				// Discord starts external events on its own; status is not
				// drift.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyScheduledEvent(ctx, env.serverID, id, discord.Payload{"status": discord.ScheduledEventStatusActive})
					return err
				}),
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr(eventAddress, "status", "active"),
			},
			{
				// A completed event cannot be changed, so it is replaced by
				// a new one.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyScheduledEvent(ctx, env.serverID, id, discord.Payload{"status": discord.ScheduledEventStatusCompleted})
					return err
				}),
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(eventAddress, plancheck.ResourceActionCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					attrDiffers(eventAddress, "id", &id),
					resource.TestCheckResourceAttr(eventAddress, "status", "scheduled"),
					captureAttr(eventAddress, "id", &id),
				),
			},
			{
				// Deleted in the Discord client: created again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteScheduledEvent(ctx, env.serverID, id)
				}),
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(eventAddress, plancheck.ResourceActionCreate)},
				},
				Check: attrDiffers(eventAddress, "id", &id),
			},
		},
	})
}

func TestAccScheduledEventCanceledOutsideTerraform(t *testing.T) {
	env := newTestEnv(t)
	cfg := env.config(fmt.Sprintf(`
resource "discord_scheduled_event" "test" {
  server_id            = local.server_id
  name                 = "tf-acc-event"
  entity_type          = "external"
  location             = "Online"
  scheduled_start_time = %q
  scheduled_end_time   = %q
}`, eventTime(48*time.Hour), eventTime(49*time.Hour)))
	var id string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{Config: cfg, Check: captureAttr(eventAddress, "id", &id)},
			{
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyScheduledEvent(ctx, env.serverID, id, discord.Payload{"status": discord.ScheduledEventStatusCanceled})
					return err
				}),
				RefreshState:       true,
				ExpectNonEmptyPlan: true,
				Check: func(s *terraform.State) error {
					if _, ok := s.RootModule().Resources[eventAddress]; ok {
						return fmt.Errorf("canceled event %s is still in state", id)
					}
					return nil
				},
			},
			{
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(eventAddress, plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

func TestAccScheduledEventEntityTypes(t *testing.T) {
	env := newTestEnv(t)
	start, end := eventTime(72*time.Hour), eventTime(73*time.Hour)
	event := func(attrs string) string {
		return env.config(fmt.Sprintf(`
resource "discord_stage_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-event-stage"
}
resource "discord_voice_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-event-voice"
}
resource "discord_scheduled_event" "test" {
  server_id            = local.server_id
  name                 = "tf-acc-event"
  scheduled_start_time = %q
%s
}`, start, attrs))
	}
	inPlace := resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(eventAddress, plancheck.ResourceActionUpdate)},
	}
	ids := statecheck.CompareValue(compare.ValuesSame())
	sameID := func() []statecheck.StateCheck {
		return []statecheck.StateCheck{ids.AddStateValue(eventAddress, tfjsonpath.New("id"))}
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: event(`
  entity_type = "stage_instance"
  channel_id  = discord_stage_channel.test.id`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(eventAddress, "entity_type", "stage_instance"),
					resource.TestCheckResourceAttrPair(eventAddress, "channel_id", "discord_stage_channel.test", "id"),
					resource.TestCheckNoResourceAttr(eventAddress, "scheduled_end_time"),
				),
				ConfigStateChecks: sameID(),
			},
			{
				// A stage event cannot be hosted in a voice channel.
				Config: event(`
  entity_type = "stage_instance"
  channel_id  = discord_voice_channel.test.id`),
				ExpectError: regexp.MustCompile(`channel_id must be a channel of the event's entity type`),
			},
			{
				Config: event(`
  entity_type        = "voice"
  channel_id         = discord_voice_channel.test.id
  scheduled_end_time = "` + end + `"`),
				ConfigPlanChecks: inPlace,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(eventAddress, "entity_type", "voice"),
					resource.TestCheckResourceAttrPair(eventAddress, "channel_id", "discord_voice_channel.test", "id"),
					resource.TestCheckResourceAttr(eventAddress, "scheduled_end_time", end),
				),
				ConfigStateChecks: sameID(),
			},
			{
				// Discord requires channel_id = null, the location and the
				// end time in the request that makes an event external,
				// even when the end time is unchanged.
				Config: event(`
  entity_type        = "external"
  location           = "Park"
  scheduled_end_time = "` + end + `"`),
				ConfigPlanChecks: inPlace,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(eventAddress, "entity_type", "external"),
					resource.TestCheckResourceAttr(eventAddress, "location", "Park"),
					resource.TestCheckNoResourceAttr(eventAddress, "channel_id"),
				),
				ConfigStateChecks: sameID(),
			},
			{
				Config: event(`
  entity_type = "stage_instance"
  channel_id  = discord_stage_channel.test.id`),
				ConfigPlanChecks: inPlace,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(eventAddress, "entity_type", "stage_instance"),
					resource.TestCheckNoResourceAttr(eventAddress, "location"),
					resource.TestCheckNoResourceAttr(eventAddress, "scheduled_end_time"),
				),
				ConfigStateChecks: sameID(),
			},
		},
	})
}

func TestAccScheduledEventRecurrence(t *testing.T) {
	env := newTestEnv(t)
	start := eventTime(96 * time.Hour)
	event := func(rule string) string {
		return env.config(fmt.Sprintf(`
resource "discord_voice_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-event-voice"
}
resource "discord_scheduled_event" "test" {
  server_id            = local.server_id
  name                 = "tf-acc-recurring"
  entity_type          = "voice"
  channel_id           = discord_voice_channel.test.id
  scheduled_start_time = %q
%s
}`, start, rule))
	}
	rule := func(attrs string) string { return "  recurrence_rule = {\n" + attrs + "\n  }" }
	ids := statecheck.CompareValue(compare.ValuesSame())
	sameID := func() []statecheck.StateCheck {
		return []statecheck.StateCheck{ids.AddStateValue(eventAddress, tfjsonpath.New("id"))}
	}
	var id string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: event(rule(`    frequency  = "weekly"
    interval   = 2
    by_weekday = ["wednesday"]`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(eventAddress, "recurrence_rule.frequency", "weekly"),
					resource.TestCheckResourceAttr(eventAddress, "recurrence_rule.interval", "2"),
					resource.TestCheckTypeSetElemAttr(eventAddress, "recurrence_rule.by_weekday.*", "wednesday"),
					resource.TestCheckNoResourceAttr(eventAddress, "recurrence_rule.by_month"),
					captureAttr(eventAddress, "id", &id),
					func(*terraform.State) error {
						e, err := env.client.GetScheduledEvent(context.Background(), env.serverID, id)
						if err != nil {
							return err
						}
						// Wednesday is 2 and weekly is 2 in Discord's enums,
						// and the recurrence starts with the event.
						r := e.RecurrenceRule
						if r == nil || r.Frequency != 2 || len(r.ByWeekday) != 1 || r.ByWeekday[0] != 2 {
							return fmt.Errorf("unexpected recurrence rule %+v", r)
						}
						if got, _ := time.Parse(time.RFC3339, r.Start); got.Format(time.RFC3339) != start {
							return fmt.Errorf("recurrence start = %s, want %s", r.Start, start)
						}
						return nil
					},
				),
				ConfigStateChecks: sameID(),
			},
			{
				ResourceName:      eventAddress,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return env.serverID + "/" + id, nil },
			},
			{
				Config: event(rule(`    frequency  = "daily"
    by_weekday = ["friday", "saturday"]`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(eventAddress, "recurrence_rule.interval", "1"),
					resource.TestCheckResourceAttr(eventAddress, "recurrence_rule.by_weekday.#", "2"),
				),
				ConfigStateChecks: sameID(),
			},
			{
				Config: event(rule(`    frequency    = "monthly"
    by_n_weekday = [{ n = 4, day = "wednesday" }]`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(eventAddress, "recurrence_rule.by_n_weekday.0.n", "4"),
					resource.TestCheckResourceAttr(eventAddress, "recurrence_rule.by_n_weekday.0.day", "wednesday"),
					resource.TestCheckNoResourceAttr(eventAddress, "recurrence_rule.by_weekday"),
				),
				ConfigStateChecks: sameID(),
			},
			{
				Config: event(rule(`    frequency    = "yearly"
    by_month     = ["july"]
    by_month_day = [24]`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckTypeSetElemAttr(eventAddress, "recurrence_rule.by_month.*", "july"),
					resource.TestCheckTypeSetElemAttr(eventAddress, "recurrence_rule.by_month_day.*", "24"),
				),
				ConfigStateChecks: sameID(),
			},
			{
				Config:            event(""),
				Check:             resource.TestCheckNoResourceAttr(eventAddress, "recurrence_rule"),
				ConfigStateChecks: sameID(),
			},
		},
	})
}

func TestAccScheduledEventValidation(t *testing.T) {
	env := newTestEnv(t)
	start, end := eventTime(24*time.Hour), eventTime(25*time.Hour)
	event := func(attrs string) string {
		return env.config(`
resource "discord_scheduled_event" "test" {
  server_id = local.server_id
  name      = "tf-acc-event"
` + attrs + `
}`)
	}
	external := func(extra string) string {
		return event(fmt.Sprintf(`  entity_type          = "external"
  location             = "Online"
  scheduled_start_time = %q
  scheduled_end_time   = %q
%s`, start, end, extra))
	}
	voice := func(rule string) string {
		return event(fmt.Sprintf(`  entity_type          = "voice"
  channel_id           = "123456789012345678"
  scheduled_start_time = %q
  recurrence_rule = {
%s
  }`, start, rule))
	}
	steps := []struct {
		config string
		err    string
	}{
		{event(`  entity_type = "external"
  scheduled_start_time = "` + start + `"`), `location is required for an external event`},
		{event(`  entity_type = "external"
  location = "Online"
  scheduled_start_time = "` + start + `"`), `scheduled_end_time is required for an external event`},
		{external(`  channel_id = "123456789012345678"`), `channel_id cannot be set on an external event`},
		{event(`  entity_type = "voice"
  scheduled_start_time = "` + start + `"`), `channel_id is required for a voice event`},
		{event(`  entity_type = "stage_instance"
  channel_id = "123456789012345678"
  location = "Online"
  scheduled_start_time = "` + start + `"`), `location can only be set on an external event`},
		{event(`  entity_type = "online"
  scheduled_start_time = "` + start + `"`), `value must be one of`},
		{event(`  entity_type = "external"
  location = "Online"
  scheduled_start_time = "` + end + `"
  scheduled_end_time = "` + start + `"`), `scheduled_end_time must be after scheduled_start_time`},
		{event(`  entity_type = "external"
  location = "Online"
  scheduled_start_time = "tomorrow"
  scheduled_end_time = "` + end + `"`), `must be an RFC 3339 timestamp`},
		{external(`  description = ""`), `character count must be between 1 and 1000`},
		{external(`  image = "data:image/gif;base64,R0lGODlhAQABAAAAACw="`), `must be a PNG, JPEG or WebP image`},
		{voice(`    frequency = "daily"
    interval  = 2`), `interval can only be 2 when frequency is weekly`},
		{voice(`    frequency = "weekly"
    interval  = 3`), `value must be between 1 and 2`},
		{voice(`    frequency  = "weekly"
    by_weekday = ["monday", "tuesday"]`), `exactly one day of the week`},
		{voice(`    frequency  = "daily"
    by_weekday = ["monday", "wednesday"]`), `A daily event can only occur on`},
		{voice(`    frequency  = "monthly"
    by_weekday = ["monday"]`), `by_weekday can only be set when frequency is daily or weekly`},
		{voice(`    frequency    = "weekly"
    by_n_weekday = [{ n = 1, day = "monday" }]`), `by_n_weekday can only be set when frequency is monthly`},
		{voice(`    frequency    = "monthly"
    by_n_weekday = [{ n = 6, day = "monday" }]`), `value must be between 1 and 5`},
		{voice(`    frequency = "yearly"
    by_month  = ["july"]`), `by_month and by_month_day must be set together`},
		{voice(`    frequency    = "monthly"
    by_month     = ["july"]
    by_month_day = [24]`), `by_month and by_month_day can only be set when frequency is yearly`},
		{voice(`    frequency    = "yearly"
    by_month     = ["july", "august"]
    by_month_day = [24]`), `(?s)set must contain at least 1 elements and\s+at most 1 elements`},
	}
	var tc resource.TestCase
	for _, s := range steps {
		tc.Steps = append(tc.Steps, resource.TestStep{Config: s.config, PlanOnly: true, ExpectError: regexp.MustCompile(s.err)})
	}
	env.run(tc)
}

func TestAccScheduledEventImage(t *testing.T) {
	env := newTestEnv(t)
	event := func(attrs string) string {
		return env.config(fmt.Sprintf(`
resource "discord_scheduled_event" "test" {
  server_id            = local.server_id
  name                 = "tf-acc-event-cover"
  entity_type          = "external"
  location             = "Online"
  scheduled_start_time = %q
  scheduled_end_time   = %q
%s
}`, eventTime(24*time.Hour), eventTime(25*time.Hour), attrs))
	}
	withImage := func(image, version string) string {
		return event(`  image_wo         = "` + image + `"
  image_wo_version = ` + version)
	}
	hashes := statecheck.CompareValue(compare.ValuesDiffer())
	var id, drifted string
	env.run(resource.TestCase{
		TerraformVersionChecks: writeOnlySupported,
		Steps: []resource.TestStep{
			{
				Config: event(`  image = "` + onePixelPNG + `"`),
				Check:  captureAttr(eventAddress, "id", &id),
				ConfigStateChecks: []statecheck.StateCheck{
					hashes.AddStateValue(eventAddress, tfjsonpath.New("image_hash")),
					statecheck.ExpectKnownValue(eventAddress, tfjsonpath.New("image"), knownvalue.StringExact(onePixelPNG)),
				},
			},
			{
				// Switching to image_wo uploads it.
				Config: withImage(otherPNG, "1"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(eventAddress, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: append(expectNull(eventAddress, "image", "image_wo"),
					hashes.AddStateValue(eventAddress, tfjsonpath.New("image_hash"))),
			},
			{
				Config: withImage(onePixelPNG, "1"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// The cover is changed in the Discord client: uploaded again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					e, err := c.ModifyScheduledEvent(ctx, env.serverID, id, discord.Payload{"image": otherPNG})
					if err == nil {
						drifted = *e.Image
					}
					return err
				}),
				Config: withImage(onePixelPNG, "1"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(eventAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(eventAddress, "image_wo_version", "1"),
					attrDiffers(eventAddress, "image_hash", &drifted),
				),
			},
			{
				// Removing the image removes the cover.
				Config:            event(""),
				ConfigStateChecks: expectNull(eventAddress, "image_hash", "image_wo_version"),
			},
		},
	})
}

func TestAccScheduledEventAuditLogReason(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	g := "/guilds/" + env.serverID
	event := func(name string) string {
		return env.config(fmt.Sprintf(`
provider "discord" {
  audit_log_reason = "Provider reason"
}
resource "discord_scheduled_event" "test" {
  server_id            = local.server_id
  name                 = %q
  entity_type          = "external"
  location             = "Online"
  scheduled_start_time = %q
  scheduled_end_time   = %q
  audit_log_reason     = "Event reason"
}`, name, eventTime(24*time.Hour), eventTime(25*time.Hour)))
	}
	var id string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{Config: event("tf-acc-event"), Check: captureAttr(eventAddress, "id", &id)},
			{
				Config: event("tf-acc-event-renamed"),
				Check: func(*terraform.State) error {
					return env.expectReasons(map[string]string{
						"POST " + g + "/scheduled-events":        "Event reason",
						"PATCH " + g + "/scheduled-events/" + id: "Event reason",
					})
				},
			},
		},
		// Discord does not document the header for deleting an event.
		CheckDestroy: func(*terraform.State) error {
			return env.expectReasons(map[string]string{"DELETE " + g + "/scheduled-events/" + id: ""})
		},
	})
}

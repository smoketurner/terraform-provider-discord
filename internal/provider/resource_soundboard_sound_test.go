package provider

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// Sounds as data URIs, holding only a file signature: Discord's own checks of
// the audio are not modeled by the fake.
const (
	soundMP3 = "data:audio/mpeg;base64,SUQzBAAAAAAAAA=="
	soundOgg = "data:audio/ogg;base64,T2dnUwACAAAAAAAAAAA="
)

// checkSoundData verifies the sound discord_soundboard_sound.test was created
// with, which only the fake records.
func (e *testEnv) checkSoundData(want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		if e.fake == nil {
			return nil
		}
		id := s.RootModule().Resources["discord_soundboard_sound.test"].Primary.ID
		if got, _ := e.fake.SoundboardSoundData(id); got != want {
			return fmt.Errorf("soundboard sound %s was created with %q, want %q", id, got, want)
		}
		return nil
	}
}

func TestAccSoundboardSound(t *testing.T) {
	env := newTestEnv(t)
	const address = "discord_soundboard_sound.test"
	sound := func(attrs string) string {
		return env.config(`
resource "discord_emoji" "test" {
  server_id = local.server_id
  name      = "tf_acc_sound"
  image     = "` + onePixelPNG + `"
}
resource "discord_soundboard_sound" "test" {
  server_id = local.server_id
` + attrs + `
}`)
	}
	created := sound(`  name             = "tf-acc-sound"
  sound            = "` + soundMP3 + `"
  volume           = 0.5
  emoji_name       = "🦆"
  audit_log_reason = "Sound for tests"`)
	updated := sound(`  name     = "tf-acc-renamed"
  sound    = "` + soundMP3 + `"
  emoji_id = discord_emoji.test.id`)
	ids := statecheck.CompareValue(compare.ValuesDiffer())
	var id string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      sound(`  name = "x"` + "\n" + `  sound = "` + soundMP3 + `"`),
				ExpectError: regexp.MustCompile(`character count must be between 2 and 32`),
			},
			{
				Config:      sound(`  name = "tf-acc-sound"` + "\n" + `  sound = "data:audio/wav;base64,UklGRg=="`),
				ExpectError: regexp.MustCompile(`must be a base64 MP3 or Ogg data URI`),
			},
			{
				Config:      sound(`  name = "tf-acc-sound"` + "\n" + `  sound = "` + soundMP3 + `"` + "\n" + `  volume = 1.5`),
				ExpectError: regexp.MustCompile(`value must be between 0\.000000 and 1\.000000`),
			},
			{
				Config: sound(`  name = "tf-acc-sound"
  sound      = "` + soundMP3 + `"
  emoji_id   = discord_emoji.test.id
  emoji_name = "🦆"`),
				ExpectError: regexp.MustCompile(`(?s)Attribute "emoji_name" cannot be specified when "emoji_id" is\s+specified`),
			},
			{
				Config: created,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "name", "tf-acc-sound"),
					resource.TestCheckResourceAttr(address, "volume", "0.5"),
					resource.TestCheckResourceAttr(address, "emoji_name", "🦆"),
					resource.TestCheckNoResourceAttr(address, "emoji_id"),
					resource.TestCheckResourceAttr(address, "available", "true"),
					captureAttr(address, "id", &id),
					env.checkSoundData(soundMP3),
					func(*terraform.State) error {
						if env.fake == nil {
							return nil
						}
						return env.expectReasons(map[string]string{"POST /guilds/" + env.serverID + "/soundboard-sounds": "Sound for tests"})
					},
				),
				ConfigStateChecks: []statecheck.StateCheck{ids.AddStateValue(address, tfjsonpath.New("id"))},
			},
			serverImportStep(env, address, "sound", "audit_log_reason"),
			{
				// Removing the volume restores the default, and the emoji
				// switches to a custom one.
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "name", "tf-acc-renamed"),
					resource.TestCheckResourceAttr(address, "volume", "1"),
					resource.TestCheckResourceAttrPair(address, "emoji_id", "discord_emoji.test", "id"),
					resource.TestCheckNoResourceAttr(address, "emoji_name"),
				),
			},
			{
				// The sound is changed in the Discord client: changed back.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifySoundboardSound(ctx, env.serverID, id, discord.Payload{
						"name": "renamed-outside", "volume": 0.25, "emoji_id": nil, "emoji_name": "🎉",
					})
					return err
				}),
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "name", "tf-acc-renamed"),
					resource.TestCheckResourceAttr(address, "volume", "1"),
					resource.TestCheckResourceAttrPair(address, "emoji_id", "discord_emoji.test", "id"),
					resource.TestCheckNoResourceAttr(address, "emoji_name"),
				),
			},
			{
				// The sound is deleted in the Discord client: created again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteSoundboardSound(ctx, env.serverID, id)
				}),
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionCreate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{ids.AddStateValue(address, tfjsonpath.New("id"))},
			},
			{
				// The sound cannot change in place: a new sound uploads a new one.
				Config: sound(`  name     = "tf-acc-renamed"
  sound    = "` + soundOgg + `"
  emoji_id = discord_emoji.test.id`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionReplace)},
				},
				Check:             env.checkSoundData(soundOgg),
				ConfigStateChecks: []statecheck.StateCheck{ids.AddStateValue(address, tfjsonpath.New("id"))},
			},
		},
	})
}

func TestAccSoundboardSoundWriteOnlySound(t *testing.T) {
	env := newTestEnv(t)
	const address = "discord_soundboard_sound.test"
	sound := func(attrs string) string {
		return env.config(`
resource "discord_soundboard_sound" "test" {
  server_id = local.server_id
  name      = "tf-acc-sound-wo"
` + attrs + `
}`)
	}
	withSound := func(data, version string) string {
		return sound(`  sound_wo         = "` + data + `"
  sound_wo_version = ` + version)
	}
	ids := statecheck.CompareValue(compare.ValuesDiffer())
	env.run(resource.TestCase{
		TerraformVersionChecks: writeOnlySupported,
		Steps: []resource.TestStep{
			{
				Config:      sound(""),
				ExpectError: regexp.MustCompile(`No attribute specified when one \(and only one\) of`),
			},
			{
				Config:      withSound("data:audio/wav;base64,UklGRg==", "1"),
				ExpectError: regexp.MustCompile(`must be a base64 MP3 or Ogg data URI`),
			},
			{
				Config: withSound(soundMP3, "1"),
				Check:  env.checkSoundData(soundMP3),
				ConfigStateChecks: append(expectNull(address, "sound_wo", "sound"),
					ids.AddStateValue(address, tfjsonpath.New("id"))),
			},
			serverImportStep(env, address, "sound_wo_version"),
			{
				// A new value under the same version is not sent.
				Config: withSound(soundOgg, "1"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: withSound(soundOgg, "2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionReplace)},
				},
				Check: env.checkSoundData(soundOgg),
				ConfigStateChecks: append(expectNull(address, "sound_wo"),
					ids.AddStateValue(address, tfjsonpath.New("id"))),
			},
			{
				// Switching to the stored argument keeps the sound.
				Config: sound(`  sound = "` + soundOgg + `"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
			},
		},
	})
}

func TestSoundValidator(t *testing.T) {
	sized := func(n int) string {
		return "data:audio/ogg;base64," + base64.StdEncoding.EncodeToString(make([]byte, n))
	}
	tests := []struct {
		name, value, wantErr string
	}{
		{"mpeg", soundMP3, ""},
		{"mp3", "data:audio/mp3;base64,SUQz", ""},
		{"ogg", soundOgg, ""},
		{"largest", sized(maxSoundSize), ""},
		{"too large", sized(maxSoundSize + 1), "at most 512 KB"},
		{"wav", "data:audio/wav;base64,UklGRg==", "base64 MP3 or Ogg data URI"},
		{"raw base64", "SUQz", "base64 MP3 or Ogg data URI"},
		{"bad padding", "data:audio/mpeg;base64,SUQ=z", "Decoding the base64 data"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &validator.StringResponse{}
			soundValidator{}.ValidateString(context.Background(), validator.StringRequest{
				Path: path.Root("sound"), ConfigValue: types.StringValue(tt.value),
			}, resp)
			checkValidation(t, resp, tt.wantErr)
		})
	}
	for _, v := range []types.String{types.StringNull(), types.StringUnknown()} {
		resp := &validator.StringResponse{}
		soundValidator{}.ValidateString(context.Background(), validator.StringRequest{ConfigValue: v}, resp)
		checkValidation(t, resp, "")
	}
}

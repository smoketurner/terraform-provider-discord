package provider

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
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

// Sticker files as base64: a 1x1 PNG, a 1x1 GIF and a minimal Lottie
// animation.
var (
	stickerPNG    = strings.TrimPrefix(onePixelPNG, "data:image/png;base64,")
	stickerGIF    = "R0lGODlhAQABAIAAAP///wAAACH5BAEAAAAALAAAAAABAAEAAAICRAEAOw=="
	stickerLottie = base64.StdEncoding.EncodeToString([]byte(`{"v":"5.7.4","fr":30,"ip":0,"op":60,"w":320,"h":320,"layers":[]}`))
)

// serverImportStep imports a resource whose import ID is
// "<server_id>/<id>".
func serverImportStep(env *testEnv, address string, ignore ...string) resource.TestStep {
	return resource.TestStep{
		ResourceName:            address,
		ImportState:             true,
		ImportStateVerify:       true,
		ImportStateVerifyIgnore: ignore,
		ImportStateIdFunc: func(s *terraform.State) (string, error) {
			return env.serverID + "/" + s.RootModule().Resources[address].Primary.ID, nil
		},
	}
}

// checkStickerUpload verifies the content type and file name of the file
// uploaded for discord_sticker.test, which only the fake records.
func (e *testEnv) checkStickerUpload(contentType, filename string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		if e.fake == nil {
			return nil
		}
		id := s.RootModule().Resources["discord_sticker.test"].Primary.ID
		u, ok := e.fake.StickerUpload(id)
		if !ok {
			return fmt.Errorf("no file was uploaded for sticker %s", id)
		}
		if u.ContentType != contentType || u.Filename != filename {
			return fmt.Errorf("sticker %s was uploaded as %s (%s), want %s (%s)", id, u.Filename, u.ContentType, filename, contentType)
		}
		return nil
	}
}

func TestAccSticker(t *testing.T) {
	env := newTestEnv(t)
	const address = "discord_sticker.test"
	sticker := func(attrs string) string {
		return env.config(`
resource "discord_sticker" "test" {
  server_id = local.server_id
` + attrs + `
}`)
	}
	created := sticker(`  name             = "tf-acc-sticker"
  description      = "Created by Terraform"
  tags             = "wave"
  file             = "` + stickerPNG + `"
  audit_log_reason = "Sticker for tests"`)
	renamed := sticker(`  name = "tf-acc-renamed"
  tags = "smile"
  file = "` + stickerPNG + `"`)
	ids := statecheck.CompareValue(compare.ValuesDiffer())
	var id string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      sticker(`  name = "x"` + "\n" + `  tags = "wave"` + "\n" + `  file = "` + stickerPNG + `"`),
				ExpectError: regexp.MustCompile(`character count must be between 2 and 30`),
			},
			{
				Config:      sticker(`  name = "tf-acc-sticker"` + "\n" + `  description = "x"` + "\n" + `  tags = "wave"` + "\n" + `  file = "` + stickerPNG + `"`),
				ExpectError: regexp.MustCompile(`character count must be between 2 and 100`),
			},
			{
				Config:      sticker(`  name = "tf-acc-sticker"` + "\n" + `  tags = "` + strings.Repeat("a", 201) + `"` + "\n" + `  file = "` + stickerPNG + `"`),
				ExpectError: regexp.MustCompile(`character count must be between 1 and 200`),
			},
			{
				Config:      sticker(`  name = "tf-acc-sticker"` + "\n" + `  tags = "wave"` + "\n" + `  file = "aGVsbG8="`),
				ExpectError: regexp.MustCompile(`must be a PNG, APNG, GIF or Lottie JSON file`),
			},
			{
				Config:      sticker(`  name = "tf-acc-sticker"` + "\n" + `  tags = "wave"` + "\n" + `  file = "not base64!"`),
				ExpectError: regexp.MustCompile(`must be base64-encoded file content`),
			},
			{
				Config: created,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "name", "tf-acc-sticker"),
					resource.TestCheckResourceAttr(address, "description", "Created by Terraform"),
					resource.TestCheckResourceAttr(address, "tags", "wave"),
					resource.TestCheckResourceAttr(address, "format_type", "png"),
					resource.TestCheckResourceAttr(address, "available", "true"),
					captureAttr(address, "id", &id),
					env.checkStickerUpload("image/png", "sticker.png"),
					func(*terraform.State) error {
						if env.fake == nil {
							return nil
						}
						return env.expectReasons(map[string]string{"POST /guilds/" + env.serverID + "/stickers": "Sticker for tests"})
					},
				),
				ConfigStateChecks: []statecheck.StateCheck{ids.AddStateValue(address, tfjsonpath.New("id"))},
			},
			serverImportStep(env, address, "file", "audit_log_reason"),
			{
				Config: renamed,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "name", "tf-acc-renamed"),
					resource.TestCheckResourceAttr(address, "tags", "smile"),
					resource.TestCheckNoResourceAttr(address, "description"),
				),
			},
			{
				// The sticker is renamed in the Discord client: renamed back.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifySticker(ctx, env.serverID, id, discord.Payload{"name": "tf-acc-renamed-outside", "description": "Outside"})
					return err
				}),
				Config: renamed,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "name", "tf-acc-renamed"),
					resource.TestCheckNoResourceAttr(address, "description"),
				),
			},
			{
				// The sticker is deleted in the Discord client: created again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteSticker(ctx, env.serverID, id)
				}),
				Config: renamed,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionCreate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{ids.AddStateValue(address, tfjsonpath.New("id"))},
			},
			{
				// The file of a sticker cannot change: a new file uploads a new sticker.
				Config: sticker(`  name = "tf-acc-renamed"
  tags = "smile"
  file = "` + stickerGIF + `"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionReplace)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "format_type", "gif"),
					env.checkStickerUpload("image/gif", "sticker.gif"),
				),
				ConfigStateChecks: []statecheck.StateCheck{ids.AddStateValue(address, tfjsonpath.New("id"))},
			},
		},
	})
}

// Lottie stickers need the VERIFIED or PARTNERED server feature.
func TestAccStickerLottie(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	const address = "discord_sticker.test"
	cfg := env.config(`
resource "discord_sticker" "test" {
  server_id = local.server_id
  name      = "tf-acc-lottie"
  tags      = "wave"
  file      = "` + stickerLottie + `"
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      cfg,
				ExpectError: regexp.MustCompile(`Lottie stickers need the VERIFIED or PARTNERED feature`),
			},
			{
				PreConfig: func() { env.fake.SetGuildFeatures("VERIFIED") },
				Config:    cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "format_type", "lottie"),
					env.checkStickerUpload("application/json", "sticker.json"),
				),
			},
		},
	})
}

func TestAccStickerWriteOnlyFile(t *testing.T) {
	env := newTestEnv(t)
	const address = "discord_sticker.test"
	sticker := func(attrs string) string {
		return env.config(`
resource "discord_sticker" "test" {
  server_id = local.server_id
  name      = "tf-acc-sticker-wo"
  tags      = "wave"
` + attrs + `
}`)
	}
	withFile := func(file, version string) string {
		return sticker(`  file_wo         = "` + file + `"
  file_wo_version = ` + version)
	}
	ids := statecheck.CompareValue(compare.ValuesDiffer())
	env.run(resource.TestCase{
		TerraformVersionChecks: writeOnlySupported,
		Steps: []resource.TestStep{
			{
				Config:      sticker(""),
				ExpectError: regexp.MustCompile(`No attribute specified when one \(and only one\) of`),
			},
			{
				Config: sticker(`  file            = "` + stickerPNG + `"
  file_wo         = "` + stickerPNG + `"
  file_wo_version = 1`),
				ExpectError: regexp.MustCompile(`2 attributes specified when one \(and only one\) of`),
			},
			{
				Config:      withFile("aGVsbG8=", "1"),
				ExpectError: regexp.MustCompile(`must be a PNG, APNG, GIF or Lottie JSON file`),
			},
			{
				Config: withFile(stickerPNG, "1"),
				Check:  env.checkStickerUpload("image/png", "sticker.png"),
				ConfigStateChecks: append(expectNull(address, "file_wo", "file"),
					ids.AddStateValue(address, tfjsonpath.New("id"))),
			},
			serverImportStep(env, address, "file_wo_version"),
			{
				// A new value under the same version is not sent.
				Config: withFile(stickerGIF, "1"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: withFile(stickerGIF, "2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionReplace)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "format_type", "gif"),
					env.checkStickerUpload("image/gif", "sticker.gif"),
				),
				ConfigStateChecks: append(expectNull(address, "file_wo"),
					ids.AddStateValue(address, tfjsonpath.New("id"))),
			},
			{
				// Switching to the stored argument keeps the sticker.
				Config: sticker(`  file = "` + stickerGIF + `"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
			},
		},
	})
}

func TestStickerFileValidator(t *testing.T) {
	encode := func(data []byte) string { return base64.StdEncoding.EncodeToString(data) }
	apng := append([]byte(nil), pngSignature...)
	apng = append(apng, "\x00\x00\x00\x08acTL"...)
	tests := []struct {
		name, value, wantErr string
	}{
		{"png", stickerPNG, ""},
		{"apng", encode(apng), ""},
		{"gif87a", encode([]byte("GIF87a\x01\x00\x01\x00")), ""},
		{"gif89a", stickerGIF, ""},
		{"lottie", stickerLottie, ""},
		{"lottie with leading whitespace", encode([]byte("\n {\"v\":\"5.7.4\"}")), ""},
		{"largest", encode(append(append([]byte(nil), pngSignature...), make([]byte, maxStickerFileSize-len(pngSignature))...)), ""},
		{"too large", encode(append(append([]byte(nil), pngSignature...), make([]byte, maxStickerFileSize-len(pngSignature)+1)...)), "at most 512 KiB"},
		{"empty", "", "base64-encoded file content"},
		{"not base64", "%%%", "base64-encoded file content"},
		{"jpeg", encode([]byte("\xff\xd8\xff\xe0")), "PNG, APNG, GIF or Lottie JSON"},
		{"json array", encode([]byte("[]")), "PNG, APNG, GIF or Lottie JSON"},
		{"invalid json", encode([]byte("{")), "PNG, APNG, GIF or Lottie JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &validator.StringResponse{}
			stickerFileValidator{}.ValidateString(context.Background(), validator.StringRequest{
				Path: path.Root("file"), ConfigValue: types.StringValue(tt.value),
			}, resp)
			checkValidation(t, resp, tt.wantErr)
		})
	}
	for _, v := range []types.String{types.StringNull(), types.StringUnknown()} {
		resp := &validator.StringResponse{}
		stickerFileValidator{}.ValidateString(context.Background(), validator.StringRequest{ConfigValue: v}, resp)
		checkValidation(t, resp, "")
	}
}

// checkValidation fails unless a validator reported exactly an error
// containing wantErr, or nothing when wantErr is empty.
func checkValidation(t *testing.T, resp *validator.StringResponse, wantErr string) {
	t.Helper()
	switch {
	case wantErr == "" && resp.Diagnostics.HasError():
		t.Errorf("unexpected error: %v", resp.Diagnostics)
	case wantErr != "" && !resp.Diagnostics.HasError():
		t.Errorf("expected an error containing %q", wantErr)
	case wantErr != "" && !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), wantErr):
		t.Errorf("error %q does not contain %q", resp.Diagnostics.Errors()[0].Detail(), wantErr)
	}
}

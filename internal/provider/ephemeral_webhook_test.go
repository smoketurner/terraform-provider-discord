package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/echoprovider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var withEchoProvider = map[string]func() (tfprotov6.ProviderServer, error){
	"discord": protoV6ProviderFactories["discord"],
	"echo":    echoprovider.NewProviderServer(),
}

func (e *testEnv) webhookToken(id string) (string, error) {
	w, err := e.client.GetWebhook(context.Background(), id)
	if err != nil {
		return "", err
	}
	if w.Token == "" {
		return "", fmt.Errorf("webhook %s has no token", id)
	}
	return w.Token, nil
}

// noWebhookToken checks that no attribute of any resource in state holds
// the token of discord_webhook.test.
func (e *testEnv) noWebhookToken() resource.TestCheckFunc {
	const address = "discord_webhook.test"
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[address]
		if !ok {
			return fmt.Errorf("resource %s not found in state", address)
		}
		token, err := e.webhookToken(rs.Primary.ID)
		if err != nil {
			return err
		}
		for name, r := range s.RootModule().Resources {
			for k, v := range r.Primary.Attributes {
				if strings.Contains(v, token) {
					return fmt.Errorf("%s.%s holds the webhook token", name, k)
				}
			}
		}
		return nil
	}
}

// planWithoutToken checks that the plan, including the prior state it
// carries, never contains the token of the webhook *id.
type planWithoutToken struct {
	env *testEnv
	id  *string
}

func (c planWithoutToken) CheckPlan(_ context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	token, err := c.env.webhookToken(*c.id)
	if err != nil {
		resp.Error = err
		return
	}
	b, err := json.Marshal(req.Plan)
	if err != nil {
		resp.Error = err
		return
	}
	if strings.Contains(string(b), token) {
		resp.Error = errors.New("the plan contains the webhook token")
	}
}

func TestAccEphemeralWebhook(t *testing.T) {
	env := newTestEnv(t)
	var id string
	env.run(resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_10_0)},
		ProtoV6ProviderFactories: withEchoProvider,
		Steps: []resource.TestStep{
			{
				Config: env.config(`
ephemeral "discord_webhook" "test" {
  id = "not-a-snowflake"
}
provider "echo" {
  data = ephemeral.discord_webhook.test
}
resource "echo" "test" {}`),
				ExpectError: regexp.MustCompile(`must be a Discord snowflake ID`),
			},
			{
				Config: env.config(`
ephemeral "discord_webhook" "test" {
  id = "123456789012345678"
}
provider "echo" {
  data = ephemeral.discord_webhook.test
}
resource "echo" "test" {}`),
				ExpectError: regexp.MustCompile(`Unknown\s+Webhook`),
			},
			{
				// The ephemeral values reach state only through the echo
				// test provider; the webhook keeps them out.
				Config: env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-ephemeral-hook"
}
resource "discord_webhook" "test" {
  channel_id    = discord_text_channel.test.id
  name          = "tf-acc-ephemeral"
  store_secrets = false
}
ephemeral "discord_webhook" "test" {
  id = discord_webhook.test.id
}
provider "echo" {
  data = ephemeral.discord_webhook.test
}
resource "echo" "test" {}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("discord_webhook.test", "id", &id),
					resource.TestCheckNoResourceAttr("discord_webhook.test", "token"),
					resource.TestCheckNoResourceAttr("discord_webhook.test", "url"),
					resource.TestCheckResourceAttrPair("echo.test", "data.id", "discord_webhook.test", "id"),
					resource.TestCheckResourceAttrWith("echo.test", "data.token", func(v string) error {
						token, err := env.webhookToken(id)
						if err == nil && v != token {
							err = fmt.Errorf("token = %q, want %q", v, token)
						}
						return err
					}),
					resource.TestCheckResourceAttrWith("echo.test", "data.url", func(v string) error {
						token, err := env.webhookToken(id)
						if want := webhookURLPrefix + id + "/" + token; err == nil && v != want {
							err = fmt.Errorf("url = %q, want %q", v, want)
						}
						return err
					}),
				),
			},
		},
	})
}

func TestAccEphemeralWebhookWithoutToken(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	ch, err := env.client.CreateChannel(context.Background(), env.serverID, discord.Payload{"name": "tf-acc-follower", "type": 0})
	if err != nil {
		t.Fatal(err)
	}
	id := env.fake.AddChannelFollowerWebhook(ch.ID)
	env.run(resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_10_0)},
		ProtoV6ProviderFactories: withEchoProvider,
		Steps: []resource.TestStep{{
			Config: `
ephemeral "discord_webhook" "test" {
  id = "` + id + `"
}
provider "echo" {
  data = ephemeral.discord_webhook.test
}
resource "echo" "test" {}`,
			ExpectError: regexp.MustCompile(`Webhook has no token`),
		}},
	})
}

func TestAccWebhookStoreSecrets(t *testing.T) {
	env := newTestEnv(t)
	webhook := func(store string) string {
		return env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-store-secrets"
}
resource "discord_webhook" "test" {
  channel_id = discord_text_channel.test.id
  name       = "tf-acc-store-secrets"
` + store + `
}`)
	}
	withoutSecrets := webhook("  store_secrets = false")
	var id string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: withoutSecrets,
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("discord_webhook.test", "id", &id),
					resource.TestCheckResourceAttr("discord_webhook.test", "store_secrets", "false"),
					resource.TestCheckNoResourceAttr("discord_webhook.test", "token"),
					resource.TestCheckNoResourceAttr("discord_webhook.test", "url"),
					env.noWebhookToken(),
				),
			},
			{
				// Refreshing and planning again never reads the token into
				// the plan.
				Config: withoutSecrets,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
						planWithoutToken{env, &id},
					},
				},
			},
			{
				ResourceName:      "discord_webhook.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					token, err := env.webhookToken(id)
					if err != nil {
						return err
					}
					for _, s := range states {
						for k, v := range s.Attributes {
							if strings.Contains(v, token) {
								return fmt.Errorf("imported %s holds the webhook token", k)
							}
						}
					}
					return nil
				},
			},
			{
				// Storing the secrets is an in-place update that reads them.
				Config: webhook(""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_webhook.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_webhook.test", "store_secrets", "true"),
					resource.TestCheckResourceAttrWith("discord_webhook.test", "token", func(v string) error {
						token, err := env.webhookToken(id)
						if err == nil && v != token {
							err = fmt.Errorf("token = %q, want %q", v, token)
						}
						return err
					}),
					resource.TestMatchResourceAttr("discord_webhook.test", "url", regexp.MustCompile(`^https://discord\.com/api/webhooks/\d+/.+$`)),
				),
			},
			{
				// Renaming keeps the stored secrets.
				Config: env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-store-secrets"
}
resource "discord_webhook" "test" {
  channel_id = discord_text_channel.test.id
  name       = "tf-acc-store-secrets-renamed"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectKnownValue("discord_webhook.test", tfjsonpath.New("token"), knownvalue.NotNull())},
				},
				Check: resource.TestCheckResourceAttrSet("discord_webhook.test", "token"),
			},
			{
				// Dropping them removes them from state.
				Config: withoutSecrets,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_webhook.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("discord_webhook.test", "token"),
					resource.TestCheckNoResourceAttr("discord_webhook.test", "url"),
					env.noWebhookToken(),
				),
			},
			{
				// The webhook is deleted in the Discord client: created
				// again, still without its secrets.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteWebhook(ctx, id)
				}),
				Config: withoutSecrets,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_webhook.test", plancheck.ResourceActionCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("discord_webhook.test", "token"),
					env.noWebhookToken(),
				),
			},
		},
	})
}

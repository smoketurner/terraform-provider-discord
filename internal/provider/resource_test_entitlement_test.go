package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
	"github.com/smoketurner/terraform-provider-discord/internal/discord/discordtest"
)

const testEntitlementAddress = "discord_test_entitlement.test"

// The test entitlement tests need an SKU, which only the fake has.
func TestAccTestEntitlement(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	sku := env.fake.AddSKU(discord.SKU{Type: 5, Name: "Premium", Slug: "premium"})
	entitlement := func(skuID, ownerType, ownerID string) string {
		return env.config(fmt.Sprintf(`
resource "discord_test_entitlement" "test" {
  sku_id     = %q
  owner_type = %q
  owner_id   = %s
}`, skuID, ownerType, ownerID))
	}
	var id, first string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      entitlement(sku, "guild", "local.server_id"),
				ExpectError: regexp.MustCompile(`owner_type value must be one of`),
			},
			{
				Config:      entitlement("100000000000000099", "server", "local.server_id"),
				ExpectError: regexp.MustCompile(`Unknown\s+SKU`),
			},
			{
				Config:      entitlement(sku, "server", `"100000000000000099"`),
				ExpectError: regexp.MustCompile(`Unknown\s+Guild`),
			},
			{
				Config: entitlement(sku, "server", "local.server_id"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(testEntitlementAddress, "application_id", discordtest.ApplicationID),
					resource.TestCheckResourceAttr(testEntitlementAddress, "sku_id", sku),
					resource.TestCheckResourceAttr(testEntitlementAddress, "owner_type", "server"),
					resource.TestCheckResourceAttr(testEntitlementAddress, "owner_id", env.serverID),
					resource.TestCheckResourceAttr(testEntitlementAddress, "type", "test_mode_purchase"),
					resource.TestCheckResourceAttr(testEntitlementAddress, "consumed", "false"),
					captureAttr(testEntitlementAddress, "id", &id),
					captureAttr(testEntitlementAddress, "id", &first),
				),
			},
			{
				ResourceName:      testEntitlementAddress,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return discordtest.ApplicationID + "/" + id, nil },
			},
			{
				ResourceName:  testEntitlementAddress,
				ImportState:   true,
				ImportStateId: id,
				ExpectError:   regexp.MustCompile(`application_id/entitlement_id`),
			},
			{
				// Granting it to a user instead replaces the entitlement and
				// deletes the old one.
				Config: entitlement(sku, "user", "local.user_id"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(testEntitlementAddress, plancheck.ResourceActionReplace)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(testEntitlementAddress, "owner_type", "user"),
					resource.TestCheckResourceAttr(testEntitlementAddress, "owner_id", env.userID),
					attrDiffers(testEntitlementAddress, "id", &id),
					captureAttr(testEntitlementAddress, "id", &id),
					env.checkEntitlementDeleted(&first),
				),
			},
			{
				// Deleted outside Terraform: granted again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteTestEntitlement(ctx, discordtest.ApplicationID, id)
				}),
				Config: entitlement(sku, "user", "local.user_id"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(testEntitlementAddress, plancheck.ResourceActionCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					attrDiffers(testEntitlementAddress, "id", &id),
					captureAttr(testEntitlementAddress, "id", &id),
				),
			},
		},
		CheckDestroy: func(s *terraform.State) error { return env.checkEntitlementDeleted(&id)(s) },
	})
}

func (e *testEnv) checkEntitlementDeleted(id *string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		ent, err := e.client.GetEntitlement(context.Background(), discordtest.ApplicationID, *id)
		if err != nil {
			return err
		}
		if !ent.Deleted {
			return errors.New("test entitlement " + *id + " was not deleted")
		}
		return nil
	}
}

package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
	"github.com/smoketurner/terraform-provider-discord/internal/discord/discordtest"
)

func TestAccSKUsDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	config := env.config(`
data "discord_skus" "default" {}
data "discord_skus" "explicit" {
  application_id = "` + discordtest.ApplicationID + `"
}`)
	var premium string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_skus.default", "application_id", discordtest.ApplicationID),
					resource.TestCheckResourceAttr("data.discord_skus.default", "skus.#", "0"),
					resource.TestCheckResourceAttr("data.discord_skus.explicit", "skus.#", "0"),
				),
			},
			{
				PreConfig: func() {
					premium = env.fake.AddSKU(discord.SKU{Type: 5, Name: "Premium", Slug: "premium", Flags: 1 << 7})
					env.fake.AddSKU(discord.SKU{Type: 6, Name: "Premium", Slug: "premium", Flags: 1 << 7})
				},
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_skus.default", "skus.#", "2"),
					resource.TestCheckResourceAttrWith("data.discord_skus.default", "skus.0.id", func(v string) error {
						if v != premium {
							return fmt.Errorf("first SKU = %s, want %s", v, premium)
						}
						return nil
					}),
					resource.TestCheckResourceAttr("data.discord_skus.default", "skus.0.type", "subscription"),
					resource.TestCheckResourceAttr("data.discord_skus.default", "skus.0.name", "Premium"),
					resource.TestCheckResourceAttr("data.discord_skus.default", "skus.0.slug", "premium"),
					resource.TestCheckResourceAttr("data.discord_skus.default", "skus.0.flags", "128"),
					resource.TestCheckResourceAttr("data.discord_skus.default", "skus.1.type", "subscription_group"),
				),
			},
			{
				Config:      env.config(`data "discord_skus" "other" { application_id = "999999999999999999" }`),
				ExpectError: regexp.MustCompile(`Unknown\s+Application`),
			},
		},
	})
}

func TestAccEntitlementsDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	alice, bob := "100000000000000010", "100000000000000011"
	skuA, skuB := "100000000000000020", "100000000000000021"
	var ids []string
	for i := range 130 {
		user, sku := alice, skuA
		if i%2 == 1 {
			user, sku = bob, skuB
		}
		ids = append(ids, env.fake.AddEntitlement(discord.Entitlement{UserID: &user, SKUID: sku, Type: 8}))
	}
	ended := env.fake.AddEntitlement(discord.Entitlement{
		GuildID: new(env.serverID), SKUID: skuA, Type: 4, StartsAt: new("2020-01-01T00:00:00+00:00"), EndsAt: new("2020-02-01T00:00:00+00:00"),
	})
	deleted := env.fake.AddEntitlement(discord.Entitlement{UserID: &alice, SKUID: skuB, Type: 3, Deleted: true, Consumed: new(true)})
	lookup := func(name, attrs string) string {
		return `
data "discord_entitlements" "` + name + `" {
` + attrs + `
}`
	}
	entitlements := "GET /applications/" + discordtest.ApplicationID + "/entitlements"
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      env.config(lookup("bad", `sku_ids = ["premium"]`)),
				ExpectError: regexp.MustCompile(`must be a Discord snowflake ID`),
			},
			{
				// The default limit of 100 returns the newest 100, sorted by ID.
				Config: env.config(lookup("recent", "") + lookup("all", "limit = 1000")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_entitlements.recent", "application_id", discordtest.ApplicationID),
					resource.TestCheckResourceAttr("data.discord_entitlements.recent", "entitlements.#", "100"),
					resource.TestCheckResourceAttr("data.discord_entitlements.recent", "entitlements.99.id", ended),
					resource.TestCheckResourceAttr("data.discord_entitlements.recent", "entitlements.0.id", ids[31]),
					resource.TestCheckResourceAttr("data.discord_entitlements.all", "entitlements.#", "131"),
					resource.TestCheckResourceAttr("data.discord_entitlements.all", "entitlements.0.id", ids[0]),
					resource.TestCheckResourceAttr("data.discord_entitlements.all", "entitlements.0.user_id", alice),
					resource.TestCheckResourceAttr("data.discord_entitlements.all", "entitlements.0.sku_id", skuA),
					resource.TestCheckResourceAttr("data.discord_entitlements.all", "entitlements.0.type", "application_subscription"),
					resource.TestCheckResourceAttr("data.discord_entitlements.all", "entitlements.0.deleted", "false"),
					resource.TestCheckNoResourceAttr("data.discord_entitlements.all", "entitlements.0.server_id"),
					resource.TestCheckNoResourceAttr("data.discord_entitlements.all", "entitlements.0.consumed"),
					resource.TestCheckNoResourceAttr("data.discord_entitlements.all", "entitlements.0.ends_at"),
					resource.TestCheckResourceAttr("data.discord_entitlements.all", "entitlements.130.server_id", env.serverID),
					resource.TestCheckResourceAttr("data.discord_entitlements.all", "entitlements.130.type", "test_mode_purchase"),
					resource.TestCheckResourceAttr("data.discord_entitlements.all", "entitlements.130.ends_at", "2020-02-01T00:00:00+00:00"),
					// One request for the default 100, two for all 131.
					env.countRequests(entitlements, 3),
				),
			},
			{
				Config: env.config(lookup("bob", `user_id = "`+bob+`"`) +
					lookup("server", `server_id = local.server_id`) +
					lookup("sku", `sku_ids = ["`+skuB+`"]`+"\n  exclude_deleted = false\n  limit = 1000") +
					lookup("active", `server_id = local.server_id`+"\n  exclude_ended = true") +
					lookup("oldest", `after = "0"`+"\n  limit = 101") +
					lookup("window", `after = "`+ids[9]+`"`+"\n"+`  before = "`+ids[13]+`"`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_entitlements.bob", "entitlements.#", "65"),
					resource.TestCheckResourceAttr("data.discord_entitlements.bob", "entitlements.0.id", ids[1]),
					resource.TestCheckResourceAttr("data.discord_entitlements.server", "entitlements.#", "1"),
					resource.TestCheckResourceAttr("data.discord_entitlements.active", "entitlements.#", "0"),
					resource.TestCheckResourceAttr("data.discord_entitlements.sku", "entitlements.#", "66"),
					resource.TestCheckResourceAttr("data.discord_entitlements.sku", "entitlements.65.id", deleted),
					resource.TestCheckResourceAttr("data.discord_entitlements.sku", "entitlements.65.deleted", "true"),
					resource.TestCheckResourceAttr("data.discord_entitlements.sku", "entitlements.65.consumed", "true"),
					resource.TestCheckResourceAttr("data.discord_entitlements.sku", "entitlements.65.type", "developer_gift"),
					resource.TestCheckResourceAttr("data.discord_entitlements.oldest", "entitlements.#", "101"),
					resource.TestCheckResourceAttr("data.discord_entitlements.oldest", "entitlements.100.id", ids[100]),
					resource.TestCheckResourceAttr("data.discord_entitlements.window", "entitlements.#", "3"),
					resource.TestCheckResourceAttr("data.discord_entitlements.window", "entitlements.0.id", ids[10]),
				),
			},
			{
				Config:      env.config(lookup("other", `application_id = "999999999999999999"`)),
				ExpectError: regexp.MustCompile(`Unknown\s+Application`),
			},
		},
	})
}

func TestAccSKUSubscriptionsDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	user, other := "100000000000000010", "100000000000000011"
	sku := env.fake.AddSKU(discord.SKU{Type: 5, Name: "Premium", Slug: "premium"})
	var ids []string
	for range 3 {
		ids = append(ids, env.fake.AddSubscription(discord.Subscription{
			UserID: user, SKUIDs: []string{sku}, EntitlementIDs: []string{"100000000000000030"},
			CurrentPeriodStart: "2024-08-27T19:48:44+00:00", CurrentPeriodEnd: "2024-09-27T19:48:44+00:00",
		}))
	}
	canceled := env.fake.AddSubscription(discord.Subscription{
		UserID: user, SKUIDs: []string{sku}, EntitlementIDs: []string{}, RenewalSKUIDs: []string{sku}, Status: 2,
		CurrentPeriodStart: "2024-08-27T19:48:44+00:00", CurrentPeriodEnd: "2024-09-27T19:48:44+00:00",
		CanceledAt: new("2024-09-01T00:00:00+00:00"),
	})
	env.fake.AddSubscription(discord.Subscription{UserID: other, SKUIDs: []string{sku}, EntitlementIDs: []string{}})
	lookup := func(name, attrs string) string {
		return `
data "discord_sku_subscriptions" "` + name + `" {
  sku_id  = "` + sku + `"
  user_id = "` + user + `"
` + attrs + `
}`
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      env.config(`data "discord_sku_subscriptions" "bad" { sku_id = "` + sku + `" }`),
				ExpectError: regexp.MustCompile(`The argument "user_id" is required`),
			},
			{
				Config: env.config(lookup("all", "") + lookup("first", "limit = 1\n  after = \"0\"") + lookup("before", `before = "`+ids[2]+`"`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_sku_subscriptions.all", "subscriptions.#", "4"),
					resource.TestCheckResourceAttr("data.discord_sku_subscriptions.all", "subscriptions.0.id", ids[0]),
					resource.TestCheckResourceAttr("data.discord_sku_subscriptions.all", "subscriptions.0.user_id", user),
					resource.TestCheckResourceAttr("data.discord_sku_subscriptions.all", "subscriptions.0.status", "active"),
					resource.TestCheckResourceAttr("data.discord_sku_subscriptions.all", "subscriptions.0.sku_ids.0", sku),
					resource.TestCheckResourceAttr("data.discord_sku_subscriptions.all", "subscriptions.0.entitlement_ids.#", "1"),
					resource.TestCheckNoResourceAttr("data.discord_sku_subscriptions.all", "subscriptions.0.renewal_sku_ids"),
					resource.TestCheckNoResourceAttr("data.discord_sku_subscriptions.all", "subscriptions.0.canceled_at"),
					resource.TestCheckResourceAttr("data.discord_sku_subscriptions.all", "subscriptions.0.current_period_end", "2024-09-27T19:48:44+00:00"),
					resource.TestCheckResourceAttr("data.discord_sku_subscriptions.all", "subscriptions.3.id", canceled),
					resource.TestCheckResourceAttr("data.discord_sku_subscriptions.all", "subscriptions.3.status", "ending"),
					resource.TestCheckResourceAttr("data.discord_sku_subscriptions.all", "subscriptions.3.canceled_at", "2024-09-01T00:00:00+00:00"),
					resource.TestCheckResourceAttr("data.discord_sku_subscriptions.all", "subscriptions.3.renewal_sku_ids.0", sku),
					resource.TestCheckResourceAttr("data.discord_sku_subscriptions.first", "subscriptions.#", "1"),
					resource.TestCheckResourceAttr("data.discord_sku_subscriptions.first", "subscriptions.0.id", ids[0]),
					resource.TestCheckResourceAttr("data.discord_sku_subscriptions.before", "subscriptions.#", "2"),
				),
			},
		},
	})
}

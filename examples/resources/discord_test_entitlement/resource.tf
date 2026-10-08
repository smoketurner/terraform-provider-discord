data "discord_skus" "app" {}

# Gives a development server the premium subscription, to test premium features
# without a purchase.
resource "discord_test_entitlement" "dev_server" {
  sku_id     = one([for s in data.discord_skus.app.skus : s.id if s.type == "subscription"])
  owner_type = "server"
  owner_id   = var.server_id
}

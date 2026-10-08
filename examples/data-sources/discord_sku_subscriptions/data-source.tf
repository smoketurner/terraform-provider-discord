data "discord_skus" "app" {}

data "discord_sku_subscriptions" "alice" {
  sku_id  = one([for s in data.discord_skus.app.skus : s.id if s.type == "subscription"])
  user_id = var.user_id
}

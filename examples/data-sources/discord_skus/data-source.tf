data "discord_skus" "app" {}

output "subscription_sku_ids" {
  value = [for s in data.discord_skus.app.skus : s.id if s.type == "subscription"]
}

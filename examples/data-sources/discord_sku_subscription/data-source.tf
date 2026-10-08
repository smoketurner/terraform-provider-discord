data "discord_sku_subscription" "alice" {
  sku_id = var.sku_id
  id     = var.subscription_id
}

output "alice_subscription_status" {
  value = data.discord_sku_subscription.alice.status
}

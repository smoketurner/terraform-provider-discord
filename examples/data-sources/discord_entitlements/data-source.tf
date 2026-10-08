# Active entitlements of a user, for the bot's own application.
data "discord_entitlements" "alice" {
  user_id       = var.user_id
  exclude_ended = true
}

output "alice_sku_ids" {
  value = toset(data.discord_entitlements.alice.entitlements[*].sku_id)
}

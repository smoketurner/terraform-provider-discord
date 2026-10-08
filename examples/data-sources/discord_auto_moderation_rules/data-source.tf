data "discord_auto_moderation_rules" "all" {
  server_id = var.server_id
}

output "disabled_rules" {
  value = [for r in data.discord_auto_moderation_rules.all.rules : r.name if !r.enabled]
}

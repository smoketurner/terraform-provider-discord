data "discord_role" "moderators" {
  server_id = var.server_id
  name      = "Moderators"
}

data "discord_role_member_counts" "all" {
  server_id = var.server_id
}

output "moderator_count" {
  value = lookup(data.discord_role_member_counts.all.counts, data.discord_role.moderators.id, 0)
}

# Requires the Server Members privileged intent.
data "discord_members" "all" {
  server_id = var.server_id
}

output "bots" {
  value = [for m in data.discord_members.all.members : m.username if m.bot]
}

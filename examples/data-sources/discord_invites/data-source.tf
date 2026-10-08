data "discord_invites" "all" {
  server_id = var.server_id
}

output "permanent_invites" {
  value = [for i in data.discord_invites.all.invites : i.code if i.max_age == 0]
}

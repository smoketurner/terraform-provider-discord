data "discord_roles" "all" {
  server_id = var.server_id
}

output "managed_roles" {
  value = [for r in data.discord_roles.all.roles : r.name if r.managed]
}

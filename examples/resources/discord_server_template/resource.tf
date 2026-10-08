resource "discord_server_template" "community" {
  server_id   = var.server_id
  name        = "Community"
  description = "Roles and channels for a community server"
}

output "template_code" {
  value = discord_server_template.community.code
}

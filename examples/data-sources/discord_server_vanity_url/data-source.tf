data "discord_server_vanity_url" "main" {
  server_id = var.server_id
}

output "vanity_invite" {
  value = data.discord_server_vanity_url.main.url
}

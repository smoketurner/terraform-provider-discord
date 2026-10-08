resource "discord_server_widget" "main" {
  server_id  = var.server_id
  enabled    = true
  channel_id = discord_text_channel.welcome.id
}

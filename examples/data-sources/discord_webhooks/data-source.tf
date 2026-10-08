data "discord_webhooks" "all" {
  server_id = var.server_id
}

data "discord_webhooks" "announcements" {
  server_id  = var.server_id
  channel_id = var.channel_id
}

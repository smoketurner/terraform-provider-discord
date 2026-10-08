data "discord_threads" "active" {
  server_id = var.server_id
}

data "discord_threads" "archived" {
  server_id  = var.server_id
  channel_id = var.channel_id
  archived   = "public"
}

list "discord_channel_permission" "all" {
  provider         = discord
  include_resource = true

  config {
    server_id  = var.server_id
    channel_id = var.channel_id
  }
}

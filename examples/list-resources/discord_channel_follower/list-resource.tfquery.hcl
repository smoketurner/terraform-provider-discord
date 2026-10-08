list "discord_channel_follower" "all" {
  provider         = discord
  include_resource = true

  config {
    server_id = var.server_id
  }
}

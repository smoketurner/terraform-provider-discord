list "discord_webhook" "all" {
  provider         = discord
  include_resource = true

  config {
    server_id  = var.server_id
    channel_id = var.channel_id
  }
}

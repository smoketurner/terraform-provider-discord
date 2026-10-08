list "discord_stage_channel" "all" {
  provider         = discord
  include_resource = true

  config {
    server_id = var.server_id
  }
}

list "discord_soundboard_sound" "all" {
  provider         = discord
  include_resource = true

  config {
    server_id = var.server_id
  }
}

list "discord_auto_moderation_rule" "all" {
  provider         = discord
  include_resource = true

  config {
    server_id = var.server_id
  }
}

list "discord_server_template" "all" {
  provider         = discord
  include_resource = true

  config {
    server_id = var.server_id
  }
}

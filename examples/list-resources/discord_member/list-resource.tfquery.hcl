list "discord_member" "all" {
  provider         = discord
  include_resource = true
  limit            = 1000

  config {
    server_id = var.server_id
  }
}

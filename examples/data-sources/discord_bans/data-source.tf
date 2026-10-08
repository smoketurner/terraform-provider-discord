data "discord_bans" "recent" {
  server_id = var.server_id
  limit     = 100
}

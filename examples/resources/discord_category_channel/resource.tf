resource "discord_category_channel" "community" {
  server_id = var.server_id
  name      = "Community"
}

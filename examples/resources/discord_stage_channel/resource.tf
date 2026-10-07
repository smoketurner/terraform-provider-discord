# Requires the server to have Community enabled.
resource "discord_stage_channel" "town_hall" {
  server_id   = var.server_id
  name        = "Town Hall"
  category_id = discord_category_channel.community.id
}

# Includes VIP regions when the server has them.
data "discord_voice_regions" "server" {
  server_id = var.server_id
}

resource "discord_voice_channel" "lobby" {
  server_id  = var.server_id
  name       = "Lobby"
  rtc_region = one([for r in data.discord_voice_regions.server.regions : r.id if r.optimal])
}

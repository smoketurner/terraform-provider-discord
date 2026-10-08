resource "discord_emoji" "party" {
  server_id = var.server_id
  name      = "party_parrot"
  image     = "data:image/gif;base64,${filebase64("${path.module}/party_parrot.gif")}"
}

resource "discord_role" "blurple" {
  server_id = var.server_id
  name      = "Blurple"
  color     = provider::discord::color("#5865F2")
}

# On Terraform 1.8 and later, prefer provider::discord::color().
data "discord_color" "blurple" {
  hex = "#5865F2"
}

data "discord_color" "green" {
  rgb = [87, 242, 135]
}

resource "discord_role" "blurple" {
  server_id = var.server_id
  name      = "Blurple"
  color     = data.discord_color.blurple.color
}

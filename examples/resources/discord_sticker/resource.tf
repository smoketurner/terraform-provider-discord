resource "discord_sticker" "wave" {
  server_id   = var.server_id
  name        = "Wave"
  description = "Wumpus waves hello"
  tags        = "wave"
  file        = filebase64("${path.module}/wave.png")
}

# With Terraform 1.11 or later, file_wo keeps the file out of state. Bump
# file_wo_version to upload a new file, which creates a new sticker.
resource "discord_sticker" "dance" {
  server_id       = var.server_id
  name            = "Dance"
  tags            = "dancer"
  file_wo         = filebase64("${path.module}/dance.gif")
  file_wo_version = 1
}

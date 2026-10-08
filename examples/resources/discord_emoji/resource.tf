resource "discord_emoji" "party" {
  server_id = var.server_id
  name      = "party_parrot"
  image     = "data:image/gif;base64,${filebase64("${path.module}/party_parrot.gif")}"
}

# With Terraform 1.11 or later, image_wo keeps the image out of state. Bump
# image_wo_version to upload a new image.
resource "discord_emoji" "wave" {
  server_id        = var.server_id
  name             = "wave"
  image_wo         = "data:image/png;base64,${filebase64("${path.module}/wave.png")}"
  image_wo_version = 1
}

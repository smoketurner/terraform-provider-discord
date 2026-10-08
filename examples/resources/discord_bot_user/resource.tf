resource "discord_bot_user" "this" {
  username = "Helper"

  # With Terraform 1.11 or later, avatar_wo keeps the image out of state. Bump
  # avatar_wo_version to upload a new image.
  avatar_wo         = "data:image/png;base64,${filebase64("${path.module}/avatar.png")}"
  avatar_wo_version = 1
}

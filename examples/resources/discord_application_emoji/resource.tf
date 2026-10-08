resource "discord_application_emoji" "check" {
  name  = "check"
  image = "data:image/png;base64,${filebase64("${path.module}/check.png")}"
}

# With Terraform 1.11 or later, image_wo keeps the image out of state. Bump
# image_wo_version to upload a new emoji.
resource "discord_application_emoji" "cross" {
  name             = "cross"
  image_wo         = "data:image/png;base64,${filebase64("${path.module}/cross.png")}"
  image_wo_version = 1
}

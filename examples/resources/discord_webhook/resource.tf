resource "discord_webhook" "deploys" {
  channel_id = discord_text_channel.general.id
  name       = "Deploy Bot"
  avatar     = "data:image/png;base64,${filebase64("${path.module}/avatar.png")}"
}

# With Terraform 1.11 or later, avatar_wo keeps the avatar out of state. Bump
# avatar_wo_version to upload a new avatar.
resource "discord_webhook" "alerts" {
  channel_id        = discord_text_channel.general.id
  name              = "Alert Bot"
  avatar_wo         = "data:image/png;base64,${filebase64("${path.module}/alerts.png")}"
  avatar_wo_version = 1
}

output "deploy_webhook_url" {
  value     = discord_webhook.deploys.url
  sensitive = true
}

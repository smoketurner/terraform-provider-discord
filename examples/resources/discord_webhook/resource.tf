resource "discord_webhook" "deploys" {
  channel_id = discord_text_channel.general.id
  name       = "Deploy Bot"
  avatar     = "data:image/png;base64,${filebase64("${path.module}/avatar.png")}"
}

output "deploy_webhook_url" {
  value     = discord_webhook.deploys.url
  sensitive = true
}

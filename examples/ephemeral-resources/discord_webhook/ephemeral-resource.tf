resource "discord_webhook" "deploys" {
  channel_id    = discord_text_channel.deploys.id
  name          = "Deploys"
  store_secrets = false
}

ephemeral "discord_webhook" "deploys" {
  id = discord_webhook.deploys.id
}

# The URL reaches SSM through a write-only argument (Terraform 1.11 or later),
# so it is never stored in any Terraform state.
resource "aws_ssm_parameter" "deploy_webhook" {
  name             = "/discord/deploy-webhook"
  type             = "SecureString"
  value_wo         = ephemeral.discord_webhook.deploys.url
  value_wo_version = 1
}

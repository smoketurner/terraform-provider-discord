resource "discord_webhook" "status" {
  channel_id    = discord_text_channel.status.id
  name          = "Status"
  store_secrets = false
}

resource "discord_webhook_message" "status" {
  webhook_id = discord_webhook.status.id
  username   = "Status Page"
  avatar_url = "https://example.com/status.png"
  embeds = [{
    title       = "All systems operational"
    description = "Updated by Terraform."
    color       = provider::discord::color("#2ecc71")
  }]
}

# A webhook in a forum channel starts a post with thread_name, or posts in an
# existing post with thread_id.
resource "discord_webhook" "releases" {
  channel_id    = discord_forum_channel.releases.id
  name          = "Releases"
  store_secrets = false
}

resource "discord_webhook_message" "release" {
  webhook_id  = discord_webhook.releases.id
  thread_name = "v1.2.0"
  content     = "Release notes for v1.2.0."
}

resource "discord_webhook_message" "release_assets" {
  webhook_id = discord_webhook.releases.id
  thread_id  = discord_webhook_message.release.channel_id
  content    = "Downloads: https://example.com/releases/v1.2.0"
}

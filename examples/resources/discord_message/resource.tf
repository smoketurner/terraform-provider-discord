resource "discord_message" "rules" {
  channel_id = discord_text_channel.rules.id
  pinned     = true

  embeds = [{
    title       = "Server rules"
    description = "Please read these before posting."
    color       = provider::discord::color("#5865F2")
    footer_text = "Managed by Terraform"
    fields = [
      { name = "1. Be respectful", value = "No harassment or hate speech." },
      { name = "2. Stay on topic", value = "Use the right channel for your question." },
    ]
  }]
}

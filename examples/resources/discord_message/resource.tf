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

# Link previews hidden, a file attached and no notification sent.
resource "discord_message" "faq" {
  channel_id             = discord_text_channel.rules.id
  content                = "Read the FAQ: https://example.com/faq"
  suppress_embeds        = true
  suppress_notifications = true

  attachments = [{
    filename    = "faq.pdf"
    source      = "${path.module}/faq.pdf"
    source_hash = filesha256("${path.module}/faq.pdf")
    description = "Frequently asked questions"
  }]
}

# A Components V2 message laid out with components only.
resource "discord_message" "welcome" {
  channel_id    = discord_text_channel.rules.id
  components_v2 = true

  attachments = [{
    filename       = "banner.png"
    content_base64 = filebase64("${path.module}/banner.png")
  }]

  components = jsonencode([{
    type         = 17
    accent_color = provider::discord::color("#5865F2")
    components = [
      { type = 12, items = [{ media = { url = "attachment://banner.png" } }] },
      { type = 10, content = "# Welcome!\nPick your roles below." },
      {
        type = 1
        components = [
          { type = 2, style = 5, label = "Rules", url = "https://example.com/rules" },
        ]
      },
    ]
  }])
}

# A poll, open for two days.
resource "discord_message" "lunch" {
  channel_id = discord_text_channel.rules.id

  poll = {
    question = "What should we order for lunch?"
    answers = [
      { text = "Pizza", emoji_name = "🍕" },
      { text = "Tacos", emoji_name = "🌮" },
    ]
    duration = 48
  }
}

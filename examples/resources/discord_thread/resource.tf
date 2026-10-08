# A post in a forum channel, pinned and tagged.
resource "discord_thread" "faq" {
  channel_id   = discord_forum_channel.help.id
  name         = "Frequently asked questions"
  pinned       = true
  applied_tags = [discord_forum_channel.help.available_tags[0].id]

  message = {
    content = "Read this before opening a new post."
    embeds = [{
      title       = "Getting started"
      description = "Check the docs and search existing posts first."
      color       = provider::discord::color("#5865F2")
    }]
  }
}

# A private thread in a text channel, kept open.
resource "discord_thread" "moderators" {
  channel_id = discord_text_channel.general.id
  name       = "Moderators"
  private    = true
  invitable  = false
  archived   = false
}

# A thread started from an existing message.
resource "discord_thread" "release" {
  channel_id = discord_text_channel.general.id
  message_id = discord_message.release.id
  name       = "Release discussion"
}

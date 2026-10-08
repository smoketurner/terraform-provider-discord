resource "discord_message" "roles" {
  channel_id = discord_text_channel.roles.id
  content    = "React to pick your roles."
}

# Seed the reactions members click on.
resource "discord_message_reaction" "thumbs_up" {
  channel_id = discord_message.roles.channel_id
  message_id = discord_message.roles.id
  emoji      = "👍"
}

# Custom emoji are given as name:id.
resource "discord_message_reaction" "party" {
  channel_id = discord_message.roles.channel_id
  message_id = discord_message.roles.id
  emoji      = "${discord_emoji.party.name}:${discord_emoji.party.id}"
}

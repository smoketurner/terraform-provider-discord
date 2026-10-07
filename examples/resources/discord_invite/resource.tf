resource "discord_invite" "website" {
  channel_id = discord_text_channel.general.id
  max_age    = 0 # never expires
}

output "invite_url" {
  value = discord_invite.website.url
}

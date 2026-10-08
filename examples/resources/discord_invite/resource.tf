resource "discord_invite" "website" {
  channel_id = discord_text_channel.general.id
  max_age    = 0 # never expires
}

output "invite_url" {
  value = discord_invite.website.url
}

# Grants a role on joining and can only be accepted by the listed users.
resource "discord_invite" "beta_testers" {
  channel_id      = discord_text_channel.general.id
  max_age         = 604800
  role_ids        = [discord_role.beta.id]
  target_user_ids = ["456789012345678901", "567890123456789012"]
}

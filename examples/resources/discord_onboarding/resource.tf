resource "discord_onboarding" "main" {
  server_id = var.server_id
  enabled   = true

  # At least 7 channels, 5 of which @everyone can send messages in.
  default_channel_ids = [for c in discord_text_channel.default : c.id]

  prompts = [
    {
      title         = "What brings you here?"
      single_select = true
      required      = true
      options = [
        {
          title       = "Playing"
          description = "Find people to play with"
          emoji_name  = "🎮"
          role_ids    = [discord_role.players.id]
          channel_ids = [discord_text_channel.lfg.id]
        },
        {
          title       = "Modding"
          emoji_id    = discord_emoji.wrench.id
          channel_ids = [discord_forum_channel.mods.id]
        },
      ]
    },
    {
      title         = "Pronouns"
      type          = "dropdown"
      in_onboarding = false
      options = [
        { title = "he/him", role_ids = [discord_role.he.id] },
        { title = "she/her", role_ids = [discord_role.she.id] },
        { title = "they/them", role_ids = [discord_role.they.id] },
      ]
    },
  ]
}

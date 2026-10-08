resource "discord_text_channel" "general" {
  server_id           = var.server_id
  name                = "general"
  category_id         = discord_category_channel.community.id
  topic               = "Chat about anything"
  rate_limit_per_user = 5
}

# A private channel: the overwrites are sent in the request that creates it,
# so it is never visible to @everyone.
resource "discord_text_channel" "staff" {
  server_id = var.server_id
  name      = "staff"

  initial_permission_overwrites = [
    {
      id   = var.server_id # @everyone
      type = "role"
      deny = provider::discord::permissions(["VIEW_CHANNEL"])
    },
    {
      id    = discord_role.staff.id
      type  = "role"
      allow = provider::discord::permissions(["VIEW_CHANNEL"])
    },
  ]
}

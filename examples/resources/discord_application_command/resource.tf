# A global slash command with a parameter limited to fixed choices.
resource "discord_application_command" "meetup" {
  name                       = "meetup"
  description                = "Show the next meetup"
  default_member_permissions = provider::discord::permissions(["SEND_MESSAGES"])
  contexts                   = ["guild", "bot_dm"]
  integration_types          = ["guild_install"]

  description_localizations = {
    de = "Zeigt das nächste Treffen"
  }

  options = [
    {
      type        = "string"
      name        = "when"
      description = "Which meetup"
      required    = true
      choices = [
        { name = "Next", value = "next" },
        { name = "Last", value = "last" },
      ]
    },
    {
      type        = "integer"
      name        = "days"
      description = "Look ahead this many days"
      min_value   = 1
      max_value   = 30
    },
  ]
}

# A server command with a subcommand group: /roles self add <role>.
resource "discord_application_command" "roles" {
  server_id   = var.server_id
  name        = "roles"
  description = "Manage roles"

  options = [
    {
      type        = "sub_command_group"
      name        = "self"
      description = "Your own roles"
      options = [
        {
          type        = "sub_command"
          name        = "add"
          description = "Add a role to yourself"
          options = [
            { type = "role", name = "role", description = "Role to add", required = true },
          ]
        },
      ]
    },
  ]
}

# A command in the user context menu.
resource "discord_application_command" "high_five" {
  type = "user"
  name = "High Five"
}

data "discord_current_user" "bot" {}

data "discord_server_member_permissions" "bot" {
  server_id = var.server_id
  user_id   = data.discord_current_user.bot.id
}

check "bot_can_manage_roles" {
  assert {
    condition     = contains(data.discord_server_member_permissions.bot.permission_names, "MANAGE_ROLES")
    error_message = "The bot needs Manage Roles to manage discord_role resources."
  }
}

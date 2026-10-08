resource "discord_application_role_connection_metadata" "this" {
  records = [
    {
      type        = "boolean_equal"
      key         = "member"
      name        = "Member"
      description = "Has an account on the site"
    },
    {
      type        = "datetime_less_than_or_equal"
      key         = "joined"
      name        = "Member for"
      description = "Days since the account was created"
      name_localizations = {
        fr = "Membre depuis"
      }
    },
  ]
}

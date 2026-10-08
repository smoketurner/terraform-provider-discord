data "discord_current_application" "app" {}

output "application_id" {
  value = data.discord_current_application.app.id
}

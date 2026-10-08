terraform {
  required_providers {
    discord = {
      source = "smoketurner/discord"
    }
  }
}

# The token can also be supplied with the DISCORD_TOKEN environment variable.
provider "discord" {
  token = var.discord_token

  # Optional: recorded in the server's audit log for changes the provider makes.
  audit_log_reason = "Managed by Terraform"
}

variable "discord_token" {
  type      = string
  sensitive = true
}

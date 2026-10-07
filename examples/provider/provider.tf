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
}

variable "discord_token" {
  type      = string
  sensitive = true
}

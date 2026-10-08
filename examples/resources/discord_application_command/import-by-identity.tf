# Global command: omit server_id.
import {
  to = discord_application_command.meetup
  identity = {
    application_id = "123456789012345678"
    command_id     = "345678901234567890"
  }
}

# Server command
import {
  to = discord_application_command.roles
  identity = {
    application_id = "123456789012345678"
    server_id      = "234567890123456789"
    command_id     = "456789012345678901"
  }
}

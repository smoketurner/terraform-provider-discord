---
page_title: "Adopt an existing server"
subcategory: ""
description: |-
  Generate configuration and import blocks for a server built by hand, with terraform query.
---

# Adopt an existing server

A server built by hand in the Discord client already has its roles, channels, permission overwrites and webhooks.
Terraform 1.14 and later can find them with `terraform query` and write the configuration and `import` blocks that
bring them under management, instead of you writing one `import` block per object.

The provider has a list resource for every resource with a Discord list endpoint:

| List resource | Lists | Config |
| --- | --- | --- |
| `discord_role` | roles, except `@everyone` and roles managed by an integration | `server_id` |
| `discord_category_channel`, `discord_text_channel`, `discord_announcement_channel`, `discord_voice_channel`, `discord_stage_channel`, `discord_forum_channel`, `discord_media_channel` | channels of that type | `server_id` |
| `discord_channel_permission` | permission overwrites | `server_id`, optional `channel_id` |
| `discord_webhook` | incoming webhooks | `server_id`, optional `channel_id` |
| `discord_channel_follower` | channels following an announcement channel | `server_id`, optional `channel_id` |
| `discord_invite` | invites | `server_id`, optional `channel_id` |
| `discord_thread` | active threads and forum posts | `server_id`, optional `channel_id` |
| `discord_emoji`, `discord_sticker`, `discord_soundboard_sound` | custom emojis (except managed ones), stickers and sounds | `server_id` |
| `discord_auto_moderation_rule`, `discord_scheduled_event`, `discord_server_template` | AutoMod rules, scheduled events, templates | `server_id` |
| `discord_member`, `discord_ban` | members (Server Members intent) and bans | `server_id` |
| `discord_application_command` | the bot's commands | optional `server_id` (omit for global commands) |
| `discord_application_emoji` | the bot application's emojis | none |

Server-wide settings have one instance per server, so import them with an `import` block instead:
`discord_server_settings`, `discord_role_everyone`, `discord_onboarding`, `discord_welcome_screen`,
`discord_server_widget` and `discord_server_incident_actions`.

## 1. Configure the provider

In an empty directory, configure the provider as usual, with a variable for the server:

```terraform
terraform {
  required_providers {
    discord = {
      source = "smoketurner/discord"
    }
  }
}

provider "discord" {}

variable "server_id" {
  type = string
}
```

Run `terraform init`.

## 2. Write a query

Queries live in files ending in `.tfquery.hcl`. Add a `list` block for each kind of object to adopt. With
`include_resource = true` each result carries the object's full state, which Terraform needs to generate its
configuration.

```terraform
# server.tfquery.hcl
list "discord_role" "all" {
  provider         = discord
  include_resource = true

  config {
    server_id = var.server_id
  }
}

list "discord_category_channel" "all" {
  provider         = discord
  include_resource = true

  config {
    server_id = var.server_id
  }
}

list "discord_text_channel" "all" {
  provider         = discord
  include_resource = true

  config {
    server_id = var.server_id
  }
}

list "discord_channel_permission" "all" {
  provider         = discord
  include_resource = true

  config {
    server_id = var.server_id
  }
}

list "discord_webhook" "all" {
  provider         = discord
  include_resource = true

  config {
    server_id = var.server_id
  }
}
```

Terraform returns at most 100 results per `list` block by default. Set `limit` in the block for larger servers.

## 3. Run the query

```shell
terraform query -var server_id=123456789012345678
```

Terraform prints each object it found with its identity and name:

```
list.discord_role.all   role_id=200000000000000001,server_id=123456789012345678   Moderators

list.discord_category_channel.all   channel_id=200000000000000002   Community

list.discord_text_channel.all   channel_id=200000000000000003   general

list.discord_channel_permission.all   channel_id=200000000000000003,overwrite_id=200000000000000001   #general role 200000000000000001

list.discord_webhook.all   webhook_id=200000000000000005   Alerts
```

## 4. Generate configuration

Run the query again and write the configuration to a new file:

```shell
terraform query -var server_id=123456789012345678 -generate-config-out=generated.tf
```

For each result, `generated.tf` holds a `resource` block with the object's current settings and an `import` block
that imports it by its identity:

```terraform
resource "discord_text_channel" "all_0" {
  provider            = discord
  category_id         = "200000000000000002"
  name                = "general"
  nsfw                = false
  rate_limit_per_user = 0
  server_id           = "123456789012345678"
  topic               = "Say hi"
}

import {
  to       = discord_text_channel.all_0
  provider = discord
  identity = {
    channel_id = "200000000000000003"
  }
}
```

## 5. Review and import

Rename the generated resources, replace IDs with references (for example
`category_id = discord_category_channel.community.id`), and move the blocks into your configuration. Then plan:

```shell
terraform plan -var server_id=123456789012345678
```

The plan should only import, with no changes:

```
Plan: 5 to import, 0 to add, 0 to change, 0 to destroy.
```

Run `terraform apply` to record the objects in state, then delete the `import` blocks.

## Notes

- Discord does not return uploaded files or secrets, so imported emojis, stickers, sounds and images have no `image`,
  `file`, `sound` or `icon` in state, and imported webhooks keep `store_secrets = false`. Set them in configuration
  only if you want to replace the upload.
- `initial_permission_overwrites` and `audit_log_reason` only apply to requests Terraform makes, so they stay null.
  Manage permission overwrites with the `discord_channel_permission` results instead.
- Starting November 16, 2026, Discord omits channels the bot cannot view from the channel lists. Give the bot the
  View Channel permission on every channel to adopt.

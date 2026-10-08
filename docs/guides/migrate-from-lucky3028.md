---
page_title: "Migrate from Lucky3028/discord"
subcategory: ""
description: |-
  Switch from the archived Lucky3028/discord provider to this one without recreating Discord objects.
---

# Migrate from Lucky3028/discord

[Lucky3028/discord](https://registry.terraform.io/providers/Lucky3028/discord/latest) is archived. This provider can
take over the channels, roles, messages and other objects it manages without deleting and recreating them: Terraform
hands each resource's state to this provider, which translates it to its own schema, and the refresh that follows reads
the rest from Discord, as after an import.

Moving state between providers needs Terraform 1.8 or later.

## Before you start

1. Run `terraform plan` with Lucky3028/discord and resolve any changes, so that the state matches Discord.
2. Back up the state: `terraform state pull > lucky3028.tfstate`.
3. Keep the Lucky3028/discord provider installable until the migration is applied. Terraform reads its schema for the
   resources that are still in the state, so `terraform init` installs it from the state even after you remove it from
   `required_providers`.

## 1. Switch the provider

Change the provider source. The `token` argument and the `DISCORD_TOKEN` environment variable work as before; remove
`client_id` and `secret`.

```terraform
terraform {
  required_providers {
    discord = {
      source = "smoketurner/discord"
    }
  }
}

provider "discord" {}
```

Run `terraform init -upgrade`.

## 2. Move the resources

How a resource moves depends on whether its type name changes.

**Resources whose type name changes** need a `moved` block from the old type to the new one:

```terraform
moved {
  from = discord_news_channel.announcements
  to   = discord_announcement_channel.announcements
}

moved {
  from = discord_server.main
  to   = discord_server_settings.main
}
```

**Resources whose type name is unchanged** need no `moved` block: Terraform rejects one whose `from` and `to` are
the same address (`Redundant move statement`). Changing the provider is enough. Terraform reads the old state with this
provider's schema and the refresh rebuilds it from Discord. That loses the values Discord does not return, so for the
following resources move them to a new name instead, which runs this provider's translation of the old state:

- `discord_auto_moderation_rule`: required. Lucky3028/discord stores its `trigger_metadata` in a form this provider
  cannot read, and the plan fails with `Unable to Read Previously Saved State`.
- `discord_message` with `file` blocks: keeps each file's `source`, which Discord does not return. Without it the plan
  shows `source` being added to each attachment, an update that records it without uploading the files again.
- `discord_webhook` with `avatar_data_uri`: keeps the avatar, which Discord only returns as a hash. Without it the plan
  uploads the avatar again.
- `discord_role_positions`: keeps the order. Without it the plan applies the configured order again, which changes
  nothing if it is already in place.

```terraform
moved {
  from = discord_auto_moderation_rule.invites
  to   = discord_auto_moderation_rule.block_invites
}
```

A later `moved` block can rename the resource back; within one provider, Terraform allows it.

## 3. Update the configuration

The arguments of most resources changed. Rewrite each resource as described in the table below and in its
documentation. Arguments that this provider reads from Discord, such as the channel `position`, are left out of the
configuration and filled in by the refresh.

| Lucky3028/discord | This provider | Configuration changes |
| --- | --- | --- |
| `discord_category_channel` | `discord_category_channel` | Remove `position` (read-only; order channels with `discord_channel_positions`) and `type`. |
| `discord_text_channel`, `discord_voice_channel`, `discord_forum_channel` | same names | `category` becomes `category_id`. Remove `position`, `type` and `sync_perms_with_category`. |
| `discord_news_channel` | `discord_announcement_channel` | As for text channels; announcement channels have no `rate_limit_per_user`. |
| `discord_channel_permission` | `discord_channel_permission` | `type = "user"` becomes `type = "member"`. `allow` and `deny` are decimal strings; numbers are converted. |
| `discord_role` | `discord_role` | Remove `position` (read-only; order roles with `discord_role_positions`). `permissions` is a decimal string. |
| `discord_role_everyone` | `discord_role_everyone` | `permissions` is required and is a decimal string. |
| `discord_role_positions` | `discord_role_positions` | The `position` blocks become `role_ids`, ordered from the highest role to the lowest. |
| `discord_member_roles` | `discord_member_roles` | The `role` blocks become `role_ids`, which is authoritative. See [Member roles](#member-roles). |
| `discord_message` | `discord_message` | The `embed` block becomes `embeds`, a list whose `footer`, `image`, `thumbnail` and `author` blocks are flattened to `footer_text`, `image_url`, `thumbnail_url`, `author_name` and so on. Each `file` block becomes an `attachments` entry with `filename` and `source`. Remove `tts` and `edited_timestamp`. |
| `discord_webhook` | `discord_webhook` | `avatar_data_uri` becomes `avatar`. `avatar_url` is not supported: use `avatar = "data:image/png;base64,${filebase64("avatar.png")}"`. |
| `discord_invite` | `discord_invite` | Keep the `unique` value the invite was created with: `unique = false` unless it was set. Lucky3028/discord defaults to `false` and this provider to `true`, and changing it replaces the invite. |
| `discord_guild_sticker` | `discord_sticker` | `file` holds the file's content, not its path: `file = filebase64("wave.png")`. Adopting it after the move only updates state. |
| `discord_server`, `discord_managed_server` | `discord_server_settings` | `verification_level`, `explicit_content_filter` and `default_message_notifications` are names (`medium`, `members_without_roles`, `only_mentions`). `icon_data_uri` and `splash_data_uri` become `icon` and `splash`; `icon_url` and `splash_url` are not supported. Remove `region` and `owner_id`. This provider cannot create or delete servers: destroying `discord_server_settings` leaves the server in place. |
| `discord_system_channel` | `discord_server_settings` | Keep `system_channel_id` and `system_channel_flags`. |
| `discord_server_onboarding` | `discord_onboarding` | `mode` is `default` or `advanced`. The `prompt` blocks become `prompts` and their `option` blocks `options`; `type` is `multiple_choice` or `dropdown`. |
| `discord_server_widget` | `discord_server_widget` | None. |
| `discord_auto_moderation_rule` | `discord_auto_moderation_rule` | `event_type`, `trigger_type`, `presets` and the action `type` are names (`message_send`, `keyword`, `profanity`, `block_message`). `exempt_roles` and `exempt_channels` become `exempt_role_ids` and `exempt_channel_ids`. `trigger_metadata` is an attribute (`trigger_metadata = { ... }`), and `actions` a list whose `metadata` fields are set on the action itself. `enabled` defaults to `false` here and to `true` in Lucky3028/discord, so set `enabled = true`. |
| `discord_role_connection_metadata` | `discord_application_role_connection_metadata` | The `metadata` blocks become `records`, and `type` is a name such as `integer_greater_than_or_equal`. |

A server managed by both `discord_server` and `discord_system_channel` moves to two `discord_server_settings`
resources. Each manages only the settings it configures, so they do not conflict. To combine them, move one and drop
the other from state without changing the server:

```terraform
removed {
  from = discord_system_channel.main

  lifecycle {
    destroy = false
  }
}
```

Then set `system_channel_id` and `system_channel_flags` on the remaining `discord_server_settings`.

## 4. Review the plan and apply

Run `terraform plan`. Each moved resource shows as `has moved to`, and resources with an unchanged name show no
change. Any other change comes from configuration that does not yet match Discord: compare it with the table above
before applying.

Run `terraform apply`, even when the plan has no changes, to record the moves. The state then no longer refers to
Lucky3028/discord, and the `moved` blocks can be removed once every workspace that uses the configuration has applied
them.

## Member roles

Lucky3028/discord's `discord_member_roles` only adds and removes the roles it lists, keeping roles granted in other
ways. This provider's `discord_member_roles` sets the member's complete list of roles: roles missing from `role_ids`
are revoked, including roles granted by moderators or bots, except roles managed by Discord or an integration.

The move keeps the roles listed with `has_role = true`, and the refresh replaces them with every role the member has.
The plan then shows, as an update to `role_ids`, each role the configuration would revoke. Review it before
applying:

- To keep a role, add it to `role_ids`.
- Roles listed with `has_role = false` are revoked by leaving them out of `role_ids`.

To keep Lucky3028/discord's behavior instead, manage each role with `discord_member_role`, which grants one role and
leaves the member's other roles alone. Drop `discord_member_roles` from state without revoking anything, and import
each role (Terraform 1.7 or later):

```terraform
removed {
  from = discord_member_roles.alice

  lifecycle {
    destroy = false
  }
}

import {
  to = discord_member_role.alice_moderator
  id = "${var.server_id}/${var.alice_id}/${discord_role.moderator.id}"
}

resource "discord_member_role" "alice_moderator" {
  server_id = var.server_id
  user_id   = var.alice_id
  role_id   = discord_role.moderator.id
}
```

## Data sources

Data sources have no state to move. Replace them with this provider's equivalents:

| Lucky3028/discord | This provider |
| --- | --- |
| `discord_color` | The `provider::discord::color` function, or the `discord_color` data source. |
| `discord_local_image` | Terraform's `filebase64` function: `"data:image/png;base64,${filebase64("icon.png")}"`. |
| `discord_member` | `discord_member`. |
| `discord_permission` | The `provider::discord::permissions` function, or the `discord_permissions` data source, which take permission names. |
| `discord_role` | `discord_role`. |
| `discord_server` | `discord_server`. |
| `discord_system_channel` | The `system_channel_id` attribute of `discord_server`. |

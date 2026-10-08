# A weekly community call in a stage channel.
resource "discord_scheduled_event" "community_call" {
  server_id            = var.server_id
  name                 = "Community call"
  description          = "Roadmap updates and open questions."
  entity_type          = "stage_instance"
  channel_id           = discord_stage_channel.town_hall.id
  scheduled_start_time = "2030-01-08T18:00:00Z"

  recurrence_rule = {
    frequency  = "weekly"
    by_weekday = ["tuesday"]
  }
}

# A meetup outside Discord. With Terraform 1.11 or later, image_wo keeps the
# cover image out of state; bump image_wo_version to upload a new one.
resource "discord_scheduled_event" "meetup" {
  server_id            = var.server_id
  name                 = "Meetup"
  entity_type          = "external"
  location             = "https://example.com/meetup"
  scheduled_start_time = "2030-03-01T17:00:00Z"
  scheduled_end_time   = "2030-03-01T20:00:00Z"
  image_wo             = "data:image/png;base64,${filebase64("${path.module}/cover.png")}"
  image_wo_version     = 1
}

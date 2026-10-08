package discord

import (
	"context"
	"net/http"
	"net/url"
)

// Discord documents no audit log reason for the guild template endpoints.

// ListGuildTemplates lists a guild's templates. Discord has no endpoint that
// reads one of a guild's templates by code for the guild.
func (c *Client) ListGuildTemplates(ctx context.Context, guildID string) ([]GuildTemplate, error) {
	var templates []GuildTemplate
	return templates, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/templates", nil, &templates)
}

// CreateGuildTemplate creates a template from a guild's current state.
func (c *Client) CreateGuildTemplate(ctx context.Context, guildID string, p Payload) (*GuildTemplate, error) {
	var t GuildTemplate
	return &t, c.do(ctx, http.MethodPost, "/guilds/"+guildID+"/templates", p, &t)
}

// ModifyGuildTemplate updates a template's name and description.
func (c *Client) ModifyGuildTemplate(ctx context.Context, guildID, code string, p Payload) (*GuildTemplate, error) {
	var t GuildTemplate
	return &t, c.do(ctx, http.MethodPatch, "/guilds/"+guildID+"/templates/"+url.PathEscape(code), p, &t)
}

// SyncGuildTemplate updates a template's snapshot to the guild's current
// state.
func (c *Client) SyncGuildTemplate(ctx context.Context, guildID, code string) (*GuildTemplate, error) {
	var t GuildTemplate
	return &t, c.do(ctx, http.MethodPut, "/guilds/"+guildID+"/templates/"+url.PathEscape(code), nil, &t)
}

// DeleteGuildTemplate deletes a template.
func (c *Client) DeleteGuildTemplate(ctx context.Context, guildID, code string) error {
	return c.do(ctx, http.MethodDelete, "/guilds/"+guildID+"/templates/"+url.PathEscape(code), nil, nil)
}

package discord

import (
	"context"
	"net/http"
)

// ApplicationID returns the ID of the bot's application. It is fetched once
// and cached, since every request made with the bot token acts for the same
// application.
func (c *Client) ApplicationID(ctx context.Context) (string, error) {
	c.appMu.Lock()
	defer c.appMu.Unlock()
	if c.applicationID != "" {
		return c.applicationID, nil
	}
	app, err := c.GetCurrentApplication(ctx)
	if err != nil {
		return "", err
	}
	c.applicationID = app.ID
	return app.ID, nil
}

// The command endpoints take an empty guildID for global commands. None of
// them accept an audit log reason.

// GetApplicationCommand fetches a global or guild command.
func (c *Client) GetApplicationCommand(ctx context.Context, applicationID, guildID, commandID string) (*ApplicationCommand, error) {
	var cmd ApplicationCommand
	if guildID == "" {
		return &cmd, c.do(ctx, http.MethodGet, "/applications/"+applicationID+"/commands/"+commandID, nil, &cmd)
	}
	return &cmd, c.do(ctx, http.MethodGet, "/applications/"+applicationID+"/guilds/"+guildID+"/commands/"+commandID, nil, &cmd)
}

// CreateApplicationCommand creates a global or guild command. Discord treats
// it as an upsert: a command of the same type and name in the same scope is
// overwritten and keeps its ID.
func (c *Client) CreateApplicationCommand(ctx context.Context, applicationID, guildID string, p Payload) (*ApplicationCommand, error) {
	var cmd ApplicationCommand
	if guildID == "" {
		return &cmd, c.do(ctx, http.MethodPost, "/applications/"+applicationID+"/commands", p, &cmd)
	}
	return &cmd, c.do(ctx, http.MethodPost, "/applications/"+applicationID+"/guilds/"+guildID+"/commands", p, &cmd)
}

// EditApplicationCommand updates a global or guild command. Each field sent
// replaces the current value entirely.
func (c *Client) EditApplicationCommand(ctx context.Context, applicationID, guildID, commandID string, p Payload) (*ApplicationCommand, error) {
	var cmd ApplicationCommand
	if guildID == "" {
		return &cmd, c.do(ctx, http.MethodPatch, "/applications/"+applicationID+"/commands/"+commandID, p, &cmd)
	}
	return &cmd, c.do(ctx, http.MethodPatch, "/applications/"+applicationID+"/guilds/"+guildID+"/commands/"+commandID, p, &cmd)
}

// DeleteApplicationCommand deletes a global or guild command.
func (c *Client) DeleteApplicationCommand(ctx context.Context, applicationID, guildID, commandID string) error {
	if guildID == "" {
		return c.do(ctx, http.MethodDelete, "/applications/"+applicationID+"/commands/"+commandID, nil, nil)
	}
	return c.do(ctx, http.MethodDelete, "/applications/"+applicationID+"/guilds/"+guildID+"/commands/"+commandID, nil, nil)
}

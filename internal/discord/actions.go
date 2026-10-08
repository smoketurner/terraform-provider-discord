package discord

import (
	"context"
	"net/http"
	"strconv"
	"strings"
)

// The endpoints in this file make one-off changes that the provider exposes
// as Terraform actions rather than resources.

// CrosspostMessage publishes a message in an announcement channel to the
// channels following it.
func (c *Client) CrosspostMessage(ctx context.Context, channelID, messageID string) (*Message, error) {
	var m Message
	return &m, c.do(ctx, http.MethodPost, "/channels/"+channelID+"/messages/"+messageID+"/crosspost", nil, &m)
}

// BulkDeleteMessages deletes 2 to 100 messages that are less than two weeks
// old.
func (c *Client) BulkDeleteMessages(ctx context.Context, channelID string, messageIDs []string) error {
	return c.doAudited(ctx, http.MethodPost, "/channels/"+channelID+"/messages/bulk-delete", Payload{"messages": messageIDs}, nil)
}

// EndPoll ends a poll the bot posted.
func (c *Client) EndPoll(ctx context.Context, channelID, messageID string) (*Message, error) {
	var m Message
	return &m, c.do(ctx, http.MethodPost, "/channels/"+channelID+"/polls/"+messageID+"/expire", nil, &m)
}

// PreviewPrune returns how many members a prune with the same days and
// included roles would remove. Zero days uses Discord's default.
func (c *Client) PreviewPrune(ctx context.Context, guildID string, days int64, includeRoles []string) (*PruneResult, error) {
	path := "/guilds/" + guildID + "/prune?days=" + strconv.FormatInt(days, 10)
	if len(includeRoles) > 0 {
		path += "&include_roles=" + strings.Join(includeRoles, ",")
	}
	var r PruneResult
	return &r, c.do(ctx, http.MethodGet, path, nil, &r)
}

// PruneMembers removes members who have been inactive for the given number
// of days.
func (c *Client) PruneMembers(ctx context.Context, guildID string, p Payload) (*PruneResult, error) {
	var r PruneResult
	return &r, c.doAudited(ctx, http.MethodPost, "/guilds/"+guildID+"/prune", p, &r)
}

// BulkBan bans up to 200 users from a guild.
func (c *Client) BulkBan(ctx context.Context, guildID string, p Payload) (*BulkBanResult, error) {
	var r BulkBanResult
	return &r, c.doAudited(ctx, http.MethodPost, "/guilds/"+guildID+"/bulk-ban", p, &r)
}

// SetVoiceChannelStatus sets the status shown on a voice channel, or clears
// it when status is nil. Discord does not return the status on the channel
// object.
func (c *Client) SetVoiceChannelStatus(ctx context.Context, channelID string, status *string) error {
	return c.doAudited(ctx, http.MethodPut, "/channels/"+channelID+"/voice-status", Payload{"status": status}, nil)
}

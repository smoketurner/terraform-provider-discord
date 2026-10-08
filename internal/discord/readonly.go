package discord

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// ListVoiceRegions lists the voice regions available to every guild.
func (c *Client) ListVoiceRegions(ctx context.Context) ([]VoiceRegion, error) {
	var regions []VoiceRegion
	return regions, c.do(ctx, http.MethodGet, "/voice/regions", nil, &regions)
}

// ListGuildVoiceRegions lists the voice regions a guild can use, including
// VIP regions when the guild has them.
func (c *Client) ListGuildVoiceRegions(ctx context.Context, guildID string) ([]VoiceRegion, error) {
	var regions []VoiceRegion
	return regions, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/regions", nil, &regions)
}

// GetGuildPreview fetches a guild's preview. The bot must be a member unless
// the guild is discoverable.
func (c *Client) GetGuildPreview(ctx context.Context, guildID string) (*GuildPreview, error) {
	var p GuildPreview
	return &p, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/preview", nil, &p)
}

// GetGuildVanityURL fetches a guild's vanity invite and its use count.
func (c *Client) GetGuildVanityURL(ctx context.Context, guildID string) (*VanityURL, error) {
	var v VanityURL
	return &v, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/vanity-url", nil, &v)
}

// GetGuildWidget fetches the public widget data of a guild. Discord refuses
// it when the widget is disabled.
func (c *Client) GetGuildWidget(ctx context.Context, guildID string) (*GuildWidget, error) {
	var w GuildWidget
	return &w, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/widget.json", nil, &w)
}

// AuditLogQuery filters a page of a guild's audit log. Empty and zero fields
// are not sent.
type AuditLogQuery struct {
	UserID     string
	ActionType int64
	Before     string
	After      string
	Limit      int
}

// GetGuildAuditLog fetches one page of a guild's audit log. Entries are
// newest first, or oldest first when After is set.
func (c *Client) GetGuildAuditLog(ctx context.Context, guildID string, q AuditLogQuery) (*AuditLog, error) {
	path := "/guilds/" + guildID + "/audit-logs?limit=" + strconv.Itoa(q.Limit)
	if q.UserID != "" {
		path += "&user_id=" + q.UserID
	}
	if q.ActionType != 0 {
		path += "&action_type=" + strconv.FormatInt(q.ActionType, 10)
	}
	if q.Before != "" {
		path += "&before=" + q.Before
	}
	if q.After != "" {
		path += "&after=" + q.After
	}
	var l AuditLog
	return &l, c.do(ctx, http.MethodGet, path, nil, &l)
}

// GetInvite resolves an invite code, with approximate member counts.
func (c *Client) GetInvite(ctx context.Context, code, scheduledEventID string) (*Invite, error) {
	path := "/invites/" + url.PathEscape(code) + "?with_counts=true"
	if scheduledEventID != "" {
		path += "&guild_scheduled_event_id=" + scheduledEventID
	}
	var i Invite
	return &i, c.do(ctx, http.MethodGet, path, nil, &i)
}

// ListPins fetches up to 50 of a channel's pins pinned before the given
// ISO 8601 timestamp, or the most recent ones when before is empty.
func (c *Client) ListPins(ctx context.Context, channelID, before string) (*MessagePins, error) {
	path := "/channels/" + channelID + "/messages/pins?limit=50"
	if before != "" {
		path += "&before=" + url.QueryEscape(before)
	}
	var p MessagePins
	return &p, c.do(ctx, http.MethodGet, path, nil, &p)
}

// GetRoleMemberCounts returns the number of members with each role of a
// guild, except @everyone.
func (c *Client) GetRoleMemberCounts(ctx context.Context, guildID string) (map[string]int64, error) {
	var counts map[string]int64
	return counts, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/roles/member-counts", nil, &counts)
}

// GetSticker fetches a standard or guild sticker by ID.
func (c *Client) GetSticker(ctx context.Context, stickerID string) (*AnySticker, error) {
	var s AnySticker
	return &s, c.do(ctx, http.MethodGet, "/stickers/"+stickerID, nil, &s)
}

// ListStickerPacks lists the standard sticker packs.
func (c *Client) ListStickerPacks(ctx context.Context) ([]StickerPack, error) {
	var p StickerPacks
	if err := c.do(ctx, http.MethodGet, "/sticker-packs", nil, &p); err != nil {
		return nil, err
	}
	return p.StickerPacks, nil
}

// GetStickerPack fetches a standard sticker pack.
func (c *Client) GetStickerPack(ctx context.Context, packID string) (*StickerPack, error) {
	var p StickerPack
	return &p, c.do(ctx, http.MethodGet, "/sticker-packs/"+packID, nil, &p)
}

// ListDefaultSoundboardSounds lists the soundboard sounds every user can
// play.
func (c *Client) ListDefaultSoundboardSounds(ctx context.Context) ([]SoundboardSound, error) {
	var sounds []SoundboardSound
	return sounds, c.do(ctx, http.MethodGet, "/soundboard-default-sounds", nil, &sounds)
}

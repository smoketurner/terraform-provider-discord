package discord

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// MaxPageSize is the largest page List Guild Members and Get Guild Bans
// return.
const MaxPageSize = 1000

// pageByUser fetches pages ordered by user ID, passing the last ID of each
// page as the next page's "after", until a short page or maxItems items (0 for
// all) are read.
func pageByUser[T any](maxItems int, userID func(T) string, fetch func(limit int, after string) ([]T, error)) ([]T, error) {
	all := []T{}
	after := "0"
	for {
		limit := MaxPageSize
		if maxItems > 0 {
			limit = min(limit, maxItems-len(all))
		}
		page, err := fetch(limit, after)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < limit || (maxItems > 0 && len(all) >= maxItems) {
			return all, nil
		}
		after = userID(page[len(page)-1])
	}
}

// ListMembers lists up to maxItems members of a guild (0 for all) in ascending
// order of user ID. It requires the GUILD_MEMBERS privileged intent.
func (c *Client) ListMembers(ctx context.Context, guildID string, maxItems int) ([]Member, error) {
	return pageByUser(maxItems, func(m Member) string { return m.User.ID }, func(limit int, after string) ([]Member, error) {
		var page []Member
		path := "/guilds/" + guildID + "/members?limit=" + strconv.Itoa(limit) + "&after=" + after
		return page, c.do(ctx, http.MethodGet, path, nil, &page)
	})
}

// ListBans lists up to maxItems bans of a guild (0 for all) in ascending order of
// user ID.
func (c *Client) ListBans(ctx context.Context, guildID string, maxItems int) ([]Ban, error) {
	return pageByUser(maxItems, func(b Ban) string { return b.User.ID }, func(limit int, after string) ([]Ban, error) {
		var page []Ban
		path := "/guilds/" + guildID + "/bans?limit=" + strconv.Itoa(limit) + "&after=" + after
		return page, c.do(ctx, http.MethodGet, path, nil, &page)
	})
}

// ListEmojis lists the custom emojis of a guild.
func (c *Client) ListEmojis(ctx context.Context, guildID string) ([]Emoji, error) {
	var emojis []Emoji
	return emojis, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/emojis", nil, &emojis)
}

// ListStickers lists the custom stickers of a guild.
func (c *Client) ListStickers(ctx context.Context, guildID string) ([]Sticker, error) {
	var stickers []Sticker
	return stickers, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/stickers", nil, &stickers)
}

// ListSoundboardSounds lists the soundboard sounds of a guild.
func (c *Client) ListSoundboardSounds(ctx context.Context, guildID string) ([]SoundboardSound, error) {
	var resp struct {
		Items []SoundboardSound `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/soundboard-sounds", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

// ListGuildWebhooks lists the webhooks of every channel in a guild.
func (c *Client) ListGuildWebhooks(ctx context.Context, guildID string) ([]Webhook, error) {
	var webhooks []Webhook
	return webhooks, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/webhooks", nil, &webhooks)
}

// ListGuildInvites lists the invites of a guild. Discord includes their
// metadata only when the bot has the MANAGE_GUILD permission.
func (c *Client) ListGuildInvites(ctx context.Context, guildID string) ([]Invite, error) {
	var invites []Invite
	return invites, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/invites", nil, &invites)
}

// ListScheduledEvents lists the scheduled events of a guild.
func (c *Client) ListScheduledEvents(ctx context.Context, guildID string) ([]ScheduledEvent, error) {
	var events []ScheduledEvent
	return events, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/scheduled-events", nil, &events)
}

// ListAutoModerationRules lists the AutoMod rules of a guild.
func (c *Client) ListAutoModerationRules(ctx context.Context, guildID string) ([]AutoModerationRule, error) {
	var rules []AutoModerationRule
	return rules, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/auto-moderation/rules", nil, &rules)
}

// ListActiveThreads lists the active threads of a guild.
func (c *Client) ListActiveThreads(ctx context.Context, guildID string) ([]Thread, error) {
	var resp struct {
		Threads []Thread `json:"threads"`
	}
	if err := c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/threads/active", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Threads, nil
}

// ListIntegrations lists up to 50 integrations of a guild; Discord does not
// return more.
func (c *Client) ListIntegrations(ctx context.Context, guildID string) ([]Integration, error) {
	var integrations []Integration
	return integrations, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/integrations", nil, &integrations)
}

// ListChannelWebhooks lists the webhooks of a channel.
func (c *Client) ListChannelWebhooks(ctx context.Context, channelID string) ([]Webhook, error) {
	var webhooks []Webhook
	return webhooks, c.do(ctx, http.MethodGet, "/channels/"+channelID+"/webhooks", nil, &webhooks)
}

// ArchivedThreadsPageSize is the largest page List Public Archived Threads
// and List Private Archived Threads return.
const ArchivedThreadsPageSize = 100

// ListArchivedThreads lists the archived public or private threads of a
// channel, most recently archived first, following has_more with the archive
// timestamp of the last thread of each page.
func (c *Client) ListArchivedThreads(ctx context.Context, channelID string, private bool) ([]Thread, error) {
	all := []Thread{}
	before := ""
	for {
		var page struct {
			Threads []Thread `json:"threads"`
			HasMore bool     `json:"has_more"`
		}
		query := "?limit=" + strconv.Itoa(ArchivedThreadsPageSize)
		if before != "" {
			query += "&before=" + url.QueryEscape(before)
		}
		var err error
		if private {
			err = c.do(ctx, http.MethodGet, "/channels/"+channelID+"/threads/archived/private"+query, nil, &page)
		} else {
			err = c.do(ctx, http.MethodGet, "/channels/"+channelID+"/threads/archived/public"+query, nil, &page)
		}
		if err != nil {
			return nil, err
		}
		all = append(all, page.Threads...)
		if !page.HasMore || len(page.Threads) == 0 {
			return all, nil
		}
		last := page.Threads[len(page.Threads)-1].ThreadMetadata
		if last == nil || last.ArchiveTimestamp == nil {
			return all, nil
		}
		before = *last.ArchiveTimestamp
	}
}

// ListApplicationCommands lists the global commands of an application, or its
// commands in a guild when guildID is set.
func (c *Client) ListApplicationCommands(ctx context.Context, applicationID, guildID string) ([]ApplicationCommand, error) {
	var commands []ApplicationCommand
	if guildID == "" {
		return commands, c.do(ctx, http.MethodGet, "/applications/"+applicationID+"/commands", nil, &commands)
	}
	return commands, c.do(ctx, http.MethodGet, "/applications/"+applicationID+"/guilds/"+guildID+"/commands", nil, &commands)
}

// ListApplicationEmojis lists the emojis of an application.
func (c *Client) ListApplicationEmojis(ctx context.Context, applicationID string) ([]Emoji, error) {
	var resp struct {
		Items []Emoji `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/applications/"+applicationID+"/emojis", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

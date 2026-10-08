package discord

import (
	"context"
	"net/http"
	"net/url"
)

// Payload is a JSON request body. Only the keys present are sent, and a nil
// value clears the field on Discord's side.
type Payload map[string]any

// GetGuild fetches a guild.
func (c *Client) GetGuild(ctx context.Context, guildID string) (*Guild, error) {
	var g Guild
	return &g, c.do(ctx, http.MethodGet, "/guilds/"+guildID, nil, &g)
}

// ModifyGuild updates guild settings.
func (c *Client) ModifyGuild(ctx context.Context, guildID string, p Payload) (*Guild, error) {
	var g Guild
	return &g, c.doAudited(ctx, http.MethodPatch, "/guilds/"+guildID, p, &g)
}

// ListRoles lists the roles of a guild.
func (c *Client) ListRoles(ctx context.Context, guildID string) ([]Role, error) {
	var roles []Role
	return roles, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/roles", nil, &roles)
}

// GetRole fetches a single guild role.
func (c *Client) GetRole(ctx context.Context, guildID, roleID string) (*Role, error) {
	var r Role
	return &r, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/roles/"+roleID, nil, &r)
}

// CreateRole creates a guild role.
func (c *Client) CreateRole(ctx context.Context, guildID string, p Payload) (*Role, error) {
	var r Role
	return &r, c.doAudited(ctx, http.MethodPost, "/guilds/"+guildID+"/roles", p, &r)
}

// ModifyRole updates a guild role.
func (c *Client) ModifyRole(ctx context.Context, guildID, roleID string, p Payload) (*Role, error) {
	var r Role
	return &r, c.doAudited(ctx, http.MethodPatch, "/guilds/"+guildID+"/roles/"+roleID, p, &r)
}

// DeleteRole deletes a guild role.
func (c *Client) DeleteRole(ctx context.Context, guildID, roleID string) error {
	return c.doAudited(ctx, http.MethodDelete, "/guilds/"+guildID+"/roles/"+roleID, nil, nil)
}

// ModifyRolePositions moves several roles in a single request.
func (c *Client) ModifyRolePositions(ctx context.Context, guildID string, updates []PositionUpdate) error {
	return c.doAudited(ctx, http.MethodPatch, "/guilds/"+guildID+"/roles", updates, nil)
}

// ListChannels lists the channels of a guild.
func (c *Client) ListChannels(ctx context.Context, guildID string) ([]Channel, error) {
	var channels []Channel
	return channels, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/channels", nil, &channels)
}

// GetChannel fetches a channel.
func (c *Client) GetChannel(ctx context.Context, channelID string) (*Channel, error) {
	var ch Channel
	return &ch, c.do(ctx, http.MethodGet, "/channels/"+channelID, nil, &ch)
}

// CreateChannel creates a guild channel.
func (c *Client) CreateChannel(ctx context.Context, guildID string, p Payload) (*Channel, error) {
	var ch Channel
	return &ch, c.doAudited(ctx, http.MethodPost, "/guilds/"+guildID+"/channels", p, &ch)
}

// ModifyChannel updates a channel.
func (c *Client) ModifyChannel(ctx context.Context, channelID string, p Payload) (*Channel, error) {
	var ch Channel
	return &ch, c.doAudited(ctx, http.MethodPatch, "/channels/"+channelID, p, &ch)
}

// DeleteChannel deletes a channel.
func (c *Client) DeleteChannel(ctx context.Context, channelID string) error {
	return c.doAudited(ctx, http.MethodDelete, "/channels/"+channelID, nil, nil)
}

// GetThread fetches a thread. Threads are channels, but Get Guild Channels
// does not list them.
func (c *Client) GetThread(ctx context.Context, threadID string) (*Thread, error) {
	var t Thread
	return &t, c.do(ctx, http.MethodGet, "/channels/"+threadID, nil, &t)
}

// StartThread starts a thread that is not attached to a message. In a forum
// or media channel the payload carries the post's starter message.
func (c *Client) StartThread(ctx context.Context, channelID string, p Payload) (*Thread, error) {
	var t Thread
	return &t, c.doAudited(ctx, http.MethodPost, "/channels/"+channelID+"/threads", p, &t)
}

// StartThreadFromMessage starts a thread on an existing message. The thread
// has the message's ID.
func (c *Client) StartThreadFromMessage(ctx context.Context, channelID, messageID string, p Payload) (*Thread, error) {
	var t Thread
	return &t, c.doAudited(ctx, http.MethodPost, "/channels/"+channelID+"/messages/"+messageID+"/threads", p, &t)
}

// ModifyThread updates a thread.
func (c *Client) ModifyThread(ctx context.Context, threadID string, p Payload) (*Thread, error) {
	var t Thread
	return &t, c.doAudited(ctx, http.MethodPatch, "/channels/"+threadID, p, &t)
}

// ModifyChannelPositions moves several channels in a single request.
func (c *Client) ModifyChannelPositions(ctx context.Context, guildID string, updates []PositionUpdate) error {
	return c.do(ctx, http.MethodPatch, "/guilds/"+guildID+"/channels", updates, nil)
}

// EditChannelPermission creates or replaces a permission overwrite.
func (c *Client) EditChannelPermission(ctx context.Context, channelID string, o Overwrite) error {
	body := Payload{"type": o.Type, "allow": o.Allow, "deny": o.Deny}
	return c.doAudited(ctx, http.MethodPut, "/channels/"+channelID+"/permissions/"+o.ID, body, nil)
}

// DeleteChannelPermission removes a permission overwrite.
func (c *Client) DeleteChannelPermission(ctx context.Context, channelID, overwriteID string) error {
	return c.doAudited(ctx, http.MethodDelete, "/channels/"+channelID+"/permissions/"+overwriteID, nil, nil)
}

// GetMember fetches a guild member.
func (c *Client) GetMember(ctx context.Context, guildID, userID string) (*Member, error) {
	var m Member
	return &m, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/members/"+userID, nil, &m)
}

// SearchMembers returns members whose username or nickname starts with query.
func (c *Client) SearchMembers(ctx context.Context, guildID, query string) ([]Member, error) {
	var members []Member
	path := "/guilds/" + guildID + "/members/search?limit=1000&query=" + url.QueryEscape(query)
	return members, c.do(ctx, http.MethodGet, path, nil, &members)
}

// ModifyMember updates a guild member's nickname, roles or timeout.
func (c *Client) ModifyMember(ctx context.Context, guildID, userID string, p Payload) (*Member, error) {
	var m Member
	return &m, c.doAudited(ctx, http.MethodPatch, "/guilds/"+guildID+"/members/"+userID, p, &m)
}

// AddMemberRole grants a role to a member.
func (c *Client) AddMemberRole(ctx context.Context, guildID, userID, roleID string) error {
	return c.doAudited(ctx, http.MethodPut, "/guilds/"+guildID+"/members/"+userID+"/roles/"+roleID, nil, nil)
}

// RemoveMemberRole revokes a role from a member.
func (c *Client) RemoveMemberRole(ctx context.Context, guildID, userID, roleID string) error {
	return c.doAudited(ctx, http.MethodDelete, "/guilds/"+guildID+"/members/"+userID+"/roles/"+roleID, nil, nil)
}

// CreateWebhook creates a channel webhook.
func (c *Client) CreateWebhook(ctx context.Context, channelID string, p Payload) (*Webhook, error) {
	var w Webhook
	return &w, c.doAudited(ctx, http.MethodPost, "/channels/"+channelID+"/webhooks", p, &w)
}

// GetWebhook fetches a webhook.
func (c *Client) GetWebhook(ctx context.Context, webhookID string) (*Webhook, error) {
	var w Webhook
	return &w, c.do(ctx, http.MethodGet, "/webhooks/"+webhookID, nil, &w)
}

// ModifyWebhook updates a webhook.
func (c *Client) ModifyWebhook(ctx context.Context, webhookID string, p Payload) (*Webhook, error) {
	var w Webhook
	return &w, c.doAudited(ctx, http.MethodPatch, "/webhooks/"+webhookID, p, &w)
}

// DeleteWebhook deletes a webhook.
func (c *Client) DeleteWebhook(ctx context.Context, webhookID string) error {
	return c.doAudited(ctx, http.MethodDelete, "/webhooks/"+webhookID, nil, nil)
}

// CreateInvite creates a channel invite.
func (c *Client) CreateInvite(ctx context.Context, channelID string, p Payload) (*Invite, error) {
	var i Invite
	return &i, c.doAudited(ctx, http.MethodPost, "/channels/"+channelID+"/invites", p, &i)
}

// ListChannelInvites lists a channel's invites with their metadata.
func (c *Client) ListChannelInvites(ctx context.Context, channelID string) ([]Invite, error) {
	var invites []Invite
	return invites, c.do(ctx, http.MethodGet, "/channels/"+channelID+"/invites", nil, &invites)
}

// DeleteInvite revokes an invite.
func (c *Client) DeleteInvite(ctx context.Context, code string) error {
	return c.doAudited(ctx, http.MethodDelete, "/invites/"+url.PathEscape(code), nil, nil)
}

// CreateMessage posts a message to a channel.
func (c *Client) CreateMessage(ctx context.Context, channelID string, p Payload) (*Message, error) {
	var m Message
	return &m, c.do(ctx, http.MethodPost, "/channels/"+channelID+"/messages", p, &m)
}

// GetMessage fetches a message.
func (c *Client) GetMessage(ctx context.Context, channelID, messageID string) (*Message, error) {
	var m Message
	return &m, c.do(ctx, http.MethodGet, "/channels/"+channelID+"/messages/"+messageID, nil, &m)
}

// EditMessage updates a message.
func (c *Client) EditMessage(ctx context.Context, channelID, messageID string, p Payload) (*Message, error) {
	var m Message
	return &m, c.do(ctx, http.MethodPatch, "/channels/"+channelID+"/messages/"+messageID, p, &m)
}

// DeleteMessage deletes a message.
func (c *Client) DeleteMessage(ctx context.Context, channelID, messageID string) error {
	return c.doAudited(ctx, http.MethodDelete, "/channels/"+channelID+"/messages/"+messageID, nil, nil)
}

// PinMessage pins a message in its channel.
func (c *Client) PinMessage(ctx context.Context, channelID, messageID string) error {
	return c.doAudited(ctx, http.MethodPut, "/channels/"+channelID+"/messages/pins/"+messageID, nil, nil)
}

// UnpinMessage unpins a message.
func (c *Client) UnpinMessage(ctx context.Context, channelID, messageID string) error {
	return c.doAudited(ctx, http.MethodDelete, "/channels/"+channelID+"/messages/pins/"+messageID, nil, nil)
}

// GetEmoji fetches a custom guild emoji.
func (c *Client) GetEmoji(ctx context.Context, guildID, emojiID string) (*Emoji, error) {
	var e Emoji
	return &e, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/emojis/"+emojiID, nil, &e)
}

// CreateEmoji uploads a custom guild emoji.
func (c *Client) CreateEmoji(ctx context.Context, guildID string, p Payload) (*Emoji, error) {
	var e Emoji
	return &e, c.doAudited(ctx, http.MethodPost, "/guilds/"+guildID+"/emojis", p, &e)
}

// ModifyEmoji updates a custom guild emoji.
func (c *Client) ModifyEmoji(ctx context.Context, guildID, emojiID string, p Payload) (*Emoji, error) {
	var e Emoji
	return &e, c.doAudited(ctx, http.MethodPatch, "/guilds/"+guildID+"/emojis/"+emojiID, p, &e)
}

// DeleteEmoji deletes a custom guild emoji.
func (c *Client) DeleteEmoji(ctx context.Context, guildID, emojiID string) error {
	return c.doAudited(ctx, http.MethodDelete, "/guilds/"+guildID+"/emojis/"+emojiID, nil, nil)
}

// GetAutoModerationRule fetches an AutoMod rule.
func (c *Client) GetAutoModerationRule(ctx context.Context, guildID, ruleID string) (*AutoModerationRule, error) {
	var r AutoModerationRule
	return &r, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/auto-moderation/rules/"+ruleID, nil, &r)
}

// CreateAutoModerationRule creates an AutoMod rule.
func (c *Client) CreateAutoModerationRule(ctx context.Context, guildID string, p Payload) (*AutoModerationRule, error) {
	var r AutoModerationRule
	return &r, c.doAudited(ctx, http.MethodPost, "/guilds/"+guildID+"/auto-moderation/rules", p, &r)
}

// ModifyAutoModerationRule updates an AutoMod rule.
func (c *Client) ModifyAutoModerationRule(ctx context.Context, guildID, ruleID string, p Payload) (*AutoModerationRule, error) {
	var r AutoModerationRule
	return &r, c.doAudited(ctx, http.MethodPatch, "/guilds/"+guildID+"/auto-moderation/rules/"+ruleID, p, &r)
}

// DeleteAutoModerationRule deletes an AutoMod rule.
func (c *Client) DeleteAutoModerationRule(ctx context.Context, guildID, ruleID string) error {
	return c.doAudited(ctx, http.MethodDelete, "/guilds/"+guildID+"/auto-moderation/rules/"+ruleID, nil, nil)
}

// GetScheduledEvent fetches a guild scheduled event.
func (c *Client) GetScheduledEvent(ctx context.Context, guildID, eventID string) (*ScheduledEvent, error) {
	var e ScheduledEvent
	return &e, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/scheduled-events/"+eventID, nil, &e)
}

// CreateScheduledEvent creates a guild scheduled event.
func (c *Client) CreateScheduledEvent(ctx context.Context, guildID string, p Payload) (*ScheduledEvent, error) {
	var e ScheduledEvent
	return &e, c.doAudited(ctx, http.MethodPost, "/guilds/"+guildID+"/scheduled-events", p, &e)
}

// ModifyScheduledEvent updates a guild scheduled event.
func (c *Client) ModifyScheduledEvent(ctx context.Context, guildID, eventID string, p Payload) (*ScheduledEvent, error) {
	var e ScheduledEvent
	return &e, c.doAudited(ctx, http.MethodPatch, "/guilds/"+guildID+"/scheduled-events/"+eventID, p, &e)
}

// DeleteScheduledEvent deletes a guild scheduled event. Unlike creating and
// updating, Discord does not document an audit log reason for it.
func (c *Client) DeleteScheduledEvent(ctx context.Context, guildID, eventID string) error {
	return c.do(ctx, http.MethodDelete, "/guilds/"+guildID+"/scheduled-events/"+eventID, nil, nil)
}

// GetStageInstance fetches the stage instance of a stage channel.
func (c *Client) GetStageInstance(ctx context.Context, channelID string) (*StageInstance, error) {
	var s StageInstance
	return &s, c.do(ctx, http.MethodGet, "/stage-instances/"+channelID, nil, &s)
}

// CreateStageInstance starts a stage instance on a stage channel.
func (c *Client) CreateStageInstance(ctx context.Context, p Payload) (*StageInstance, error) {
	var s StageInstance
	return &s, c.doAudited(ctx, http.MethodPost, "/stage-instances", p, &s)
}

// ModifyStageInstance updates the stage instance of a stage channel.
func (c *Client) ModifyStageInstance(ctx context.Context, channelID string, p Payload) (*StageInstance, error) {
	var s StageInstance
	return &s, c.doAudited(ctx, http.MethodPatch, "/stage-instances/"+channelID, p, &s)
}

// DeleteStageInstance ends the stage instance of a stage channel.
func (c *Client) DeleteStageInstance(ctx context.Context, channelID string) error {
	return c.doAudited(ctx, http.MethodDelete, "/stage-instances/"+channelID, nil, nil)
}

// GetWidgetSettings fetches a guild's widget settings.
func (c *Client) GetWidgetSettings(ctx context.Context, guildID string) (*WidgetSettings, error) {
	var w WidgetSettings
	return &w, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/widget", nil, &w)
}

// ModifyWidgetSettings updates a guild's widget settings.
func (c *Client) ModifyWidgetSettings(ctx context.Context, guildID string, p Payload) (*WidgetSettings, error) {
	var w WidgetSettings
	return &w, c.doAudited(ctx, http.MethodPatch, "/guilds/"+guildID+"/widget", p, &w)
}

// GetWelcomeScreen fetches a guild's welcome screen.
func (c *Client) GetWelcomeScreen(ctx context.Context, guildID string) (*WelcomeScreen, error) {
	var ws WelcomeScreen
	return &ws, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/welcome-screen", nil, &ws)
}

// ModifyWelcomeScreen updates a guild's welcome screen.
func (c *Client) ModifyWelcomeScreen(ctx context.Context, guildID string, p Payload) (*WelcomeScreen, error) {
	var ws WelcomeScreen
	return &ws, c.doAudited(ctx, http.MethodPatch, "/guilds/"+guildID+"/welcome-screen", p, &ws)
}

// GetOnboarding fetches a guild's onboarding configuration.
func (c *Client) GetOnboarding(ctx context.Context, guildID string) (*Onboarding, error) {
	var o Onboarding
	return &o, c.do(ctx, http.MethodGet, "/guilds/"+guildID+"/onboarding", nil, &o)
}

// ModifyOnboarding replaces the parts of a guild's onboarding configuration
// that the payload contains.
func (c *Client) ModifyOnboarding(ctx context.Context, guildID string, p Payload) (*Onboarding, error) {
	var o Onboarding
	return &o, c.doAudited(ctx, http.MethodPut, "/guilds/"+guildID+"/onboarding", p, &o)
}

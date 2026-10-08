package provider

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// readOnlyDataSource is embedded by data sources that only need the client.
type readOnlyDataSource struct{ client *discord.Client }

func (d *readOnlyDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromDataSource(req, resp)
}

func computedList(desc string, attrs map[string]schema.Attribute) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		MarkdownDescription: desc,
		Computed:            true,
		NestedObject:        schema.NestedAttributeObject{Attributes: attrs},
	}
}

func requiredSnowflake(desc string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Required: true, Validators: []validator.String{snowflakeValidator()}}
}

func int64PtrValue(v *int64) types.Int64 {
	if v == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*v)
}

// Voice regions.

type voiceRegionsDataSource struct{ readOnlyDataSource }

type voiceRegionModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	Optimal    types.Bool   `tfsdk:"optimal"`
	Deprecated types.Bool   `tfsdk:"deprecated"`
	Custom     types.Bool   `tfsdk:"custom"`
}

type voiceRegionsDataModel struct {
	ServerID types.String       `tfsdk:"server_id"`
	Regions  []voiceRegionModel `tfsdk:"regions"`
}

func newVoiceRegionsDataSource() datasource.DataSource { return &voiceRegionsDataSource{} }

func (d *voiceRegionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_voice_regions"
}

func (d *voiceRegionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the voice regions a voice or stage channel's `rtc_region` can be set to.",
		Attributes: map[string]schema.Attribute{
			"server_id": schema.StringAttribute{
				MarkdownDescription: "ID of a server to list the regions of. Unlike the global list, this includes VIP " +
					"regions when the server has them.",
				Optional:   true,
				Validators: []validator.String{snowflakeValidator()},
			},
			"regions": computedList("Voice regions.", map[string]schema.Attribute{
				"id":         computedString("Region ID, the value of `rtc_region`."),
				"name":       computedString("Region name."),
				"optimal":    computedBool("Whether this is the region closest to the bot."),
				"deprecated": computedBool("Whether the region is deprecated; avoid switching to it."),
				"custom":     computedBool("Whether this is a custom region, such as for events."),
			}),
		},
	}
}

func (d *voiceRegionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m voiceRegionsDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var regions []discord.VoiceRegion
	var err error
	if m.ServerID.IsNull() {
		regions, err = d.client.ListVoiceRegions(ctx)
	} else {
		regions, err = d.client.ListGuildVoiceRegions(ctx, m.ServerID.ValueString())
	}
	if err != nil {
		apiError(&resp.Diagnostics, "list voice regions", err)
		return
	}
	m.Regions = []voiceRegionModel{}
	for _, r := range regions {
		m.Regions = append(m.Regions, voiceRegionModel{
			ID:         types.StringValue(r.ID),
			Name:       types.StringValue(r.Name),
			Optimal:    types.BoolValue(r.Optimal),
			Deprecated: types.BoolValue(r.Deprecated),
			Custom:     types.BoolValue(r.Custom),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Server preview.

type serverPreviewDataSource struct{ readOnlyDataSource }

type previewEmojiModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	Animated types.Bool   `tfsdk:"animated"`
}

type previewStickerModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
}

type serverPreviewDataModel struct {
	ID                       types.String          `tfsdk:"id"`
	Name                     types.String          `tfsdk:"name"`
	Description              types.String          `tfsdk:"description"`
	IconHash                 types.String          `tfsdk:"icon_hash"`
	SplashHash               types.String          `tfsdk:"splash_hash"`
	DiscoverySplashHash      types.String          `tfsdk:"discovery_splash_hash"`
	Features                 types.Set             `tfsdk:"features"`
	ApproximateMemberCount   types.Int64           `tfsdk:"approximate_member_count"`
	ApproximatePresenceCount types.Int64           `tfsdk:"approximate_presence_count"`
	Emojis                   []previewEmojiModel   `tfsdk:"emojis"`
	Stickers                 []previewStickerModel `tfsdk:"stickers"`
}

func newServerPreviewDataSource() datasource.DataSource { return &serverPreviewDataSource{} }

func (d *serverPreviewDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_preview"
}

func (d *serverPreviewDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads the public preview of a server. The bot does not need to be a member of " +
			"discoverable servers.",
		Attributes: map[string]schema.Attribute{
			"id":                         requiredSnowflake("Server ID."),
			"name":                       computedString("Server name."),
			"description":                computedString("Server description."),
			"icon_hash":                  computedString("Hash of the server icon."),
			"splash_hash":                computedString("Hash of the invite splash image."),
			"discovery_splash_hash":      computedString("Hash of the discovery splash image."),
			"features":                   schema.SetAttribute{MarkdownDescription: "Enabled server features.", ElementType: types.StringType, Computed: true},
			"approximate_member_count":   computedInt("Approximate number of members."),
			"approximate_presence_count": computedInt("Approximate number of online members."),
			"emojis": computedList("Custom emojis.", map[string]schema.Attribute{
				"id":       computedString("Emoji ID."),
				"name":     computedString("Emoji name."),
				"animated": computedBool("Whether the emoji is animated."),
			}),
			"stickers": computedList("Custom stickers.", map[string]schema.Attribute{
				"id":          computedString("Sticker ID."),
				"name":        computedString("Sticker name."),
				"description": computedString("Sticker description."),
			}),
		},
	}
}

func (d *serverPreviewDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m serverPreviewDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := d.client.GetGuildPreview(ctx, m.ID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "read server preview (the server must be discoverable or have the bot as a member)", err)
		return
	}
	m.Name = types.StringValue(p.Name)
	m.Description = stringPtrValue(p.Description)
	m.IconHash = stringPtrValue(p.Icon)
	m.SplashHash = stringPtrValue(p.Splash)
	m.DiscoverySplashHash = stringPtrValue(p.DiscoverySplash)
	m.Features = stringSetValue(ctx, p.Features, &resp.Diagnostics)
	m.ApproximateMemberCount = types.Int64Value(p.ApproximateMemberCount)
	m.ApproximatePresenceCount = types.Int64Value(p.ApproximatePresenceCount)
	m.Emojis = []previewEmojiModel{}
	for _, e := range p.Emojis {
		m.Emojis = append(m.Emojis, previewEmojiModel{ID: types.StringValue(e.ID), Name: types.StringValue(e.Name), Animated: types.BoolValue(e.Animated)})
	}
	m.Stickers = []previewStickerModel{}
	for _, s := range p.Stickers {
		m.Stickers = append(m.Stickers, previewStickerModel{ID: types.StringValue(s.ID), Name: types.StringValue(s.Name), Description: stringPtrValue(s.Description)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Server vanity URL.

type serverVanityURLDataSource struct{ readOnlyDataSource }

type serverVanityURLDataModel struct {
	ServerID types.String `tfsdk:"server_id"`
	Code     types.String `tfsdk:"code"`
	URL      types.String `tfsdk:"url"`
	Uses     types.Int64  `tfsdk:"uses"`
}

func newServerVanityURLDataSource() datasource.DataSource { return &serverVanityURLDataSource{} }

func (d *serverVanityURLDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_vanity_url"
}

func (d *serverVanityURLDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a server's vanity invite and how many times it has been used. The server needs the " +
			"`VANITY_URL` feature and the bot the Manage Server permission.",
		Attributes: map[string]schema.Attribute{
			"server_id": dsServerID(),
			"code":      computedString("Vanity invite code, or null when none is set."),
			"url":       computedString("Invite URL, `https://discord.gg/<code>`, or null when no code is set."),
			"uses":      computedInt("Number of times the vanity invite has been used."),
		},
	}
}

func (d *serverVanityURLDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m serverVanityURLDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v, err := d.client.GetGuildVanityURL(ctx, m.ServerID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "read server vanity URL", err)
		return
	}
	m.Code = stringPtrValue(v.Code)
	m.URL = types.StringNull()
	if !m.Code.IsNull() {
		m.URL = types.StringValue("https://discord.gg/" + m.Code.ValueString())
	}
	m.Uses = types.Int64Value(v.Uses)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Server widget.

type serverWidgetDataSource struct{ readOnlyDataSource }

type widgetChannelModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	Position types.Int64  `tfsdk:"position"`
}

type widgetMemberModel struct {
	ID        types.String `tfsdk:"id"`
	Username  types.String `tfsdk:"username"`
	Status    types.String `tfsdk:"status"`
	AvatarURL types.String `tfsdk:"avatar_url"`
}

type serverWidgetDataModel struct {
	ServerID      types.String         `tfsdk:"server_id"`
	Name          types.String         `tfsdk:"name"`
	InstantInvite types.String         `tfsdk:"instant_invite"`
	PresenceCount types.Int64          `tfsdk:"presence_count"`
	Channels      []widgetChannelModel `tfsdk:"channels"`
	Members       []widgetMemberModel  `tfsdk:"members"`
}

func newServerWidgetDataSource() datasource.DataSource { return &serverWidgetDataSource{} }

func (d *serverWidgetDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_widget"
}

func (d *serverWidgetDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads the public widget data of a server, as shown by the server widget. The widget must " +
			"be enabled, for example with the `discord_server_widget` resource. Reading it creates an invite when the " +
			"widget has an invite channel and no invite exists yet.",
		Attributes: map[string]schema.Attribute{
			"server_id":      dsServerID(),
			"name":           computedString("Server name."),
			"instant_invite": computedString("Invite URL for the widget's invite channel, or null when none is set."),
			"presence_count": computedInt("Number of online members."),
			"channels": computedList("Voice and stage channels that @everyone can join.", map[string]schema.Attribute{
				"id":       computedString("Channel ID."),
				"name":     computedString("Channel name."),
				"position": computedInt("Sort position."),
			}),
			"members": computedList("Online members, at most 100. Discord anonymizes their IDs.", map[string]schema.Attribute{
				"id":         computedString("Anonymized member ID."),
				"username":   computedString("Username."),
				"status":     computedString("Presence status, such as `online` or `idle`."),
				"avatar_url": computedString("Avatar URL."),
			}),
		},
	}
}

func (d *serverWidgetDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m serverWidgetDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	w, err := d.client.GetGuildWidget(ctx, m.ServerID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "read server widget (the widget must be enabled)", err)
		return
	}
	m.Name = types.StringValue(w.Name)
	m.InstantInvite = stringPtrValue(w.InstantInvite)
	m.PresenceCount = types.Int64Value(w.PresenceCount)
	m.Channels = []widgetChannelModel{}
	for _, c := range w.Channels {
		m.Channels = append(m.Channels, widgetChannelModel{ID: types.StringValue(c.ID), Name: types.StringValue(c.Name), Position: types.Int64Value(c.Position)})
	}
	m.Members = []widgetMemberModel{}
	for _, u := range w.Members {
		m.Members = append(m.Members, widgetMemberModel{
			ID: types.StringValue(u.ID), Username: types.StringValue(u.Username), Status: types.StringValue(u.Status), AvatarURL: types.StringValue(u.AvatarURL),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Invite.

type inviteDataSource struct{ readOnlyDataSource }

type inviteDataModel struct {
	Code                     types.String `tfsdk:"code"`
	ScheduledEventID         types.String `tfsdk:"scheduled_event_id"`
	Type                     types.String `tfsdk:"type"`
	ServerID                 types.String `tfsdk:"server_id"`
	ServerName               types.String `tfsdk:"server_name"`
	ChannelID                types.String `tfsdk:"channel_id"`
	ChannelName              types.String `tfsdk:"channel_name"`
	InviterID                types.String `tfsdk:"inviter_id"`
	TargetType               types.String `tfsdk:"target_type"`
	TargetUserID             types.String `tfsdk:"target_user_id"`
	ExpiresAt                types.String `tfsdk:"expires_at"`
	ApproximateMemberCount   types.Int64  `tfsdk:"approximate_member_count"`
	ApproximatePresenceCount types.Int64  `tfsdk:"approximate_presence_count"`
}

var (
	inviteTypes       = enumMapping{"guild", "group_dm", "friend"}
	inviteTargetTypes = enumMapping{"", "stream", "embedded_application"}
)

func newInviteDataSource() datasource.DataSource { return &inviteDataSource{} }

func (d *inviteDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_invite"
}

func (d *inviteDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Resolves an invite code to the server and channel it leads to. Works for invites to " +
			"servers the bot is not a member of.",
		Attributes: map[string]schema.Attribute{
			"code": schema.StringAttribute{MarkdownDescription: "Invite code, the part after `https://discord.gg/`.", Required: true},
			"scheduled_event_id": schema.StringAttribute{
				MarkdownDescription: "ID of a scheduled event to resolve the invite for.",
				Optional:            true,
				Validators:          []validator.String{snowflakeValidator()},
			},
			"type":                       computedString("Invite type: " + inviteTypes.doc() + "."),
			"server_id":                  computedString("ID of the server the invite is for."),
			"server_name":                computedString("Name of the server the invite is for."),
			"channel_id":                 computedString("ID of the channel the invite is for."),
			"channel_name":               computedString("Name of the channel the invite is for."),
			"inviter_id":                 computedString("ID of the user who created the invite."),
			"target_type":                computedString("For voice channel invites, what the invite opens: " + inviteTargetTypes.doc() + "."),
			"target_user_id":             computedString("ID of the user whose stream the invite opens."),
			"expires_at":                 computedString("When the invite expires, or null if it never does."),
			"approximate_member_count":   computedInt("Approximate number of members of the server."),
			"approximate_presence_count": computedInt("Approximate number of online members of the server."),
		},
	}
}

func (d *inviteDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m inviteDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	inv, err := d.client.GetInvite(ctx, m.Code.ValueString(), m.ScheduledEventID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "resolve invite", err)
		return
	}
	m.Type = inviteTypes.name(inv.Type)
	m.ServerID, m.ServerName = types.StringNull(), types.StringNull()
	if inv.Guild != nil {
		m.ServerID, m.ServerName = types.StringValue(inv.Guild.ID), types.StringValue(inv.Guild.Name)
	}
	m.ChannelID, m.ChannelName = types.StringNull(), types.StringNull()
	if inv.Channel != nil {
		m.ChannelID, m.ChannelName = types.StringValue(inv.Channel.ID), stringPtrValue(&inv.Channel.Name)
	}
	m.InviterID = types.StringNull()
	if inv.Inviter != nil {
		m.InviterID = types.StringValue(inv.Inviter.ID)
	}
	m.TargetType = types.StringNull()
	if inv.TargetType != 0 {
		m.TargetType = inviteTargetTypes.name(inv.TargetType)
	}
	m.TargetUserID = types.StringNull()
	if inv.TargetUser != nil {
		m.TargetUserID = types.StringValue(inv.TargetUser.ID)
	}
	m.ExpiresAt = stringPtrValue(inv.ExpiresAt)
	m.ApproximateMemberCount = int64PtrValue(inv.ApproximateMemberCount)
	m.ApproximatePresenceCount = int64PtrValue(inv.ApproximatePresenceCount)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Message.

type messageDataSource struct{ readOnlyDataSource }

type messageFields struct {
	ID              types.String `tfsdk:"id"`
	Type            types.Int64  `tfsdk:"type"`
	AuthorID        types.String `tfsdk:"author_id"`
	AuthorUsername  types.String `tfsdk:"author_username"`
	Content         types.String `tfsdk:"content"`
	Timestamp       types.String `tfsdk:"timestamp"`
	EditedTimestamp types.String `tfsdk:"edited_timestamp"`
	Pinned          types.Bool   `tfsdk:"pinned"`
	Flags           types.Int64  `tfsdk:"flags"`
	WebhookID       types.String `tfsdk:"webhook_id"`
}

func messageFieldsValue(msg *discord.Message) messageFields {
	f := messageFields{
		ID:              types.StringValue(msg.ID),
		Type:            types.Int64Value(msg.Type),
		AuthorID:        types.StringNull(),
		AuthorUsername:  types.StringNull(),
		Content:         types.StringValue(msg.Content),
		Timestamp:       types.StringValue(msg.Timestamp),
		EditedTimestamp: stringPtrValue(msg.EditedTimestamp),
		Pinned:          types.BoolValue(msg.Pinned),
		Flags:           types.Int64Value(msg.Flags),
		WebhookID:       stringPtrValue(msg.WebhookID),
	}
	if msg.Author != nil {
		f.AuthorID, f.AuthorUsername = types.StringValue(msg.Author.ID), types.StringValue(msg.Author.Username)
	}
	return f
}

// messageAttributes describes messageFields. The ID is left to the caller,
// since it is an argument of discord_message and computed elsewhere.
func messageAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"type":             computedInt("[Message type](https://docs.discord.com/developers/resources/message#message-object-message-types), `0` for a default message."),
		"author_id":        computedString("ID of the author."),
		"author_username":  computedString("Username of the author."),
		"content":          computedString("Text content. Empty unless the bot has the Message Content privileged intent or wrote the message."),
		"timestamp":        computedString("When the message was sent."),
		"edited_timestamp": computedString("When the message was last edited, or null."),
		"pinned":           computedBool("Whether the message is pinned."),
		"flags":            computedInt("[Message flags](https://docs.discord.com/developers/resources/message#message-object-message-flags) bitfield."),
		"webhook_id":       computedString("ID of the webhook that sent the message, if any."),
	}
}

type messageDataModel struct {
	ChannelID types.String `tfsdk:"channel_id"`
	messageFields
}

func newMessageDataSource() datasource.DataSource { return &messageDataSource{} }

func (d *messageDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_message"
}

func (d *messageDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := messageAttributes()
	attrs["channel_id"] = requiredSnowflake("ID of the channel or thread the message is in.")
	attrs["id"] = requiredSnowflake("Message ID.")
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a message. The bot needs the View Channel and Read Message History permissions, " +
			"and Connect in voice channels.",
		Attributes: attrs,
	}
}

func (d *messageDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m messageDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	msg, err := d.client.GetMessage(ctx, m.ChannelID.ValueString(), m.ID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "read message", err)
		return
	}
	m.messageFields = messageFieldsValue(msg)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Pinned messages.

type pinnedMessagesDataSource struct{ readOnlyDataSource }

type pinnedMessageModel struct {
	PinnedAt types.String `tfsdk:"pinned_at"`
	messageFields
}

type pinnedMessagesDataModel struct {
	ChannelID types.String         `tfsdk:"channel_id"`
	Messages  []pinnedMessageModel `tfsdk:"messages"`
}

func newPinnedMessagesDataSource() datasource.DataSource { return &pinnedMessagesDataSource{} }

func (d *pinnedMessagesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_pinned_messages"
}

func (d *pinnedMessagesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := messageAttributes()
	attrs["id"] = computedString("Message ID.")
	attrs["pinned_at"] = computedString("When the message was pinned.")
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the pinned messages of a channel, most recently pinned first. The bot needs the " +
			"View Channel permission; without Read Message History, Discord returns no pins.",
		Attributes: map[string]schema.Attribute{
			"channel_id": requiredSnowflake("ID of the channel or thread."),
			"messages":   computedList("Pinned messages.", attrs),
		},
	}
}

func (d *pinnedMessagesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m pinnedMessagesDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m.Messages = []pinnedMessageModel{}
	before := ""
	for {
		page, err := d.client.ListPins(ctx, m.ChannelID.ValueString(), before)
		if err != nil {
			apiError(&resp.Diagnostics, "list pinned messages", err)
			return
		}
		for _, p := range page.Items {
			m.Messages = append(m.Messages, pinnedMessageModel{PinnedAt: types.StringValue(p.PinnedAt), messageFields: messageFieldsValue(&p.Message)})
		}
		if !page.HasMore || len(page.Items) == 0 {
			break
		}
		before = page.Items[len(page.Items)-1].PinnedAt
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Messages.

type messagesDataSource struct{ readOnlyDataSource }

type messagesDataModel struct {
	ChannelID types.String    `tfsdk:"channel_id"`
	Around    types.String    `tfsdk:"around"`
	Before    types.String    `tfsdk:"before"`
	After     types.String    `tfsdk:"after"`
	Limit     types.Int64     `tfsdk:"limit"`
	Messages  []messageFields `tfsdk:"messages"`
}

func newMessagesDataSource() datasource.DataSource { return &messagesDataSource{} }

func (d *messagesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_messages"
}

func (d *messagesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := messageAttributes()
	attrs["id"] = computedString("Message ID.")
	cursor := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: desc + " Conflicts with the other two of `around`, `before` and `after`.",
			Optional:            true,
			Validators: []validator.String{
				snowflakeValidator(),
				stringvalidator.ConflictsWith(path.MatchRoot("around"), path.MatchRoot("before"), path.MatchRoot("after")),
			},
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists messages of a channel or thread, newest first. The bot needs the View Channel " +
			"permission, and Connect in voice channels; without Read Message History, Discord returns no messages.",
		Attributes: map[string]schema.Attribute{
			"channel_id": requiredSnowflake("ID of the channel or thread."),
			"around":     cursor("Return the messages around this message ID, including it."),
			"before":     cursor("Return the messages before this message ID."),
			"after":      cursor("Return the messages after this message ID."),
			"limit": schema.Int64Attribute{
				MarkdownDescription: "Maximum number of messages to return. Defaults to 50. Discord returns at most 100 " +
					"per request, so higher limits take several requests; with `around`, the limit is at most 100.",
				Optional:   true,
				Validators: []validator.Int64{int64validator.AtLeast(1)},
			},
			"messages": computedList("Messages, newest first.", attrs),
		},
	}
}

func (d *messagesDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var m messagesDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.Around.IsNull() && !m.Around.IsUnknown() && m.Limit.ValueInt64() > maxPageLimit {
		resp.Diagnostics.AddAttributeError(path.Root("limit"), "Invalid limit",
			fmt.Sprintf("With around, limit must be at most %d: Discord cannot page around a message.", maxPageLimit))
	}
}

func (d *messagesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m messagesDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	limit := int64(50)
	if !m.Limit.IsNull() {
		limit = m.Limit.ValueInt64()
	}
	channelID := m.ChannelID.ValueString()
	var msgs []discord.Message
	var err error
	if !m.Around.IsNull() {
		msgs, err = d.client.ListMessages(ctx, channelID, m.Around.ValueString(), discord.Page{Limit: int(limit)})
	} else {
		msgs, err = fetchPages(discord.Page{Before: m.Before.ValueString(), After: m.After.ValueString()}, limit,
			func(p discord.Page) ([]discord.Message, error) { return d.client.ListMessages(ctx, channelID, "", p) },
			func(msg discord.Message) string { return msg.ID })
	}
	if err != nil {
		apiError(&resp.Diagnostics, "list messages", err)
		return
	}
	slices.SortFunc(msgs, func(a, b discord.Message) int { return compareSnowflakes(b.ID, a.ID) })
	m.Messages = make([]messageFields, 0, len(msgs))
	for _, msg := range msgs {
		m.Messages = append(m.Messages, messageFieldsValue(&msg))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Role member counts.

type roleMemberCountsDataSource struct{ readOnlyDataSource }

type roleMemberCountsDataModel struct {
	ServerID types.String `tfsdk:"server_id"`
	Counts   types.Map    `tfsdk:"counts"`
}

func newRoleMemberCountsDataSource() datasource.DataSource { return &roleMemberCountsDataSource{} }

func (d *roleMemberCountsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role_member_counts"
}

func (d *roleMemberCountsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads how many members have each role of a server.",
		Attributes: map[string]schema.Attribute{
			"server_id": dsServerID(),
			"counts": schema.MapAttribute{
				MarkdownDescription: "Number of members with each role, keyed by role ID. Excludes @everyone.",
				ElementType:         types.Int64Type,
				Computed:            true,
			},
		},
	}
}

func (d *roleMemberCountsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m roleMemberCountsDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	counts, err := d.client.GetRoleMemberCounts(ctx, m.ServerID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "read role member counts", err)
		return
	}
	if counts == nil {
		counts = map[string]int64{}
	}
	v, diags := types.MapValueFrom(ctx, types.Int64Type, maps.Clone(counts))
	resp.Diagnostics.Append(diags...)
	m.Counts = v
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Stickers and sticker packs.

type stickerModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Tags        types.String `tfsdk:"tags"`
	FormatType  types.String `tfsdk:"format_type"`
	SortValue   types.Int64  `tfsdk:"sort_value"`
}

var stickerFormatTypes = enumMapping{"", "png", "apng", "lottie", "gif"}

func stickerAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":          computedString("Sticker ID."),
		"name":        computedString("Sticker name."),
		"description": computedString("Sticker description."),
		"tags":        computedString("Autocomplete and suggestion tags."),
		"format_type": computedString("Format: " + stickerFormatTypes.doc() + "."),
		"sort_value":  computedInt("Sort order within the pack."),
	}
}

func stickerValue(s discord.StandardSticker) stickerModel {
	return stickerModel{
		ID:          types.StringValue(s.ID),
		Name:        types.StringValue(s.Name),
		Description: stringPtrValue(s.Description),
		Tags:        types.StringValue(s.Tags),
		FormatType:  stickerFormatTypes.name(s.FormatType),
		SortValue:   types.Int64Value(s.SortValue),
	}
}

type stickerPackModel struct {
	ID             types.String   `tfsdk:"id"`
	Name           types.String   `tfsdk:"name"`
	Description    types.String   `tfsdk:"description"`
	SKUID          types.String   `tfsdk:"sku_id"`
	CoverStickerID types.String   `tfsdk:"cover_sticker_id"`
	BannerAssetID  types.String   `tfsdk:"banner_asset_id"`
	Stickers       []stickerModel `tfsdk:"stickers"`
}

func stickerPackAttributes(id schema.Attribute) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":               id,
		"name":             computedString("Pack name."),
		"description":      computedString("Pack description."),
		"sku_id":           computedString("ID of the pack's SKU."),
		"cover_sticker_id": computedString("ID of the sticker shown as the pack's icon."),
		"banner_asset_id":  computedString("ID of the pack's banner image."),
		"stickers":         computedList("Stickers in the pack.", stickerAttributes()),
	}
}

func stickerPackValue(p *discord.StickerPack) stickerPackModel {
	m := stickerPackModel{
		ID:             types.StringValue(p.ID),
		Name:           types.StringValue(p.Name),
		Description:    types.StringValue(p.Description),
		SKUID:          types.StringValue(p.SKUID),
		CoverStickerID: stringPtrValue(p.CoverStickerID),
		BannerAssetID:  stringPtrValue(p.BannerAssetID),
		Stickers:       []stickerModel{},
	}
	for _, s := range p.Stickers {
		m.Stickers = append(m.Stickers, stickerValue(s))
	}
	return m
}

type stickerPacksDataSource struct{ readOnlyDataSource }

type stickerPacksDataModel struct {
	Packs []stickerPackModel `tfsdk:"packs"`
}

func newStickerPacksDataSource() datasource.DataSource { return &stickerPacksDataSource{} }

func (d *stickerPacksDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sticker_packs"
}

func (d *stickerPacksDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists Discord's standard sticker packs.",
		Attributes: map[string]schema.Attribute{
			"packs": computedList("Sticker packs.", stickerPackAttributes(computedString("Pack ID."))),
		},
	}
}

func (d *stickerPacksDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	packs, err := d.client.ListStickerPacks(ctx)
	if err != nil {
		apiError(&resp.Diagnostics, "list sticker packs", err)
		return
	}
	m := stickerPacksDataModel{Packs: []stickerPackModel{}}
	for i := range packs {
		m.Packs = append(m.Packs, stickerPackValue(&packs[i]))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

type stickerPackDataSource struct{ readOnlyDataSource }

func newStickerPackDataSource() datasource.DataSource { return &stickerPackDataSource{} }

func (d *stickerPackDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sticker_pack"
}

func (d *stickerPackDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one of Discord's standard sticker packs.",
		Attributes:          stickerPackAttributes(requiredSnowflake("Pack ID.")),
	}
}

func (d *stickerPackDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := d.client.GetStickerPack(ctx, id.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "read sticker pack", err)
		return
	}
	m := stickerPackValue(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

type stickerDataSource struct{ readOnlyDataSource }

type stickerDataModel struct {
	stickerModel
	Type      types.String `tfsdk:"type"`
	PackID    types.String `tfsdk:"pack_id"`
	ServerID  types.String `tfsdk:"server_id"`
	Available types.Bool   `tfsdk:"available"`
}

var stickerTypes = enumMapping{"", "standard", "guild"}

func newStickerDataSource() datasource.DataSource { return &stickerDataSource{} }

func (d *stickerDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sticker"
}

func (d *stickerDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := stickerAttributes()
	attrs["id"] = requiredSnowflake("Sticker ID.")
	attrs["sort_value"] = computedInt("Sort order within the pack, for standard stickers.")
	attrs["type"] = computedString("Sticker type: " + stickerTypes.doc() + ".")
	attrs["pack_id"] = computedString("ID of the pack of a standard sticker.")
	attrs["server_id"] = computedString("ID of the server that owns a server sticker.")
	attrs["available"] = computedBool("Whether a server sticker can be used; it can be unavailable after the server loses boosts. Null for standard stickers.")
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a standard sticker or a server sticker by ID.",
		Attributes:          attrs,
	}
}

func (d *stickerDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := d.client.GetSticker(ctx, id.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "read sticker", err)
		return
	}
	m := stickerDataModel{
		stickerModel: stickerValue(s.StandardSticker),
		Type:         stickerTypes.name(s.Type),
		PackID:       stringPtrValue(&s.PackID),
		ServerID:     stringPtrValue(s.GuildID),
		Available:    types.BoolNull(),
	}
	if s.Available != nil {
		m.Available = types.BoolValue(*s.Available)
	}
	if s.PackID == "" {
		m.SortValue = types.Int64Null()
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Default soundboard sounds.

type defaultSoundboardSoundsDataSource struct{ readOnlyDataSource }

type soundModel struct {
	ID        types.String  `tfsdk:"id"`
	Name      types.String  `tfsdk:"name"`
	Volume    types.Float64 `tfsdk:"volume"`
	EmojiID   types.String  `tfsdk:"emoji_id"`
	EmojiName types.String  `tfsdk:"emoji_name"`
}

type defaultSoundboardSoundsDataModel struct {
	Sounds []soundModel `tfsdk:"sounds"`
}

func newDefaultSoundboardSoundsDataSource() datasource.DataSource {
	return &defaultSoundboardSoundsDataSource{}
}

func (d *defaultSoundboardSoundsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_default_soundboard_sounds"
}

func (d *defaultSoundboardSoundsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists Discord's default soundboard sounds, which every user can play.",
		Attributes: map[string]schema.Attribute{
			"sounds": computedList("Default sounds.", map[string]schema.Attribute{
				"id":         computedString("Sound ID."),
				"name":       computedString("Sound name."),
				"volume":     schema.Float64Attribute{MarkdownDescription: "Volume, from 0 to 1.", Computed: true},
				"emoji_id":   computedString("ID of the sound's custom emoji."),
				"emoji_name": computedString("Unicode character of the sound's standard emoji."),
			}),
		},
	}
}

func (d *defaultSoundboardSoundsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	sounds, err := d.client.ListDefaultSoundboardSounds(ctx)
	if err != nil {
		apiError(&resp.Diagnostics, "list default soundboard sounds", err)
		return
	}
	m := defaultSoundboardSoundsDataModel{Sounds: []soundModel{}}
	for _, s := range sounds {
		m.Sounds = append(m.Sounds, soundModel{
			ID:        types.StringValue(s.SoundID),
			Name:      types.StringValue(s.Name),
			Volume:    types.Float64Value(s.Volume),
			EmojiID:   stringPtrValue(s.EmojiID),
			EmojiName: stringPtrValue(s.EmojiName),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

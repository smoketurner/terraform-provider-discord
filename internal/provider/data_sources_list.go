package provider

import (
	"cmp"
	"context"
	"maps"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// listDataSource lists one kind of server object. M is its model: the
// server_id, any filters, and the list attribute. read fills in the list.
type listDataSource[M any] struct {
	client *discord.Client
	name   string
	desc   string
	attrs  map[string]schema.Attribute
	read   func(ctx context.Context, c *discord.Client, m *M, diags *diag.Diagnostics)
}

func (d *listDataSource[M]) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.name
}

func (d *listDataSource[M]) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := maps.Clone(d.attrs)
	attrs["server_id"] = dsServerID()
	resp.Schema = schema.Schema{MarkdownDescription: d.desc, Attributes: attrs}
}

func (d *listDataSource[M]) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromDataSource(req, resp)
}

func (d *listDataSource[M]) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m M
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d.read(ctx, d.client, &m, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func computedList(desc string, attrs map[string]schema.Attribute) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		MarkdownDescription: desc,
		Computed:            true,
		NestedObject:        schema.NestedAttributeObject{Attributes: attrs},
	}
}

func computedStringSet(desc string) schema.SetAttribute {
	return schema.SetAttribute{MarkdownDescription: desc, ElementType: types.StringType, Computed: true}
}

// limitAttr is an optional cap on the number of objects a paginated list
// returns.
func limitAttr(what string) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: "Maximum number of " + what + " to return. Defaults to all of them, fetched in pages of 1000.",
		Optional:            true,
		Validators:          []validator.Int64{int64validator.AtLeast(1)},
	}
}

// listOf converts API objects to list items, returning an empty rather than
// a nil slice so an empty list is not null.
func listOf[T, U any](in []T, f func(T) U) []U {
	out := make([]U, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}
	return out
}

// Channels.

type channelsDataModel struct {
	ServerID   types.String       `tfsdk:"server_id"`
	Type       types.String       `tfsdk:"type"`
	CategoryID types.String       `tfsdk:"category_id"`
	Channels   []channelItemModel `tfsdk:"channels"`
}

type channelItemModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	Type       types.String `tfsdk:"type"`
	CategoryID types.String `tfsdk:"category_id"`
	Position   types.Int64  `tfsdk:"position"`
	Topic      types.String `tfsdk:"topic"`
	NSFW       types.Bool   `tfsdk:"nsfw"`
}

func newChannelsDataSource() datasource.DataSource {
	return &listDataSource[channelsDataModel]{
		name: "channels",
		desc: "Lists the channels of a server, optionally only those of one type or in one category, ordered by " +
			"position and then ID. Threads are not included; use `discord_threads`. Starting November 16, 2026, " +
			"Discord omits channels the bot cannot view: the bot needs the View Channel permission on a channel, or to " +
			"be connected to it if it is a voice channel, and a category is listed when any of its channels is.",
		attrs: map[string]schema.Attribute{
			"type": schema.StringAttribute{
				MarkdownDescription: "Only list channels of this type: `text`, `voice`, `category`, `announcement`, " +
					"`stage`, `forum` or `media`.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.OneOf(slices.Sorted(maps.Values(channelTypeNames))...)},
			},
			"category_id": schema.StringAttribute{
				MarkdownDescription: "Only list channels in this category.",
				Optional:            true,
				Validators:          []validator.String{snowflakeValidator()},
			},
			"channels": computedList("The channels.", map[string]schema.Attribute{
				"id":          computedString("Channel ID."),
				"name":        computedString("Channel name."),
				"type":        computedString("Channel type, or `unknown_<n>` for a type the provider does not manage."),
				"category_id": computedString("Parent category ID."),
				"position":    computedInt("Sort position."),
				"topic":       computedString("Channel topic."),
				"nsfw":        computedBool("Whether the channel is age-restricted."),
			}),
		},
		read: func(ctx context.Context, c *discord.Client, m *channelsDataModel, diags *diag.Diagnostics) {
			channels, err := c.ListChannels(ctx, m.ServerID.ValueString())
			if err != nil {
				apiError(diags, "list channels", err)
				return
			}
			channels = slices.DeleteFunc(channels, func(ch discord.Channel) bool {
				return (!m.Type.IsNull() && channelTypeName(ch.Type) != m.Type.ValueString()) ||
					(!m.CategoryID.IsNull() && (ch.ParentID == nil || *ch.ParentID != m.CategoryID.ValueString()))
			})
			slices.SortFunc(channels, func(a, b discord.Channel) int {
				return cmp.Or(cmp.Compare(a.Position, b.Position), compareSnowflakes(a.ID, b.ID))
			})
			m.Channels = listOf(channels, func(ch discord.Channel) channelItemModel {
				return channelItemModel{
					ID:         types.StringValue(ch.ID),
					Name:       types.StringValue(ch.Name),
					Type:       types.StringValue(channelTypeName(ch.Type)),
					CategoryID: stringPtrValue(ch.ParentID),
					Position:   types.Int64Value(ch.Position),
					Topic:      stringPtrValue(ch.Topic),
					NSFW:       types.BoolValue(ch.NSFW),
				}
			})
		},
	}
}

// compareSnowflakes orders IDs numerically: a longer snowflake is larger.
func compareSnowflakes(a, b string) int {
	return cmp.Or(cmp.Compare(len(a), len(b)), cmp.Compare(a, b))
}

// Roles.

type rolesDataModel struct {
	ServerID types.String    `tfsdk:"server_id"`
	Roles    []roleItemModel `tfsdk:"roles"`
}

type roleItemModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Permissions types.String `tfsdk:"permissions"`
	Color       types.Int64  `tfsdk:"color"`
	Hoist       types.Bool   `tfsdk:"hoist"`
	Mentionable types.Bool   `tfsdk:"mentionable"`
	Position    types.Int64  `tfsdk:"position"`
	Managed     types.Bool   `tfsdk:"managed"`
}

func newRolesDataSource() datasource.DataSource {
	return &listDataSource[rolesDataModel]{
		name: "roles",
		desc: "Lists the roles of a server, including `@everyone`, ordered by position and then ID.",
		attrs: map[string]schema.Attribute{
			"roles": computedList("The roles.", map[string]schema.Attribute{
				"id":          computedString("Role ID. The `@everyone` role has the server's ID."),
				"name":        computedString("Role name."),
				"permissions": computedString("Permission bitfield as a decimal string."),
				"color":       computedInt("Primary RGB color."),
				"hoist":       computedBool("Whether members are displayed separately."),
				"mentionable": computedBool("Whether the role can be mentioned by anyone."),
				"position":    computedInt("Role position."),
				"managed":     computedBool("Whether the role is managed by an integration."),
			}),
		},
		read: func(ctx context.Context, c *discord.Client, m *rolesDataModel, diags *diag.Diagnostics) {
			roles, err := c.ListRoles(ctx, m.ServerID.ValueString())
			if err != nil {
				apiError(diags, "list roles", err)
				return
			}
			slices.SortFunc(roles, func(a, b discord.Role) int {
				return cmp.Or(cmp.Compare(a.Position, b.Position), compareSnowflakes(a.ID, b.ID))
			})
			m.Roles = listOf(roles, func(r discord.Role) roleItemModel {
				color := r.Color
				if r.Colors != nil {
					color = r.Colors.PrimaryColor
				}
				return roleItemModel{
					ID:          types.StringValue(r.ID),
					Name:        types.StringValue(r.Name),
					Permissions: types.StringValue(r.Permissions),
					Color:       types.Int64Value(color),
					Hoist:       types.BoolValue(r.Hoist),
					Mentionable: types.BoolValue(r.Mentionable),
					Position:    types.Int64Value(r.Position),
					Managed:     types.BoolValue(r.Managed),
				}
			})
		},
	}
}

// Members.

type membersDataModel struct {
	ServerID types.String      `tfsdk:"server_id"`
	Limit    types.Int64       `tfsdk:"limit"`
	Members  []memberItemModel `tfsdk:"members"`
}

type memberItemModel struct {
	UserID     types.String `tfsdk:"user_id"`
	Username   types.String `tfsdk:"username"`
	GlobalName types.String `tfsdk:"global_name"`
	Nick       types.String `tfsdk:"nick"`
	Roles      types.Set    `tfsdk:"roles"`
	JoinedAt   types.String `tfsdk:"joined_at"`
	Bot        types.Bool   `tfsdk:"bot"`
}

func newMembersDataSource() datasource.DataSource {
	return &listDataSource[membersDataModel]{
		name: "members",
		desc: "Lists the members of a server in ascending order of user ID. Requires the Server Members privileged " +
			"intent to be enabled for the bot in the Discord Developer Portal.",
		attrs: map[string]schema.Attribute{
			"limit": limitAttr("members"),
			"members": computedList("The members.", map[string]schema.Attribute{
				"user_id":     computedString("User ID."),
				"username":    computedString("Unique username."),
				"global_name": computedString("Display name."),
				"nick":        computedString("Server nickname."),
				"roles":       computedStringSet("IDs of the member's roles."),
				"joined_at":   computedString("When the member joined the server."),
				"bot":         computedBool("Whether the user is a bot."),
			}),
		},
		read: func(ctx context.Context, c *discord.Client, m *membersDataModel, diags *diag.Diagnostics) {
			members, err := c.ListMembers(ctx, m.ServerID.ValueString(), int(m.Limit.ValueInt64()))
			if err != nil {
				apiError(diags, "list members (the bot needs the Server Members privileged intent)", err)
				return
			}
			m.Members = listOf(members, func(mem discord.Member) memberItemModel {
				return memberItemModel{
					UserID:     types.StringValue(mem.User.ID),
					Username:   types.StringValue(mem.User.Username),
					GlobalName: stringPtrValue(mem.User.GlobalName),
					Nick:       stringPtrValue(mem.Nick),
					Roles:      stringSetValue(ctx, mem.Roles, diags),
					JoinedAt:   types.StringValue(mem.JoinedAt),
					Bot:        types.BoolValue(mem.User.Bot),
				}
			})
		},
	}
}

// Bans.

type bansDataModel struct {
	ServerID types.String   `tfsdk:"server_id"`
	Limit    types.Int64    `tfsdk:"limit"`
	Bans     []banItemModel `tfsdk:"bans"`
}

type banItemModel struct {
	UserID   types.String `tfsdk:"user_id"`
	Username types.String `tfsdk:"username"`
	Reason   types.String `tfsdk:"reason"`
}

func newBansDataSource() datasource.DataSource {
	return &listDataSource[bansDataModel]{
		name: "bans",
		desc: "Lists the users banned from a server in ascending order of user ID. Requires the Ban Members or View " +
			"Audit Log permission.",
		attrs: map[string]schema.Attribute{
			"limit": limitAttr("bans"),
			"bans": computedList("The bans.", map[string]schema.Attribute{
				"user_id":  computedString("ID of the banned user."),
				"username": computedString("Username of the banned user."),
				"reason":   computedString("Reason for the ban."),
			}),
		},
		read: func(ctx context.Context, c *discord.Client, m *bansDataModel, diags *diag.Diagnostics) {
			bans, err := c.ListBans(ctx, m.ServerID.ValueString(), int(m.Limit.ValueInt64()))
			if err != nil {
				apiError(diags, "list bans", err)
				return
			}
			m.Bans = listOf(bans, func(b discord.Ban) banItemModel {
				return banItemModel{
					UserID:   types.StringValue(b.User.ID),
					Username: types.StringValue(b.User.Username),
					Reason:   stringPtrValue(b.Reason),
				}
			})
		},
	}
}

// Emojis.

type emojisDataModel struct {
	ServerID types.String     `tfsdk:"server_id"`
	Emojis   []emojiItemModel `tfsdk:"emojis"`
}

type emojiItemModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	Roles    types.Set    `tfsdk:"roles"`
	Managed  types.Bool   `tfsdk:"managed"`
	Animated types.Bool   `tfsdk:"animated"`
}

func newEmojisDataSource() datasource.DataSource {
	return &listDataSource[emojisDataModel]{
		name: "emojis",
		desc: "Lists the custom emojis of a server.",
		attrs: map[string]schema.Attribute{
			"emojis": computedList("The emojis.", map[string]schema.Attribute{
				"id":       computedString("Emoji ID."),
				"name":     computedString("Emoji name."),
				"roles":    computedStringSet("IDs of the roles allowed to use the emoji; empty when everyone can."),
				"managed":  computedBool("Whether the emoji is managed by an integration."),
				"animated": computedBool("Whether the emoji is animated."),
			}),
		},
		read: func(ctx context.Context, c *discord.Client, m *emojisDataModel, diags *diag.Diagnostics) {
			emojis, err := c.ListEmojis(ctx, m.ServerID.ValueString())
			if err != nil {
				apiError(diags, "list emojis", err)
				return
			}
			m.Emojis = listOf(emojis, func(e discord.Emoji) emojiItemModel {
				return emojiItemModel{
					ID:       types.StringValue(e.ID),
					Name:     types.StringValue(e.Name),
					Roles:    stringSetValue(ctx, e.Roles, diags),
					Managed:  types.BoolValue(e.Managed),
					Animated: types.BoolValue(e.Animated),
				}
			})
		},
	}
}

// Webhooks.

var webhookTypes = enumMapping{"", "incoming", "channel_follower", "application"}

type webhooksDataModel struct {
	ServerID types.String       `tfsdk:"server_id"`
	Webhooks []webhookItemModel `tfsdk:"webhooks"`
}

type webhookItemModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Type      types.String `tfsdk:"type"`
	ChannelID types.String `tfsdk:"channel_id"`
}

func newWebhooksDataSource() datasource.DataSource {
	return &listDataSource[webhooksDataModel]{
		name: "webhooks",
		desc: "Lists the webhooks of every channel in a server. Requires the Manage Webhooks permission.",
		attrs: map[string]schema.Attribute{
			"webhooks": computedList("The webhooks.", map[string]schema.Attribute{
				"id":         computedString("Webhook ID."),
				"name":       computedString("Webhook name."),
				"type":       computedString("Webhook type: " + webhookTypes.doc() + "."),
				"channel_id": computedString("ID of the channel the webhook posts to."),
			}),
		},
		read: func(ctx context.Context, c *discord.Client, m *webhooksDataModel, diags *diag.Diagnostics) {
			webhooks, err := c.ListGuildWebhooks(ctx, m.ServerID.ValueString())
			if err != nil {
				apiError(diags, "list webhooks", err)
				return
			}
			m.Webhooks = listOf(webhooks, func(w discord.Webhook) webhookItemModel {
				return webhookItemModel{
					ID:        types.StringValue(w.ID),
					Name:      stringPtrValue(w.Name),
					Type:      webhookTypes.name(int64(w.Type)),
					ChannelID: types.StringValue(w.ChannelID),
				}
			})
		},
	}
}

// Invites.

type invitesDataModel struct {
	ServerID types.String      `tfsdk:"server_id"`
	Invites  []inviteItemModel `tfsdk:"invites"`
}

type inviteItemModel struct {
	Code      types.String `tfsdk:"code"`
	ChannelID types.String `tfsdk:"channel_id"`
	MaxAge    types.Int64  `tfsdk:"max_age"`
	MaxUses   types.Int64  `tfsdk:"max_uses"`
	Uses      types.Int64  `tfsdk:"uses"`
	Temporary types.Bool   `tfsdk:"temporary"`
	CreatedAt types.String `tfsdk:"created_at"`
	ExpiresAt types.String `tfsdk:"expires_at"`
}

func newInvitesDataSource() datasource.DataSource {
	return &listDataSource[invitesDataModel]{
		name: "invites",
		desc: "Lists the invites of a server. Requires the Manage Server or View Audit Log permission. Discord only " +
			"returns `max_age`, `max_uses`, `uses`, `temporary` and `created_at` when the bot has Manage Server; " +
			"otherwise they are zero, false or null.",
		attrs: map[string]schema.Attribute{
			"invites": computedList("The invites.", map[string]schema.Attribute{
				"code":       computedString("Invite code."),
				"channel_id": computedString("ID of the channel the invite is for."),
				"max_age":    computedInt("Seconds the invite is valid for after it was created; 0 for forever."),
				"max_uses":   computedInt("Maximum number of uses; 0 for unlimited."),
				"uses":       computedInt("Number of times the invite was used."),
				"temporary":  computedBool("Whether the invite grants temporary membership."),
				"created_at": computedString("When the invite was created."),
				"expires_at": computedString("When the invite expires; null for never."),
			}),
		},
		read: func(ctx context.Context, c *discord.Client, m *invitesDataModel, diags *diag.Diagnostics) {
			invites, err := c.ListGuildInvites(ctx, m.ServerID.ValueString())
			if err != nil {
				apiError(diags, "list invites", err)
				return
			}
			m.Invites = listOf(invites, func(inv discord.Invite) inviteItemModel {
				channelID := types.StringNull()
				if inv.Channel != nil {
					channelID = types.StringValue(inv.Channel.ID)
				}
				return inviteItemModel{
					Code:      types.StringValue(inv.Code),
					ChannelID: channelID,
					MaxAge:    types.Int64Value(inv.MaxAge),
					MaxUses:   types.Int64Value(inv.MaxUses),
					Uses:      types.Int64Value(inv.Uses),
					Temporary: types.BoolValue(inv.Temporary),
					CreatedAt: stringPtrValue(&inv.CreatedAt),
					ExpiresAt: stringPtrValue(inv.ExpiresAt),
				}
			})
		},
	}
}

// Scheduled events.

type scheduledEventsDataModel struct {
	ServerID        types.String              `tfsdk:"server_id"`
	ScheduledEvents []scheduledEventItemModel `tfsdk:"scheduled_events"`
}

type scheduledEventItemModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Description        types.String `tfsdk:"description"`
	EntityType         types.String `tfsdk:"entity_type"`
	Status             types.String `tfsdk:"status"`
	ChannelID          types.String `tfsdk:"channel_id"`
	Location           types.String `tfsdk:"location"`
	ScheduledStartTime types.String `tfsdk:"scheduled_start_time"`
	ScheduledEndTime   types.String `tfsdk:"scheduled_end_time"`
	CreatorID          types.String `tfsdk:"creator_id"`
}

func newScheduledEventsDataSource() datasource.DataSource {
	return &listDataSource[scheduledEventsDataModel]{
		name: "scheduled_events",
		desc: "Lists the scheduled events of a server.",
		attrs: map[string]schema.Attribute{
			"scheduled_events": computedList("The scheduled events.", map[string]schema.Attribute{
				"id":                   computedString("Event ID."),
				"name":                 computedString("Event name."),
				"description":          computedString("Event description."),
				"entity_type":          computedString("Where the event takes place: " + scheduledEventEntityTypes.doc() + "."),
				"status":               computedString("Event status: " + scheduledEventStatuses.doc() + "."),
				"channel_id":           computedString("ID of the stage or voice channel; null for external events."),
				"location":             computedString("Location of an external event."),
				"scheduled_start_time": computedString("When the event starts, as an RFC 3339 timestamp."),
				"scheduled_end_time":   computedString("When the event ends, as an RFC 3339 timestamp."),
				"creator_id":           computedString("ID of the user who created the event."),
			}),
		},
		read: func(ctx context.Context, c *discord.Client, m *scheduledEventsDataModel, diags *diag.Diagnostics) {
			events, err := c.ListScheduledEvents(ctx, m.ServerID.ValueString())
			if err != nil {
				apiError(diags, "list scheduled events", err)
				return
			}
			m.ScheduledEvents = listOf(events, func(e discord.ScheduledEvent) scheduledEventItemModel {
				location := types.StringNull()
				if e.EntityMetadata != nil {
					location = stringPtrValue(e.EntityMetadata.Location)
				}
				return scheduledEventItemModel{
					ID:                 types.StringValue(e.ID),
					Name:               types.StringValue(e.Name),
					Description:        stringPtrValue(e.Description),
					EntityType:         scheduledEventEntityTypes.name(e.EntityType),
					Status:             scheduledEventStatuses.name(e.Status),
					ChannelID:          stringPtrValue(e.ChannelID),
					Location:           location,
					ScheduledStartTime: types.StringValue(e.ScheduledStartTime),
					ScheduledEndTime:   stringPtrValue(e.ScheduledEndTime),
					CreatorID:          stringPtrValue(e.CreatorID),
				}
			})
		},
	}
}

// Threads.

type threadsDataModel struct {
	ServerID types.String      `tfsdk:"server_id"`
	Threads  []threadItemModel `tfsdk:"threads"`
}

type threadItemModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Type      types.String `tfsdk:"type"`
	ChannelID types.String `tfsdk:"channel_id"`
	OwnerID   types.String `tfsdk:"owner_id"`
	Locked    types.Bool   `tfsdk:"locked"`
}

func newThreadsDataSource() datasource.DataSource {
	return &listDataSource[threadsDataModel]{
		name: "threads",
		desc: "Lists the active (not archived) threads of a server, public and private, newest first.",
		attrs: map[string]schema.Attribute{
			"threads": computedList("The threads.", map[string]schema.Attribute{
				"id":         computedString("Thread ID."),
				"name":       computedString("Thread name."),
				"type":       computedString("Thread type: `announcement_thread`, `public_thread` or `private_thread`."),
				"channel_id": computedString("ID of the channel the thread is in."),
				"owner_id":   computedString("ID of the user who started the thread."),
				"locked":     computedBool("Whether only moderators can unarchive the thread."),
			}),
		},
		read: func(ctx context.Context, c *discord.Client, m *threadsDataModel, diags *diag.Diagnostics) {
			threads, err := c.ListActiveThreads(ctx, m.ServerID.ValueString())
			if err != nil {
				apiError(diags, "list active threads", err)
				return
			}
			m.Threads = listOf(threads, func(t discord.Thread) threadItemModel {
				typ, ok := threadTypes[t.Type]
				if !ok {
					typ = channelTypeName(t.Type)
				}
				return threadItemModel{
					ID:        types.StringValue(t.ID),
					Name:      types.StringValue(t.Name),
					Type:      types.StringValue(typ),
					ChannelID: stringPtrValue(t.ParentID),
					OwnerID:   types.StringValue(t.OwnerID),
					Locked:    types.BoolValue(t.ThreadMetadata != nil && t.ThreadMetadata.Locked),
				}
			})
		},
	}
}

// Integrations.

type integrationsDataModel struct {
	ServerID     types.String           `tfsdk:"server_id"`
	Integrations []integrationItemModel `tfsdk:"integrations"`
}

type integrationItemModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Type        types.String `tfsdk:"type"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	AccountID   types.String `tfsdk:"account_id"`
	AccountName types.String `tfsdk:"account_name"`
}

func newIntegrationsDataSource() datasource.DataSource {
	return &listDataSource[integrationsDataModel]{
		name: "integrations",
		desc: "Lists the integrations of a server, such as bots and Twitch or YouTube connections. Requires the " +
			"Manage Server permission. Discord returns at most 50 integrations.",
		attrs: map[string]schema.Attribute{
			"integrations": computedList("The integrations.", map[string]schema.Attribute{
				"id":           computedString("Integration ID."),
				"name":         computedString("Integration name."),
				"type":         computedString("Integration type: `discord`, `twitch`, `youtube` or `guild_subscription`."),
				"enabled":      computedBool("Whether the integration is enabled."),
				"account_id":   computedString("ID of the integration's account; for a bot, its user ID."),
				"account_name": computedString("Name of the integration's account."),
			}),
		},
		read: func(ctx context.Context, c *discord.Client, m *integrationsDataModel, diags *diag.Diagnostics) {
			integrations, err := c.ListIntegrations(ctx, m.ServerID.ValueString())
			if err != nil {
				apiError(diags, "list integrations", err)
				return
			}
			m.Integrations = listOf(integrations, func(i discord.Integration) integrationItemModel {
				return integrationItemModel{
					ID:          types.StringValue(i.ID),
					Name:        types.StringValue(i.Name),
					Type:        types.StringValue(i.Type),
					Enabled:     types.BoolValue(i.Enabled),
					AccountID:   types.StringValue(i.Account.ID),
					AccountName: types.StringValue(i.Account.Name),
				}
			})
		},
	}
}

// Server templates.

type serverTemplatesDataModel struct {
	ServerID  types.String              `tfsdk:"server_id"`
	Templates []serverTemplateItemModel `tfsdk:"templates"`
}

type serverTemplateItemModel struct {
	Code        types.String `tfsdk:"code"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	UsageCount  types.Int64  `tfsdk:"usage_count"`
	CreatorID   types.String `tfsdk:"creator_id"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
	IsDirty     types.Bool   `tfsdk:"is_dirty"`
}

func newServerTemplatesDataSource() datasource.DataSource {
	return &listDataSource[serverTemplatesDataModel]{
		name: "server_templates",
		desc: "Lists the templates of a server. Requires the Manage Server permission.",
		attrs: map[string]schema.Attribute{
			"templates": computedList("The templates.", map[string]schema.Attribute{
				"code":        computedString("Template code, used in `https://discord.new/<code>`."),
				"name":        computedString("Template name."),
				"description": computedString("Template description."),
				"usage_count": computedInt("Number of times the template was used."),
				"creator_id":  computedString("ID of the user who created the template."),
				"created_at":  computedString("When the template was created."),
				"updated_at":  computedString("When the template was last synced with the server."),
				"is_dirty":    computedBool("Whether the server changed since the template was last synced."),
			}),
		},
		read: func(ctx context.Context, c *discord.Client, m *serverTemplatesDataModel, diags *diag.Diagnostics) {
			templates, err := c.ListTemplates(ctx, m.ServerID.ValueString())
			if err != nil {
				apiError(diags, "list server templates", err)
				return
			}
			m.Templates = listOf(templates, func(t discord.GuildTemplate) serverTemplateItemModel {
				return serverTemplateItemModel{
					Code:        types.StringValue(t.Code),
					Name:        types.StringValue(t.Name),
					Description: stringPtrValue(t.Description),
					UsageCount:  types.Int64Value(t.UsageCount),
					CreatorID:   types.StringValue(t.CreatorID),
					CreatedAt:   types.StringValue(t.CreatedAt),
					UpdatedAt:   types.StringValue(t.UpdatedAt),
					IsDirty:     types.BoolValue(t.IsDirty != nil && *t.IsDirty),
				}
			})
		},
	}
}

package provider

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var channelTypeNames = map[int]string{
	discord.ChannelTypeText:         "text",
	discord.ChannelTypeVoice:        "voice",
	discord.ChannelTypeCategory:     "category",
	discord.ChannelTypeAnnouncement: "announcement",
	discord.ChannelTypeStage:        "stage",
	discord.ChannelTypeForum:        "forum",
	discord.ChannelTypeMedia:        "media",
}

func channelTypeName(t int) string {
	if n, ok := channelTypeNames[t]; ok {
		return n
	}
	return fmt.Sprintf("unknown_%d", t)
}

func dsServerID() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "ID of the server (guild).",
		Required:            true,
		Validators:          []validator.String{snowflakeValidator()},
	}
}

func computedString(desc string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Computed: true}
}

func computedInt(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{MarkdownDescription: desc, Computed: true}
}

func computedBool(desc string) schema.BoolAttribute {
	return schema.BoolAttribute{MarkdownDescription: desc, Computed: true}
}

// lookupAttrs returns optional "id" and "name" attributes, exactly one of
// which must be set.
func lookupAttrs(what string) (schema.StringAttribute, schema.StringAttribute) {
	return schema.StringAttribute{
		MarkdownDescription: fmt.Sprintf("ID of the %s. Exactly one of `id` or `name` is required.", what),
		Optional:            true,
		Computed:            true,
		Validators:          []validator.String{snowflakeValidator()},
	}, schema.StringAttribute{
		MarkdownDescription: fmt.Sprintf("Name of the %s. The lookup fails if no %s or more than one has this name.", what, what),
		Optional:            true,
		Computed:            true,
	}
}

func exactlyOneIDOrName() []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name")),
	}
}

// Server.

type serverDataSource struct{ client *discord.Client }

type serverDataModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Description       types.String `tfsdk:"description"`
	IconHash          types.String `tfsdk:"icon_hash"`
	OwnerID           types.String `tfsdk:"owner_id"`
	Features          types.Set    `tfsdk:"features"`
	PreferredLocale   types.String `tfsdk:"preferred_locale"`
	VerificationLevel types.String `tfsdk:"verification_level"`
	AFKChannelID      types.String `tfsdk:"afk_channel_id"`
	AFKTimeout        types.Int64  `tfsdk:"afk_timeout"`
	SystemChannelID   types.String `tfsdk:"system_channel_id"`
	RulesChannelID    types.String `tfsdk:"rules_channel_id"`
	PremiumTier       types.Int64  `tfsdk:"premium_tier"`
}

func newServerDataSource() datasource.DataSource { return &serverDataSource{} }

func (d *serverDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server"
}

func (d *serverDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a server (guild) the bot is a member of.",
		Attributes: map[string]schema.Attribute{
			"id":                 schema.StringAttribute{MarkdownDescription: "Server ID.", Required: true, Validators: []validator.String{snowflakeValidator()}},
			"name":               computedString("Server name."),
			"description":        computedString("Server description."),
			"icon_hash":          computedString("Hash of the server icon."),
			"owner_id":           computedString("ID of the server owner."),
			"features":           schema.SetAttribute{MarkdownDescription: "Enabled server features.", ElementType: types.StringType, Computed: true},
			"preferred_locale":   computedString("Preferred locale."),
			"verification_level": computedString("Verification level: " + verificationLevels.doc() + "."),
			"afk_channel_id":     computedString("AFK voice channel ID."),
			"afk_timeout":        computedInt("AFK timeout in seconds."),
			"system_channel_id":  computedString("System messages channel ID."),
			"rules_channel_id":   computedString("Rules channel ID."),
			"premium_tier":       computedInt("Server boost level (0-3)."),
		},
	}
}

func (d *serverDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromDataSource(req, resp)
}

func (d *serverDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m serverDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := d.client.GetGuild(ctx, m.ID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "read server", err)
		return
	}
	m.Name = types.StringValue(g.Name)
	m.Description = stringPtrValue(g.Description)
	m.IconHash = stringPtrValue(g.Icon)
	m.OwnerID = types.StringValue(g.OwnerID)
	m.Features = stringSetValue(ctx, g.Features, &resp.Diagnostics)
	m.PreferredLocale = types.StringValue(g.PreferredLocale)
	m.VerificationLevel = verificationLevels.name(g.VerificationLevel)
	m.AFKChannelID = stringPtrValue(g.AFKChannelID)
	m.AFKTimeout = types.Int64Value(g.AFKTimeout)
	m.SystemChannelID = stringPtrValue(g.SystemChannelID)
	m.RulesChannelID = stringPtrValue(g.RulesChannelID)
	m.PremiumTier = types.Int64Value(g.PremiumTier)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Role.

type roleDataSource struct{ client *discord.Client }

type roleDataModel struct {
	ServerID    types.String `tfsdk:"server_id"`
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Permissions types.String `tfsdk:"permissions"`
	Color       types.Int64  `tfsdk:"color"`
	Hoist       types.Bool   `tfsdk:"hoist"`
	Mentionable types.Bool   `tfsdk:"mentionable"`
	Position    types.Int64  `tfsdk:"position"`
	Managed     types.Bool   `tfsdk:"managed"`
}

func newRoleDataSource() datasource.DataSource { return &roleDataSource{} }

func (d *roleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (d *roleDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return exactlyOneIDOrName()
}

func (d *roleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	id, name := lookupAttrs("role")
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a server role by ID or name, e.g. to reference roles created outside Terraform or by integrations.",
		Attributes: map[string]schema.Attribute{
			"server_id":   dsServerID(),
			"id":          id,
			"name":        name,
			"permissions": computedString("Permission bitfield as a decimal string."),
			"color":       computedInt("Primary RGB color."),
			"hoist":       computedBool("Whether members are displayed separately."),
			"mentionable": computedBool("Whether the role can be mentioned by anyone."),
			"position":    computedInt("Role position."),
			"managed":     computedBool("Whether the role is managed by an integration."),
		},
	}
}

func (d *roleDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromDataSource(req, resp)
}

func (d *roleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m roleDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	roles, err := d.client.ListRoles(ctx, m.ServerID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "list roles", err)
		return
	}
	var matches []discord.Role
	for _, r := range roles {
		if (!m.ID.IsNull() && r.ID == m.ID.ValueString()) || (!m.Name.IsNull() && r.Name == m.Name.ValueString()) {
			matches = append(matches, r)
		}
	}
	role, ok := single(matches, "role", &resp.Diagnostics)
	if !ok {
		return
	}
	color := role.Color
	if role.Colors != nil {
		color = role.Colors.PrimaryColor
	}
	m.ID = types.StringValue(role.ID)
	m.Name = types.StringValue(role.Name)
	m.Permissions = types.StringValue(role.Permissions)
	m.Color = types.Int64Value(color)
	m.Hoist = types.BoolValue(role.Hoist)
	m.Mentionable = types.BoolValue(role.Mentionable)
	m.Position = types.Int64Value(role.Position)
	m.Managed = types.BoolValue(role.Managed)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

type diagAdder interface {
	AddError(summary, detail string)
}

func single[T any](matches []T, what string, diags diagAdder) (T, bool) {
	var zero T
	switch len(matches) {
	case 1:
		return matches[0], true
	case 0:
		diags.AddError("No "+what+" found", "No "+what+" matches the given criteria.")
	default:
		diags.AddError("Multiple "+what+"s found", fmt.Sprintf("%d %ss match the given criteria; look it up by ID instead.", len(matches), what))
	}
	return zero, false
}

// Channel.

type channelDataSource struct{ client *discord.Client }

type channelDataModel struct {
	ServerID   types.String `tfsdk:"server_id"`
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	Type       types.String `tfsdk:"type"`
	CategoryID types.String `tfsdk:"category_id"`
	Position   types.Int64  `tfsdk:"position"`
	Topic      types.String `tfsdk:"topic"`
	NSFW       types.Bool   `tfsdk:"nsfw"`
}

func newChannelDataSource() datasource.DataSource { return &channelDataSource{} }

func (d *channelDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_channel"
}

func (d *channelDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return exactlyOneIDOrName()
}

func (d *channelDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	id, name := lookupAttrs("channel")
	names := slices.Sorted(maps.Values(channelTypeNames))
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a server channel by ID or name. The bot must be able to view the channel.",
		Attributes: map[string]schema.Attribute{
			"server_id": dsServerID(),
			"id":        id,
			"name":      name,
			"type": schema.StringAttribute{
				MarkdownDescription: "Channel type: `text`, `voice`, `category`, `announcement`, `stage`, `forum` or `media`. " +
					"When looking up by name, only channels of this type match.",
				Optional:   true,
				Computed:   true,
				Validators: []validator.String{stringvalidator.OneOf(names...)},
			},
			"category_id": computedString("Parent category ID."),
			"position":    computedInt("Sort position."),
			"topic":       computedString("Channel topic."),
			"nsfw":        computedBool("Whether the channel is age-restricted."),
		},
	}
}

func (d *channelDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromDataSource(req, resp)
}

func (d *channelDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m channelDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	channels, err := d.client.ListChannels(ctx, m.ServerID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "list channels", err)
		return
	}
	var matches []discord.Channel
	for _, ch := range channels {
		if !m.Type.IsNull() && channelTypeName(ch.Type) != m.Type.ValueString() {
			continue
		}
		if (!m.ID.IsNull() && ch.ID == m.ID.ValueString()) || (!m.Name.IsNull() && strings.EqualFold(ch.Name, m.Name.ValueString())) {
			matches = append(matches, ch)
		}
	}
	ch, ok := single(matches, "channel", &resp.Diagnostics)
	if !ok {
		return
	}
	m.ID = types.StringValue(ch.ID)
	m.Name = types.StringValue(ch.Name)
	m.Type = types.StringValue(channelTypeName(ch.Type))
	m.CategoryID = stringPtrValue(ch.ParentID)
	m.Position = types.Int64Value(ch.Position)
	m.Topic = stringPtrValue(ch.Topic)
	m.NSFW = types.BoolValue(ch.NSFW)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Member.

type memberDataSource struct{ client *discord.Client }

type memberDataModel struct {
	ServerID   types.String `tfsdk:"server_id"`
	UserID     types.String `tfsdk:"user_id"`
	Username   types.String `tfsdk:"username"`
	GlobalName types.String `tfsdk:"global_name"`
	Nick       types.String `tfsdk:"nick"`
	Roles      types.Set    `tfsdk:"roles"`
	JoinedAt   types.String `tfsdk:"joined_at"`
	Bot        types.Bool   `tfsdk:"bot"`
}

func newMemberDataSource() datasource.DataSource { return &memberDataSource{} }

func (d *memberDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_member"
}

func (d *memberDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("user_id"), path.MatchRoot("username")),
	}
}

func (d *memberDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a server member by user ID or username. Usernames are the unique, lowercase " +
			"Discord usernames (not display names). Requires the Server Members privileged intent for username lookups.",
		Attributes: map[string]schema.Attribute{
			"server_id": dsServerID(),
			"user_id": schema.StringAttribute{
				MarkdownDescription: "User ID. Exactly one of `user_id` or `username` is required.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{snowflakeValidator()},
			},
			"username": schema.StringAttribute{
				MarkdownDescription: "Unique username.",
				Optional:            true,
				Computed:            true,
			},
			"global_name": computedString("Display name."),
			"nick":        computedString("Server nickname."),
			"roles":       schema.SetAttribute{MarkdownDescription: "IDs of the member's roles.", ElementType: types.StringType, Computed: true},
			"joined_at":   computedString("When the member joined the server."),
			"bot":         computedBool("Whether the user is a bot."),
		},
	}
}

func (d *memberDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromDataSource(req, resp)
}

func (d *memberDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m memberDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var member *discord.Member
	if !m.UserID.IsNull() {
		var err error
		if member, err = d.client.GetMember(ctx, m.ServerID.ValueString(), m.UserID.ValueString()); err != nil {
			apiError(&resp.Diagnostics, "read member", err)
			return
		}
	} else {
		username := strings.ToLower(m.Username.ValueString())
		found, err := d.client.SearchMembers(ctx, m.ServerID.ValueString(), username)
		if err != nil {
			apiError(&resp.Diagnostics, "search members", err)
			return
		}
		var matches []discord.Member
		for _, f := range found {
			if f.User != nil && strings.ToLower(f.User.Username) == username {
				matches = append(matches, f)
			}
		}
		one, ok := single(matches, "member", &resp.Diagnostics)
		if !ok {
			return
		}
		member = &one
	}
	if member.User == nil {
		resp.Diagnostics.AddError("Incomplete member", "Discord returned a member without a user.")
		return
	}
	m.UserID = types.StringValue(member.User.ID)
	m.Username = types.StringValue(member.User.Username)
	m.GlobalName = stringPtrValue(member.User.GlobalName)
	m.Nick = stringPtrValue(member.Nick)
	m.Roles = stringSetValue(ctx, member.Roles, &resp.Diagnostics)
	m.JoinedAt = types.StringValue(member.JoinedAt)
	m.Bot = types.BoolValue(member.User.Bot)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

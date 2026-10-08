package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	videoQualityModes  = enumMapping{"", "auto", "full"}
	forumSortOrders    = enumMapping{"latest_activity", "creation_date"}
	forumLayouts       = enumMapping{"not_set", "list_view", "gallery_view"}
	autoArchiveMinutes = []int64{60, 1440, 4320, 10080}
)

// Shared attribute definitions.

func categoryIDAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "ID of the parent category channel. Omit to place the channel outside any category.",
		Optional:            true,
		Validators:          []validator.String{snowflakeValidator()},
	}
}

func topicAttribute(maxLen int, desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Optional:            true,
		Validators:          []validator.String{stringvalidator.LengthBetween(1, maxLen)},
	}
}

func nsfwAttribute() schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: "Whether the channel is age-restricted. Defaults to `false`.",
		Optional:            true,
		Computed:            true,
		Default:             booldefault.StaticBool(false),
	}
}

func slowmodeAttribute(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: desc + " Between `0` and `21600` seconds. Defaults to `0`.",
		Optional:            true,
		Computed:            true,
		Default:             int64default.StaticInt64(0),
		Validators:          []validator.Int64{int64validator.Between(0, 21600)},
	}
}

func autoArchiveAttribute() schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: "Default minutes of inactivity after which new threads are archived: `60`, `1440`, `4320` or `10080`.",
		Optional:            true,
		Computed:            true,
		Validators:          []validator.Int64{int64validator.OneOf(autoArchiveMinutes...)},
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
	}
}

func bitrateAttribute(maxBitrate int64, maxDesc string) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: "Bitrate in bits per second, at least `8000`. " + maxDesc + " Defaults to Discord's default (64000).",
		Optional:            true,
		Computed:            true,
		Validators:          []validator.Int64{int64validator.Between(8000, maxBitrate)},
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
	}
}

func userLimitAttribute(maxUsers int64) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: "Maximum number of connected users; `0` means unlimited. Defaults to `0`.",
		Optional:            true,
		Computed:            true,
		Default:             int64default.StaticInt64(0),
		Validators:          []validator.Int64{int64validator.Between(0, maxUsers)},
	}
}

func rtcRegionAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Voice region ID. Omit for automatic selection.",
		Optional:            true,
	}
}

func videoQualityAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Camera video quality: " + videoQualityModes.doc() + ". Defaults to `auto`.",
		Optional:            true,
		Computed:            true,
		Default:             stringdefault.StaticString("auto"),
		Validators:          []validator.String{videoQualityModes.validator()},
	}
}

func newChannelResource[T any, PT interface {
	*T
	channelModel
}](kind channelKind) resource.Resource {
	return &channelResource[T, PT]{kind: kind}
}

// Category.

type categoryChannelModel struct {
	channelBase
}

func (m *categoryChannelModel) base() *channelBase { return &m.channelBase }

func (m *categoryChannelModel) payload(context.Context) (discord.Payload, diag.Diagnostics) {
	p := discord.Payload{}
	m.channelBase.payload(p)
	return p, nil
}

func (m *categoryChannelModel) apply(_ context.Context, ch *discord.Channel) diag.Diagnostics {
	m.channelBase.apply(ch)
	return nil
}

func newCategoryChannelResource() resource.Resource {
	return newChannelResource[categoryChannelModel](channelKind{
		typeName:    "category_channel",
		channelType: discord.ChannelTypeCategory,
		description: "Manages a category that groups other channels.",
	})
}

// Text and announcement.

type textChannelModel struct {
	channelBase
	CategoryID                 types.String `tfsdk:"category_id"`
	Topic                      types.String `tfsdk:"topic"`
	NSFW                       types.Bool   `tfsdk:"nsfw"`
	RateLimitPerUser           types.Int64  `tfsdk:"rate_limit_per_user"`
	DefaultAutoArchiveDuration types.Int64  `tfsdk:"default_auto_archive_duration"`
}

func (m *textChannelModel) base() *channelBase { return &m.channelBase }

func (m *textChannelModel) payload(context.Context) (discord.Payload, diag.Diagnostics) {
	p := discord.Payload{}
	m.channelBase.payload(p)
	putString(p, "parent_id", m.CategoryID)
	putString(p, "topic", m.Topic)
	putBool(p, "nsfw", m.NSFW)
	putInt(p, "rate_limit_per_user", m.RateLimitPerUser)
	putKnownInt(p, "default_auto_archive_duration", m.DefaultAutoArchiveDuration)
	return p, nil
}

func (m *textChannelModel) apply(_ context.Context, ch *discord.Channel) diag.Diagnostics {
	m.channelBase.apply(ch)
	m.CategoryID = stringPtrValue(ch.ParentID)
	m.Topic = stringPtrValue(ch.Topic)
	m.NSFW = types.BoolValue(ch.NSFW)
	m.RateLimitPerUser = types.Int64Value(ch.RateLimitPerUser)
	m.DefaultAutoArchiveDuration = types.Int64Value(ch.DefaultAutoArchiveDuration)
	return nil
}

func newTextChannelResource() resource.Resource {
	return newChannelResource[textChannelModel](channelKind{
		typeName:    "text_channel",
		channelType: discord.ChannelTypeText,
		description: "Manages a text channel.",
		attributes: map[string]schema.Attribute{
			"category_id":                   categoryIDAttribute(),
			"topic":                         topicAttribute(1024, "Channel topic (up to 1024 characters)."),
			"nsfw":                          nsfwAttribute(),
			"rate_limit_per_user":           slowmodeAttribute("Slowmode: seconds a member must wait between messages."),
			"default_auto_archive_duration": autoArchiveAttribute(),
		},
	})
}

type announcementChannelModel struct {
	channelBase
	CategoryID                 types.String `tfsdk:"category_id"`
	Topic                      types.String `tfsdk:"topic"`
	NSFW                       types.Bool   `tfsdk:"nsfw"`
	DefaultAutoArchiveDuration types.Int64  `tfsdk:"default_auto_archive_duration"`
}

func (m *announcementChannelModel) base() *channelBase { return &m.channelBase }

func (m *announcementChannelModel) payload(context.Context) (discord.Payload, diag.Diagnostics) {
	p := discord.Payload{}
	m.channelBase.payload(p)
	putString(p, "parent_id", m.CategoryID)
	putString(p, "topic", m.Topic)
	putBool(p, "nsfw", m.NSFW)
	putKnownInt(p, "default_auto_archive_duration", m.DefaultAutoArchiveDuration)
	return p, nil
}

func (m *announcementChannelModel) apply(_ context.Context, ch *discord.Channel) diag.Diagnostics {
	m.channelBase.apply(ch)
	m.CategoryID = stringPtrValue(ch.ParentID)
	m.Topic = stringPtrValue(ch.Topic)
	m.NSFW = types.BoolValue(ch.NSFW)
	m.DefaultAutoArchiveDuration = types.Int64Value(ch.DefaultAutoArchiveDuration)
	return nil
}

func newAnnouncementChannelResource() resource.Resource {
	return newChannelResource[announcementChannelModel](channelKind{
		typeName:    "announcement_channel",
		channelType: discord.ChannelTypeAnnouncement,
		description: "Manages an announcement (news) channel whose messages other servers can follow. " +
			"Requires the server to have Community enabled; otherwise Discord rejects the channel type.",
		attributes: map[string]schema.Attribute{
			"category_id":                   categoryIDAttribute(),
			"topic":                         topicAttribute(1024, "Channel topic (up to 1024 characters)."),
			"nsfw":                          nsfwAttribute(),
			"default_auto_archive_duration": autoArchiveAttribute(),
		},
	})
}

// Voice and stage.

type voiceChannelModel struct {
	channelBase
	CategoryID       types.String `tfsdk:"category_id"`
	Bitrate          types.Int64  `tfsdk:"bitrate"`
	UserLimit        types.Int64  `tfsdk:"user_limit"`
	RTCRegion        types.String `tfsdk:"rtc_region"`
	VideoQualityMode types.String `tfsdk:"video_quality_mode"`
	NSFW             types.Bool   `tfsdk:"nsfw"`
	RateLimitPerUser types.Int64  `tfsdk:"rate_limit_per_user"`
}

func (m *voiceChannelModel) base() *channelBase { return &m.channelBase }

func (m *voiceChannelModel) payload(context.Context) (discord.Payload, diag.Diagnostics) {
	p := discord.Payload{}
	m.channelBase.payload(p)
	putString(p, "parent_id", m.CategoryID)
	putKnownInt(p, "bitrate", m.Bitrate)
	putInt(p, "user_limit", m.UserLimit)
	putString(p, "rtc_region", m.RTCRegion)
	videoQualityModes.put(p, "video_quality_mode", m.VideoQualityMode)
	putBool(p, "nsfw", m.NSFW)
	putInt(p, "rate_limit_per_user", m.RateLimitPerUser)
	return p, nil
}

func (m *voiceChannelModel) apply(_ context.Context, ch *discord.Channel) diag.Diagnostics {
	m.channelBase.apply(ch)
	m.CategoryID = stringPtrValue(ch.ParentID)
	m.Bitrate = types.Int64Value(ch.Bitrate)
	m.UserLimit = types.Int64Value(ch.UserLimit)
	m.RTCRegion = stringPtrValue(ch.RTCRegion)
	m.VideoQualityMode = videoQualityName(ch.VideoQualityMode)
	m.NSFW = types.BoolValue(ch.NSFW)
	m.RateLimitPerUser = types.Int64Value(ch.RateLimitPerUser)
	return nil
}

// videoQualityName maps Discord's mode, which is omitted (0) when it has
// never been set, to its effective value.
func videoQualityName(v int64) types.String {
	if v == 0 {
		v = 1
	}
	return videoQualityModes.name(v)
}

func newVoiceChannelResource() resource.Resource {
	return newChannelResource[voiceChannelModel](channelKind{
		typeName:    "voice_channel",
		channelType: discord.ChannelTypeVoice,
		description: "Manages a voice channel.",
		attributes: map[string]schema.Attribute{
			"category_id":         categoryIDAttribute(),
			"bitrate":             bitrateAttribute(384000, "The maximum depends on the server's boost level (96000 to 384000)."),
			"user_limit":          userLimitAttribute(99),
			"rtc_region":          rtcRegionAttribute(),
			"video_quality_mode":  videoQualityAttribute(),
			"nsfw":                nsfwAttribute(),
			"rate_limit_per_user": slowmodeAttribute("Slowmode for the voice channel's text chat."),
		},
	})
}

type stageChannelModel struct {
	channelBase
	CategoryID       types.String `tfsdk:"category_id"`
	Bitrate          types.Int64  `tfsdk:"bitrate"`
	UserLimit        types.Int64  `tfsdk:"user_limit"`
	RTCRegion        types.String `tfsdk:"rtc_region"`
	VideoQualityMode types.String `tfsdk:"video_quality_mode"`
	NSFW             types.Bool   `tfsdk:"nsfw"`
	RateLimitPerUser types.Int64  `tfsdk:"rate_limit_per_user"`
}

func (m *stageChannelModel) base() *channelBase { return &m.channelBase }

func (m *stageChannelModel) payload(context.Context) (discord.Payload, diag.Diagnostics) {
	v := voiceChannelModel(*m)
	return v.payload(context.Background())
}

func (m *stageChannelModel) apply(ctx context.Context, ch *discord.Channel) diag.Diagnostics {
	v := voiceChannelModel(*m)
	diags := v.apply(ctx, ch)
	*m = stageChannelModel(v)
	return diags
}

func newStageChannelResource() resource.Resource {
	return newChannelResource[stageChannelModel](channelKind{
		typeName:    "stage_channel",
		channelType: discord.ChannelTypeStage,
		description: "Manages a stage channel for hosting events with an audience. Requires the server to have Community enabled.",
		attributes: map[string]schema.Attribute{
			"category_id":         categoryIDAttribute(),
			"bitrate":             bitrateAttribute(64000, "At most `64000` for stage channels."),
			"user_limit":          userLimitAttribute(10000),
			"rtc_region":          rtcRegionAttribute(),
			"video_quality_mode":  videoQualityAttribute(),
			"nsfw":                nsfwAttribute(),
			"rate_limit_per_user": slowmodeAttribute("Slowmode for the stage channel's text chat."),
		},
	})
}

// Forum.

type forumChannelModel struct {
	channelBase
	CategoryID                    types.String `tfsdk:"category_id"`
	Topic                         types.String `tfsdk:"topic"`
	NSFW                          types.Bool   `tfsdk:"nsfw"`
	RateLimitPerUser              types.Int64  `tfsdk:"rate_limit_per_user"`
	DefaultAutoArchiveDuration    types.Int64  `tfsdk:"default_auto_archive_duration"`
	DefaultThreadRateLimitPerUser types.Int64  `tfsdk:"default_thread_rate_limit_per_user"`
	DefaultSortOrder              types.String `tfsdk:"default_sort_order"`
	DefaultForumLayout            types.String `tfsdk:"default_forum_layout"`
	RequireTag                    types.Bool   `tfsdk:"require_tag"`
	AvailableTags                 types.List   `tfsdk:"available_tags"`
	DefaultReactionEmoji          types.Object `tfsdk:"default_reaction_emoji"`
}

type forumTagModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Moderated types.Bool   `tfsdk:"moderated"`
	EmojiID   types.String `tfsdk:"emoji_id"`
	EmojiName types.String `tfsdk:"emoji_name"`
}

type emojiRefModel struct {
	EmojiID   types.String `tfsdk:"emoji_id"`
	EmojiName types.String `tfsdk:"emoji_name"`
}

var (
	forumTagAttrTypes = map[string]attr.Type{
		"id": types.StringType, "name": types.StringType, "moderated": types.BoolType,
		"emoji_id": types.StringType, "emoji_name": types.StringType,
	}
	emojiRefAttrTypes = map[string]attr.Type{"emoji_id": types.StringType, "emoji_name": types.StringType}
)

func (m *forumChannelModel) base() *channelBase { return &m.channelBase }

func (m *forumChannelModel) payload(ctx context.Context) (discord.Payload, diag.Diagnostics) {
	var diags diag.Diagnostics
	p := discord.Payload{}
	m.channelBase.payload(p)
	putString(p, "parent_id", m.CategoryID)
	putString(p, "topic", m.Topic)
	putBool(p, "nsfw", m.NSFW)
	putInt(p, "rate_limit_per_user", m.RateLimitPerUser)
	putKnownInt(p, "default_auto_archive_duration", m.DefaultAutoArchiveDuration)
	putInt(p, "default_thread_rate_limit_per_user", m.DefaultThreadRateLimitPerUser)
	if m.DefaultSortOrder.IsNull() {
		p["default_sort_order"] = nil
	} else {
		forumSortOrders.put(p, "default_sort_order", m.DefaultSortOrder)
	}
	forumLayouts.put(p, "default_forum_layout", m.DefaultForumLayout)
	if !m.RequireTag.IsUnknown() {
		var flags int64
		if m.RequireTag.ValueBool() {
			flags = discord.ChannelFlagRequireTag
		}
		p["flags"] = flags
	}

	if !m.AvailableTags.IsUnknown() {
		var tags []forumTagModel
		diags.Append(m.AvailableTags.ElementsAs(ctx, &tags, false)...)
		out := make([]discord.ForumTag, 0, len(tags))
		for _, t := range tags {
			tag := discord.ForumTag{
				Name:      t.Name.ValueString(),
				Moderated: t.Moderated.ValueBool(),
				EmojiID:   t.EmojiID.ValueStringPointer(),
				EmojiName: t.EmojiName.ValueStringPointer(),
			}
			if !t.ID.IsUnknown() {
				tag.ID = t.ID.ValueString()
			}
			out = append(out, tag)
		}
		p["available_tags"] = out
	}

	if m.DefaultReactionEmoji.IsNull() {
		p["default_reaction_emoji"] = nil
	} else if !m.DefaultReactionEmoji.IsUnknown() {
		var e emojiRefModel
		diags.Append(m.DefaultReactionEmoji.As(ctx, &e, basetypes.ObjectAsOptions{})...)
		p["default_reaction_emoji"] = discord.DefaultReaction{EmojiID: e.EmojiID.ValueStringPointer(), EmojiName: e.EmojiName.ValueStringPointer()}
	}
	return p, diags
}

var _ planAdjuster[*forumChannelModel] = (*forumChannelModel)(nil)

// adjustPlan assigns each planned tag the ID of the existing tag with the same
// name, or leaves it unknown for new tags. Without this, Terraform pairs tags
// by list index, so reordering tags would rename them instead and move posts
// to the wrong tag.
func (m *forumChannelModel) adjustPlan(ctx context.Context, prior *forumChannelModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if m.AvailableTags.IsUnknown() || m.AvailableTags.IsNull() || prior.AvailableTags.IsNull() || prior.AvailableTags.IsUnknown() {
		return diags
	}
	var planned, existing []forumTagModel
	diags.Append(m.AvailableTags.ElementsAs(ctx, &planned, false)...)
	diags.Append(prior.AvailableTags.ElementsAs(ctx, &existing, false)...)
	ids := map[string]types.String{}
	for _, t := range existing {
		ids[t.Name.ValueString()] = t.ID
	}
	for i := range planned {
		if id, ok := ids[planned[i].Name.ValueString()]; ok {
			planned[i].ID = id
		} else {
			planned[i].ID = types.StringUnknown()
		}
	}
	list, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: forumTagAttrTypes}, planned)
	diags.Append(d...)
	m.AvailableTags = list
	return diags
}

func (m *forumChannelModel) apply(ctx context.Context, ch *discord.Channel) diag.Diagnostics {
	var diags diag.Diagnostics
	m.channelBase.apply(ch)
	m.CategoryID = stringPtrValue(ch.ParentID)
	m.Topic = stringPtrValue(ch.Topic)
	m.NSFW = types.BoolValue(ch.NSFW)
	m.RateLimitPerUser = types.Int64Value(ch.RateLimitPerUser)
	m.DefaultAutoArchiveDuration = types.Int64Value(ch.DefaultAutoArchiveDuration)
	m.DefaultThreadRateLimitPerUser = types.Int64Value(ch.DefaultThreadRateLimitPerUser)
	if ch.DefaultSortOrder == nil {
		m.DefaultSortOrder = types.StringNull()
	} else {
		m.DefaultSortOrder = forumSortOrders.name(*ch.DefaultSortOrder)
	}
	m.DefaultForumLayout = forumLayouts.name(ch.DefaultForumLayout)
	m.RequireTag = types.BoolValue(ch.Flags&discord.ChannelFlagRequireTag != 0)

	tags := make([]forumTagModel, 0, len(ch.AvailableTags))
	for _, t := range ch.AvailableTags {
		tags = append(tags, forumTagModel{
			ID:        types.StringValue(t.ID),
			Name:      types.StringValue(t.Name),
			Moderated: types.BoolValue(t.Moderated),
			EmojiID:   stringPtrValue(t.EmojiID),
			EmojiName: stringPtrValue(t.EmojiName),
		})
	}
	if len(tags) == 0 && m.AvailableTags.IsNull() {
		m.AvailableTags = types.ListNull(types.ObjectType{AttrTypes: forumTagAttrTypes})
	} else {
		list, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: forumTagAttrTypes}, tags)
		diags.Append(d...)
		m.AvailableTags = list
	}

	if r := ch.DefaultReactionEmoji; r != nil && (r.EmojiID != nil || r.EmojiName != nil) {
		obj, d := types.ObjectValueFrom(ctx, emojiRefAttrTypes, emojiRefModel{EmojiID: stringPtrValue(r.EmojiID), EmojiName: stringPtrValue(r.EmojiName)})
		diags.Append(d...)
		m.DefaultReactionEmoji = obj
	} else {
		m.DefaultReactionEmoji = types.ObjectNull(emojiRefAttrTypes)
	}
	return diags
}

func newForumChannelResource() resource.Resource {
	return newChannelResource[forumChannelModel](channelKind{
		typeName:    "forum_channel",
		channelType: discord.ChannelTypeForum,
		description: "Manages a forum channel, where members create posts (threads) that can be tagged.",
		attributes: map[string]schema.Attribute{
			"category_id":                   categoryIDAttribute(),
			"topic":                         topicAttribute(4096, "Post guidelines shown in the forum (up to 4096 characters)."),
			"nsfw":                          nsfwAttribute(),
			"rate_limit_per_user":           slowmodeAttribute("Slowmode: seconds a member must wait between creating posts."),
			"default_auto_archive_duration": autoArchiveAttribute(),
			"default_thread_rate_limit_per_user": schema.Int64Attribute{
				MarkdownDescription: "Slowmode applied to new posts, in seconds between `0` and `21600`. Defaults to `0`.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(0),
				Validators:          []validator.Int64{int64validator.Between(0, 21600)},
			},
			"default_sort_order": schema.StringAttribute{
				MarkdownDescription: "Default sort order for posts: " + forumSortOrders.doc() + ". Omit to let each member choose.",
				Optional:            true,
				Validators:          []validator.String{forumSortOrders.validator()},
			},
			"default_forum_layout": schema.StringAttribute{
				MarkdownDescription: "Default layout: " + forumLayouts.doc() + ". Defaults to `not_set`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("not_set"),
				Validators:          []validator.String{forumLayouts.validator()},
			},
			"require_tag": schema.BoolAttribute{
				MarkdownDescription: "Whether posts must have at least one tag. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"available_tags": schema.ListNestedAttribute{
				MarkdownDescription: "Tags that can be applied to posts (at most 20). Tags are matched by name on update so existing tag IDs, and posts using them, are preserved.",
				Optional:            true,
				Validators:          []validator.List{listvalidator.SizeAtMost(20)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							MarkdownDescription: "Tag ID assigned by Discord.",
							Computed:            true,
						},
						"name": schema.StringAttribute{
							MarkdownDescription: "Tag name (up to 20 characters).",
							Required:            true,
							Validators:          []validator.String{stringvalidator.LengthBetween(1, 20)},
						},
						"moderated": schema.BoolAttribute{
							MarkdownDescription: "Whether only members with the Manage Threads permission can apply the tag. Defaults to `false`.",
							Optional:            true,
							Computed:            true,
							Default:             booldefault.StaticBool(false),
						},
						"emoji_id": schema.StringAttribute{
							MarkdownDescription: "ID of a custom server emoji for the tag. Conflicts with `emoji_name`.",
							Optional:            true,
							Validators: []validator.String{
								snowflakeValidator(),
								stringvalidator.ConflictsWith(path.MatchRelative().AtParent().AtName("emoji_name")),
							},
						},
						"emoji_name": schema.StringAttribute{
							MarkdownDescription: "Unicode emoji for the tag.",
							Optional:            true,
						},
					},
				},
			},
			"default_reaction_emoji": schema.SingleNestedAttribute{
				MarkdownDescription: "Emoji shown in the add-reaction button on posts.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"emoji_id": schema.StringAttribute{
						MarkdownDescription: "ID of a custom server emoji. Exactly one of `emoji_id` or `emoji_name` is required.",
						Optional:            true,
						Validators: []validator.String{
							snowflakeValidator(),
							stringvalidator.ExactlyOneOf(path.MatchRelative().AtParent().AtName("emoji_name")),
						},
					},
					"emoji_name": schema.StringAttribute{
						MarkdownDescription: "Unicode emoji.",
						Optional:            true,
					},
				},
			},
		},
	})
}

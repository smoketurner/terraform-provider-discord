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
	// convertibleChannelTypes names the channel types Discord converts
	// between in place.
	convertibleChannelTypes = enumMapping{discord.ChannelTypeText: "text", discord.ChannelTypeAnnouncement: "announcement"}
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

// stageUserLimitAttribute differs from userLimitAttribute because Discord
// gives stage channels a limit of 10000 and stores 10000 when 0 is sent, so
// stage channels have no unlimited setting.
func stageUserLimitAttribute() schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: "Maximum number of audience members, between `1` and `10000`. Defaults to Discord's default (10000).",
		Optional:            true,
		Computed:            true,
		Validators:          []validator.Int64{int64validator.Between(1, 10000)},
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
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
	return &channelResource[T, PT]{
		resourceIdentity: resourceIdentity{attrs: []identityAttribute{channelIdentity("id")}},
		kind:             kind,
	}
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

// convertibleType is the current channel type of a text or announcement
// channel.
type convertibleType struct {
	Type types.String `tfsdk:"type"`
}

func (c *convertibleType) apply(ch *discord.Channel) {
	c.Type = convertibleChannelTypes.name(int64(ch.Type))
}

// convertibleTypeAttribute plans the kind's own type, so a channel of the
// other type shows its conversion in the plan.
func convertibleTypeAttribute(kind string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Current channel type: `text` or `announcement`. Read-only. To convert between a text and " +
			"an announcement channel without recreating it, change the resource type and add a `moved` block " +
			"(Terraform 1.8 or later); the next apply sets the type to `" + kind + "`. Converting requires the server " +
			"to have the `NEWS` feature (Community enabled).",
		Computed: true,
		Default:  stringdefault.StaticString(kind),
	}
}

type textChannelModel struct {
	channelBase
	convertibleType
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
	m.convertibleType.apply(ch)
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
		convertible: &channelConversion{
			typeName: "announcement_channel", channelType: discord.ChannelTypeAnnouncement, resource: newAnnouncementChannelResource,
		},
		attributes: map[string]schema.Attribute{
			"type":                          convertibleTypeAttribute("text"),
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
	convertibleType
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
	m.convertibleType.apply(ch)
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
		convertible: &channelConversion{
			typeName: "text_channel", channelType: discord.ChannelTypeText, resource: newTextChannelResource,
		},
		attributes: map[string]schema.Attribute{
			"type":                          convertibleTypeAttribute("announcement"),
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

func (m *stageChannelModel) payload(ctx context.Context) (discord.Payload, diag.Diagnostics) {
	v := voiceChannelModel(*m)
	p, diags := v.payload(ctx)
	delete(p, "user_limit")
	putKnownInt(p, "user_limit", m.UserLimit)
	return p, diags
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
			"user_limit":          stageUserLimitAttribute(),
			"rtc_region":          rtcRegionAttribute(),
			"video_quality_mode":  videoQualityAttribute(),
			"nsfw":                nsfwAttribute(),
			"rate_limit_per_user": slowmodeAttribute("Slowmode for the stage channel's text chat."),
		},
	})
}

// Forum and media.

// postChannelFields holds the attributes shared by forum and media channels,
// whose messages are all posts (threads).
type postChannelFields struct {
	CategoryID                    types.String `tfsdk:"category_id"`
	Topic                         types.String `tfsdk:"topic"`
	NSFW                          types.Bool   `tfsdk:"nsfw"`
	RateLimitPerUser              types.Int64  `tfsdk:"rate_limit_per_user"`
	DefaultAutoArchiveDuration    types.Int64  `tfsdk:"default_auto_archive_duration"`
	DefaultThreadRateLimitPerUser types.Int64  `tfsdk:"default_thread_rate_limit_per_user"`
	DefaultSortOrder              types.String `tfsdk:"default_sort_order"`
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

func (f *postChannelFields) payload(ctx context.Context, p discord.Payload) diag.Diagnostics {
	var diags diag.Diagnostics
	putString(p, "parent_id", f.CategoryID)
	putString(p, "topic", f.Topic)
	putBool(p, "nsfw", f.NSFW)
	putInt(p, "rate_limit_per_user", f.RateLimitPerUser)
	putKnownInt(p, "default_auto_archive_duration", f.DefaultAutoArchiveDuration)
	putInt(p, "default_thread_rate_limit_per_user", f.DefaultThreadRateLimitPerUser)
	if f.DefaultSortOrder.IsNull() {
		p["default_sort_order"] = nil
	} else {
		forumSortOrders.put(p, "default_sort_order", f.DefaultSortOrder)
	}
	putFlag(p, discord.ChannelFlagRequireTag, f.RequireTag)

	if !f.AvailableTags.IsUnknown() {
		var tags []forumTagModel
		diags.Append(f.AvailableTags.ElementsAs(ctx, &tags, false)...)
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

	if f.DefaultReactionEmoji.IsNull() {
		p["default_reaction_emoji"] = nil
	} else if !f.DefaultReactionEmoji.IsUnknown() {
		var e emojiRefModel
		diags.Append(f.DefaultReactionEmoji.As(ctx, &e, basetypes.ObjectAsOptions{})...)
		p["default_reaction_emoji"] = discord.DefaultReaction{EmojiID: e.EmojiID.ValueStringPointer(), EmojiName: e.EmojiName.ValueStringPointer()}
	}
	return diags
}

// putFlag adds bit to the payload's managed channel flags when v is known.
func putFlag(p discord.Payload, bit int64, v types.Bool) {
	if v.IsUnknown() {
		return
	}
	f, _ := p["flags"].(channelFlags)
	p["flags"] = f.with(bit, v.ValueBool())
}

// adjustTags assigns each planned tag the ID of the existing tag with the same
// name, or leaves it unknown for new tags. Without this, Terraform pairs tags
// by list index, so reordering tags would rename them instead and move posts
// to the wrong tag.
func (f *postChannelFields) adjustTags(ctx context.Context, prior *postChannelFields) diag.Diagnostics {
	var diags diag.Diagnostics
	if f.AvailableTags.IsUnknown() || f.AvailableTags.IsNull() || prior.AvailableTags.IsNull() || prior.AvailableTags.IsUnknown() {
		return diags
	}
	var planned, existing []forumTagModel
	diags.Append(f.AvailableTags.ElementsAs(ctx, &planned, false)...)
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
	f.AvailableTags = list
	return diags
}

func (f *postChannelFields) apply(ctx context.Context, ch *discord.Channel) diag.Diagnostics {
	var diags diag.Diagnostics
	f.CategoryID = stringPtrValue(ch.ParentID)
	f.Topic = stringPtrValue(ch.Topic)
	f.NSFW = types.BoolValue(ch.NSFW)
	f.RateLimitPerUser = types.Int64Value(ch.RateLimitPerUser)
	f.DefaultAutoArchiveDuration = types.Int64Value(ch.DefaultAutoArchiveDuration)
	f.DefaultThreadRateLimitPerUser = types.Int64Value(ch.DefaultThreadRateLimitPerUser)
	if ch.DefaultSortOrder == nil {
		f.DefaultSortOrder = types.StringNull()
	} else {
		f.DefaultSortOrder = forumSortOrders.name(*ch.DefaultSortOrder)
	}
	f.RequireTag = types.BoolValue(ch.Flags&discord.ChannelFlagRequireTag != 0)

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
	if len(tags) == 0 && f.AvailableTags.IsNull() {
		f.AvailableTags = types.ListNull(types.ObjectType{AttrTypes: forumTagAttrTypes})
	} else {
		list, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: forumTagAttrTypes}, tags)
		diags.Append(d...)
		f.AvailableTags = list
	}

	if r := ch.DefaultReactionEmoji; r != nil && (r.EmojiID != nil || r.EmojiName != nil) {
		obj, d := types.ObjectValueFrom(ctx, emojiRefAttrTypes, emojiRefModel{EmojiID: stringPtrValue(r.EmojiID), EmojiName: stringPtrValue(r.EmojiName)})
		diags.Append(d...)
		f.DefaultReactionEmoji = obj
	} else {
		f.DefaultReactionEmoji = types.ObjectNull(emojiRefAttrTypes)
	}
	return diags
}

// postChannelAttributes returns the schema attributes for postChannelFields.
func postChannelAttributes(topic schema.StringAttribute) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"category_id":                   categoryIDAttribute(),
		"topic":                         topic,
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
	}
}

type forumChannelModel struct {
	channelBase
	postChannelFields
	DefaultForumLayout types.String `tfsdk:"default_forum_layout"`
}

func (m *forumChannelModel) base() *channelBase { return &m.channelBase }

func (m *forumChannelModel) payload(ctx context.Context) (discord.Payload, diag.Diagnostics) {
	p := discord.Payload{}
	m.channelBase.payload(p)
	diags := m.postChannelFields.payload(ctx, p)
	forumLayouts.put(p, "default_forum_layout", m.DefaultForumLayout)
	return p, diags
}

var _ planAdjuster[*forumChannelModel] = (*forumChannelModel)(nil)

func (m *forumChannelModel) adjustPlan(ctx context.Context, prior *forumChannelModel) diag.Diagnostics {
	return m.adjustTags(ctx, &prior.postChannelFields)
}

func (m *forumChannelModel) apply(ctx context.Context, ch *discord.Channel) diag.Diagnostics {
	m.channelBase.apply(ch)
	diags := m.postChannelFields.apply(ctx, ch)
	m.DefaultForumLayout = forumLayouts.name(ch.DefaultForumLayout)
	return diags
}

func newForumChannelResource() resource.Resource {
	attrs := postChannelAttributes(topicAttribute(4096, "Post guidelines shown in the forum (up to 4096 characters)."))
	attrs["default_forum_layout"] = schema.StringAttribute{
		MarkdownDescription: "Default layout: " + forumLayouts.doc() + ". Defaults to `not_set`.",
		Optional:            true,
		Computed:            true,
		Default:             stringdefault.StaticString("not_set"),
		Validators:          []validator.String{forumLayouts.validator()},
	}
	return newChannelResource[forumChannelModel](channelKind{
		typeName:    "forum_channel",
		channelType: discord.ChannelTypeForum,
		description: "Manages a forum channel, where members create posts (threads) that can be tagged.",
		attributes:  attrs,
	})
}

type mediaChannelModel struct {
	channelBase
	postChannelFields
	HideMediaDownloadOptions types.Bool `tfsdk:"hide_media_download_options"`
}

func (m *mediaChannelModel) base() *channelBase { return &m.channelBase }

func (m *mediaChannelModel) payload(ctx context.Context) (discord.Payload, diag.Diagnostics) {
	p := discord.Payload{}
	m.channelBase.payload(p)
	diags := m.postChannelFields.payload(ctx, p)
	putFlag(p, discord.ChannelFlagHideMediaDownloadOptions, m.HideMediaDownloadOptions)
	return p, diags
}

var (
	_ planAdjuster[*mediaChannelModel] = (*mediaChannelModel)(nil)
	_ createSplitter                   = (*mediaChannelModel)(nil)
)

func (m *mediaChannelModel) adjustPlan(ctx context.Context, prior *mediaChannelModel) diag.Diagnostics {
	return m.adjustTags(ctx, &prior.postChannelFields)
}

// splitCreate moves nsfw to a follow-up modify: Create Guild Channel does not
// list nsfw for media channels, but Modify Channel does.
func (m *mediaChannelModel) splitCreate(p discord.Payload) discord.Payload {
	nsfw, ok := p["nsfw"]
	delete(p, "nsfw")
	if ok && nsfw == true {
		return discord.Payload{"nsfw": true}
	}
	return nil
}

func (m *mediaChannelModel) apply(ctx context.Context, ch *discord.Channel) diag.Diagnostics {
	m.channelBase.apply(ch)
	diags := m.postChannelFields.apply(ctx, ch)
	m.HideMediaDownloadOptions = types.BoolValue(ch.Flags&discord.ChannelFlagHideMediaDownloadOptions != 0)
	return diags
}

func newMediaChannelResource() resource.Resource {
	// Create Guild Channel limits the topic to 1024 characters for every
	// channel type, while Modify Channel allows 4096 for media channels. The
	// lower limit keeps a topic valid for both.
	attrs := postChannelAttributes(topicAttribute(1024, "Post guidelines shown in the channel (up to 1024 characters)."))
	attrs["hide_media_download_options"] = schema.BoolAttribute{
		MarkdownDescription: "Whether to hide the download options on embedded media. Defaults to `false`.",
		Optional:            true,
		Computed:            true,
		Default:             booldefault.StaticBool(false),
	}
	return newChannelResource[mediaChannelModel](channelKind{
		typeName:    "media_channel",
		channelType: discord.ChannelTypeMedia,
		description: "Manages a media channel, a forum-like channel for image and video posts. " +
			"Media channels are a Discord beta available only to Community servers with Server Subscriptions " +
			"enabled (the `ROLE_SUBSCRIPTIONS_ENABLED` server feature), and not yet to all of those; elsewhere Discord " +
			"rejects the channel type with error 50024. Discord documents media channels as still in active " +
			"development, so their behavior may change.",
		attributes: attrs,
	})
}

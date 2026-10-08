package provider

import (
	"context"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure      = &threadResource{}
	_ resource.ResourceWithImportState    = &threadResource{}
	_ resource.ResourceWithValidateConfig = &threadResource{}
)

var threadTypes = map[int]string{
	discord.ChannelTypeAnnouncementThread: "announcement_thread",
	discord.ChannelTypePublicThread:       "public_thread",
	discord.ChannelTypePrivateThread:      "private_thread",
}

type threadResource struct {
	client *discord.Client
}

type threadModel struct {
	ID                  types.String `tfsdk:"id"`
	ChannelID           types.String `tfsdk:"channel_id"`
	ServerID            types.String `tfsdk:"server_id"`
	Name                types.String `tfsdk:"name"`
	Type                types.String `tfsdk:"type"`
	Private             types.Bool   `tfsdk:"private"`
	Invitable           types.Bool   `tfsdk:"invitable"`
	MessageID           types.String `tfsdk:"message_id"`
	Message             types.Object `tfsdk:"message"`
	AutoArchiveDuration types.Int64  `tfsdk:"auto_archive_duration"`
	RateLimitPerUser    types.Int64  `tfsdk:"rate_limit_per_user"`
	Archived            types.Bool   `tfsdk:"archived"`
	Locked              types.Bool   `tfsdk:"locked"`
	Pinned              types.Bool   `tfsdk:"pinned"`
	AppliedTags         types.Set    `tfsdk:"applied_tags"`
	OwnerID             types.String `tfsdk:"owner_id"`
	AuditLogReason      types.String `tfsdk:"audit_log_reason"`
}

// threadMessageModel is the starter message of a forum or media post.
type threadMessageModel struct {
	Content         types.String `tfsdk:"content"`
	Embeds          types.List   `tfsdk:"embeds"`
	AllowedMentions types.Set    `tfsdk:"allowed_mentions"`
}

var threadMessageAttrTypes = map[string]attr.Type{
	"content":          types.StringType,
	"embeds":           types.ListType{ElemType: types.ObjectType{AttrTypes: embedAttrTypes}},
	"allowed_mentions": types.SetType{ElemType: types.StringType},
}

// asMessage reuses discord_message's payload and state handling for the
// starter message.
func (m threadMessageModel) asMessage() *messageModel {
	return &messageModel{Content: m.Content, Embeds: m.Embeds, AllowedMentions: m.AllowedMentions}
}

func newThreadResource() resource.Resource { return &threadResource{} }

func (r *threadResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_thread"
}

func (r *threadResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a thread: a post in a forum or media channel, or a public or private thread in a " +
			"text or announcement channel, optionally started from an existing message.\n\n" +
			"Discord archives threads after a period of inactivity and unpins them when they are archived. An archived " +
			"thread is not treated as drift unless `archived = false` is set, in which case the next apply unarchives " +
			"it. Changing any other argument of an archived thread unarchives it, unless `archived = true` is set.\n\n" +
			"The bot needs the Send Messages permission to create a forum or media post, Create Public Threads or " +
			"Create Private Threads for other threads, and Manage Threads to change `locked`, `rate_limit_per_user` " +
			"and `pinned` or to delete the thread.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttributeFor("creating, changing and deleting the thread " +
				"(Discord records no reason for editing the starter message)"),
			"id": idAttribute("Thread ID. For a forum or media post it is also the starter message's ID."),
			"channel_id": schema.StringAttribute{
				MarkdownDescription: "ID of the text, announcement, forum or media channel to create the thread in.",
				Required:            true,
				Validators:          []validator.String{snowflakeValidator()},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"server_id": schema.StringAttribute{
				MarkdownDescription: "ID of the server (guild) the thread is in.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Thread name (1-100 characters).",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 100)},
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "Thread type: `public_thread` (also used for forum and media posts), " +
					"`private_thread` or `announcement_thread`.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"private": schema.BoolAttribute{
				MarkdownDescription: "Whether to create a private thread, visible only to invited members and moderators. " +
					"Only text channels have private threads. Defaults to `false`.",
				Optional:      true,
				Computed:      true,
				Default:       booldefault.StaticBool(false),
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"invitable": schema.BoolAttribute{
				MarkdownDescription: "Whether members who are not moderators can add other members to a private thread. " +
					"Requires `private = true`. Discord defaults to `true`.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"message_id": schema.StringAttribute{
				MarkdownDescription: "ID of a message in the text or announcement channel to start the thread from. " +
					"The thread gets the message's ID, and a message can only have one thread. Changing it replaces the " +
					"thread. An imported thread adopts the configured value.",
				Optional:   true,
				Validators: []validator.String{snowflakeValidator()},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
						resp.RequiresReplace = !req.StateValue.IsNull()
					},
					"Changing message_id replaces the thread.",
					"Changing `message_id` replaces the thread.",
				)},
			},
			"message": schema.SingleNestedAttribute{
				MarkdownDescription: "Starter message of a forum or media post, required to create one. Edits are " +
					"applied in place. Attachments, components and stickers are not supported yet. An imported post " +
					"adopts the configured message on the next apply. Removing the argument stops managing the message.",
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"content": schema.StringAttribute{
						MarkdownDescription: "Message text (up to 2000 characters). At least one of `content` or `embeds` " +
							"is required.",
						Optional: true,
						Validators: []validator.String{
							stringvalidator.LengthBetween(1, 2000),
							stringvalidator.AtLeastOneOf(path.MatchRelative().AtParent().AtName("embeds")),
						},
					},
					"embeds":           embedsAttribute(),
					"allowed_mentions": allowedMentionsAttribute(),
				},
			},
			"auto_archive_duration": schema.Int64Attribute{
				MarkdownDescription: "Minutes of inactivity after which the thread stops showing in the channel list: " +
					"`60`, `1440`, `4320` or `10080`. Defaults to the channel's `default_auto_archive_duration`.",
				Optional:      true,
				Computed:      true,
				Validators:    []validator.Int64{int64validator.OneOf(autoArchiveMinutes...)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"rate_limit_per_user": schema.Int64Attribute{
				MarkdownDescription: "Slowmode: seconds a member must wait between messages, between `0` and `21600`. " +
					"Forum and media posts default to the channel's `default_thread_rate_limit_per_user`.",
				Optional:      true,
				Computed:      true,
				Validators:    []validator.Int64{int64validator.Between(0, 21600)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"archived": schema.BoolAttribute{
				MarkdownDescription: "Whether the thread is archived. Leave unset to let Discord archive it after " +
					"inactivity; set `false` to keep unarchiving it.",
				Optional: true,
				Computed: true,
			},
			"locked": schema.BoolAttribute{
				MarkdownDescription: "Whether the thread is locked, so only moderators can unarchive it or send messages. " +
					"Defaults to `false`.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"pinned": schema.BoolAttribute{
				MarkdownDescription: "Whether the post is pinned to the top of its forum or media channel. A pinned post " +
					"does not auto-archive, and archiving it unpins it. Conflicts with `archived = true`.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"applied_tags": schema.SetAttribute{
				MarkdownDescription: "IDs of the forum or media channel's tags applied to the post (at most 5), from " +
					"`discord_forum_channel.available_tags[*].id`.",
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Validators: []validator.Set{
					setvalidator.SizeAtMost(5),
					setvalidator.ValueStringsAre(snowflakeValidator()),
				},
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
			},
			"owner_id": schema.StringAttribute{
				MarkdownDescription: "ID of the user that created the thread (the bot).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *threadResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func isTrue(v types.Bool) bool { return !v.IsUnknown() && v.ValueBool() }

func isSet(v attr.Value) bool { return !v.IsNull() && !v.IsUnknown() }

func (r *threadResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m threadModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if isSet(m.Invitable) && !m.Private.IsUnknown() && !m.Private.ValueBool() {
		resp.Diagnostics.AddAttributeError(path.Root("invitable"), "Invalid argument", "invitable requires private = true.")
	}
	if isTrue(m.Private) {
		for name, v := range map[string]attr.Value{"message_id": m.MessageID, "message": m.Message, "applied_tags": m.AppliedTags, "pinned": m.Pinned} {
			if !v.IsNull() {
				resp.Diagnostics.AddAttributeError(path.Root(name), "Invalid argument",
					name+" cannot be set on a private thread: private threads are not started from a message and only exist in text channels.")
			}
		}
	}
	if !m.MessageID.IsNull() && !m.Message.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("message"), "Invalid argument",
			"message cannot be set with message_id: a forum or media post has its own starter message.")
	}
	if isTrue(m.Pinned) && isTrue(m.Archived) {
		resp.Diagnostics.AddAttributeError(path.Root("pinned"), "Invalid argument",
			"pinned cannot be true when archived is true: archiving a thread unpins it.")
	}
}

// checkParent validates the configuration against the type of the channel
// the thread is created in, which is only known from Discord.
func (m *threadModel) checkParent(parent *discord.Channel, diags *diag.Diagnostics) {
	forum := parent.Type == discord.ChannelTypeForum || parent.Type == discord.ChannelTypeMedia
	invalid := func(name, why string) {
		diags.AddAttributeError(path.Root(name), "Invalid argument", fmt.Sprintf("%s: channel %s %s.", name, parent.ID, why))
	}
	switch {
	case forum:
		if m.Message.IsNull() {
			invalid("message", "is a forum or media channel, whose posts need a starter message")
		}
		if !m.MessageID.IsNull() {
			invalid("message_id", "is a forum or media channel, where threads cannot be started from a message")
		}
		if m.Private.ValueBool() {
			invalid("private", "is a forum or media channel, which has no private threads")
		}
	case parent.Type == discord.ChannelTypeText, parent.Type == discord.ChannelTypeAnnouncement:
		for name, v := range map[string]attr.Value{"message": m.Message, "applied_tags": m.AppliedTags, "pinned": m.Pinned} {
			if isSet(v) {
				invalid(name, "is not a forum or media channel")
			}
		}
		if parent.Type == discord.ChannelTypeAnnouncement && m.Private.ValueBool() {
			invalid("private", "is an announcement channel, which has no private threads")
		}
	default:
		invalid("channel_id", "is not a text, announcement, forum or media channel")
	}
}

func (m *threadModel) starter(ctx context.Context, diags *diag.Diagnostics) *messageModel {
	if m.Message.IsNull() || m.Message.IsUnknown() {
		return &messageModel{Embeds: types.ListNull(types.ObjectType{AttrTypes: embedAttrTypes}), AllowedMentions: types.SetNull(types.StringType)}
	}
	var tm threadMessageModel
	diags.Append(m.Message.As(ctx, &tm, basetypes.ObjectAsOptions{})...)
	return tm.asMessage()
}

func (m *threadModel) setStarter(ctx context.Context, msg *discord.Message, diags *diag.Diagnostics) {
	mm := m.starter(ctx, diags)
	diags.Append(mm.apply(ctx, msg)...)
	obj, d := types.ObjectValueFrom(ctx, threadMessageAttrTypes, threadMessageModel{Content: mm.Content, Embeds: mm.Embeds, AllowedMentions: mm.AllowedMentions})
	diags.Append(d...)
	m.Message = obj
}

func (m *threadModel) apply(ctx context.Context, t *discord.Thread, diags *diag.Diagnostics) {
	m.ID = types.StringValue(t.ID)
	m.ServerID = types.StringValue(t.GuildID)
	if t.ParentID != nil {
		m.ChannelID = types.StringValue(*t.ParentID)
	}
	m.Name = types.StringValue(t.Name)
	m.Type = types.StringValue(threadTypes[t.Type])
	m.Private = types.BoolValue(t.Type == discord.ChannelTypePrivateThread)
	m.RateLimitPerUser = types.Int64Value(t.RateLimitPerUser)
	m.Pinned = types.BoolValue(t.Flags&discord.ChannelFlagPinned != 0)
	m.AppliedTags = stringSetValue(ctx, t.AppliedTags, diags)
	m.OwnerID = types.StringValue(t.OwnerID)
	meta := t.ThreadMetadata
	if meta == nil {
		meta = &discord.ThreadMetadata{}
	}
	m.Archived = types.BoolValue(meta.Archived)
	m.Locked = types.BoolValue(meta.Locked)
	m.AutoArchiveDuration = types.Int64Value(meta.AutoArchiveDuration)
	m.Invitable = types.BoolPointerValue(meta.Invitable)
}

func (r *threadResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan threadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	parent, err := r.client.GetChannel(ctx, plan.ChannelID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "read the thread's channel", err)
		return
	}
	plan.checkParent(parent, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	p := discord.Payload{"name": plan.Name.ValueString()}
	putKnownInt(p, "auto_archive_duration", plan.AutoArchiveDuration)
	putKnownInt(p, "rate_limit_per_user", plan.RateLimitPerUser)
	var t *discord.Thread
	switch {
	case !plan.Message.IsNull():
		if isSet(plan.AppliedTags) {
			var tags []string
			resp.Diagnostics.Append(plan.AppliedTags.ElementsAs(ctx, &tags, false)...)
			p["applied_tags"] = tags
		}
		msg, diags := plan.starter(ctx, &resp.Diagnostics).payload(ctx)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		p["message"] = msg
		t, err = r.client.StartThread(ctx, parent.ID, p)
	case !plan.MessageID.IsNull():
		t, err = r.client.StartThreadFromMessage(ctx, parent.ID, plan.MessageID.ValueString(), p)
	default:
		// Discord defaults the type to a private thread, so it is always sent.
		switch {
		case parent.Type == discord.ChannelTypeAnnouncement:
			p["type"] = discord.ChannelTypeAnnouncementThread
		case plan.Private.ValueBool():
			p["type"] = discord.ChannelTypePrivateThread
			putBool(p, "invitable", plan.Invitable)
		default:
			p["type"] = discord.ChannelTypePublicThread
		}
		t, err = r.client.StartThread(ctx, parent.ID, p)
	}
	if err != nil {
		apiError(&resp.Diagnostics, "create thread", err)
		return
	}

	// Locking, pinning and archiving are not Start Thread parameters.
	follow := discord.Payload{}
	if plan.Locked.ValueBool() {
		follow["locked"] = true
	}
	if isTrue(plan.Pinned) {
		follow["flags"] = t.Flags | discord.ChannelFlagPinned
	}
	if isTrue(plan.Archived) {
		follow["archived"] = true
	}
	if len(follow) > 0 {
		updated, err := r.client.ModifyThread(ctx, t.ID, follow)
		if err != nil {
			// Roll back so the failed create leaves no untracked thread.
			if delErr := r.client.DeleteChannel(ctx, t.ID); delErr != nil {
				err = fmt.Errorf("%w (and deleting the new thread %s failed: %w)", err, t.ID, delErr)
			}
			apiError(&resp.Diagnostics, "update new thread", err)
			return
		}
		t = updated
	}
	plan.apply(ctx, t, &resp.Diagnostics)
	if !plan.Message.IsNull() {
		msg, err := r.client.GetMessage(ctx, t.ID, t.ID)
		if err != nil {
			apiError(&resp.Diagnostics, "read starter message", err)
			return
		}
		plan.setStarter(ctx, msg, &resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *threadResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state threadModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t, err := r.client.GetThread(ctx, state.ID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read thread", err)
		return
	}
	if _, ok := threadTypes[t.Type]; !ok {
		resp.Diagnostics.AddError("Not a thread", fmt.Sprintf("Channel %s has type %d, which is not a thread.", t.ID, t.Type))
		return
	}
	state.apply(ctx, t, &resp.Diagnostics)
	if !state.Message.IsNull() {
		msg, err := r.client.GetMessage(ctx, t.ID, t.ID)
		switch {
		case discord.IsNotFound(err):
			// A deleted starter message cannot be posted again; the plan
			// shows it, and editing it reports the error.
			state.Message = types.ObjectNull(threadMessageAttrTypes)
		case err != nil:
			apiError(&resp.Diagnostics, "read starter message", err)
			return
		default:
			state.setStarter(ctx, msg, &resp.Diagnostics)
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// threadChanges returns the Modify Channel fields that differ between the
// plan and the thread as Discord has it.
func (m *threadModel) threadChanges(ctx context.Context, t *discord.Thread, diags *diag.Diagnostics) discord.Payload {
	body := discord.Payload{}
	meta := t.ThreadMetadata
	if meta == nil {
		meta = &discord.ThreadMetadata{}
	}
	if m.Name.ValueString() != t.Name {
		body["name"] = m.Name.ValueString()
	}
	if isSet(m.AutoArchiveDuration) && m.AutoArchiveDuration.ValueInt64() != meta.AutoArchiveDuration {
		body["auto_archive_duration"] = m.AutoArchiveDuration.ValueInt64()
	}
	if isSet(m.RateLimitPerUser) && m.RateLimitPerUser.ValueInt64() != t.RateLimitPerUser {
		body["rate_limit_per_user"] = m.RateLimitPerUser.ValueInt64()
	}
	if isSet(m.Locked) && m.Locked.ValueBool() != meta.Locked {
		body["locked"] = m.Locked.ValueBool()
	}
	if isSet(m.Invitable) && t.Type == discord.ChannelTypePrivateThread && (meta.Invitable == nil || *meta.Invitable != m.Invitable.ValueBool()) {
		body["invitable"] = m.Invitable.ValueBool()
	}
	if isSet(m.Pinned) && m.Pinned.ValueBool() != (t.Flags&discord.ChannelFlagPinned != 0) {
		body["flags"] = t.Flags ^ discord.ChannelFlagPinned
	}
	if isSet(m.AppliedTags) {
		var tags []string
		diags.Append(m.AppliedTags.ElementsAs(ctx, &tags, false)...)
		current := slices.Clone(t.AppliedTags)
		slices.Sort(tags)
		slices.Sort(current)
		if !slices.Equal(tags, current) {
			body["applied_tags"] = tags
		}
	}
	return body
}

func (r *threadResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan, state threadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	id := state.ID.ValueString()
	t, err := r.client.GetThread(ctx, id)
	if err != nil {
		apiError(&resp.Diagnostics, "read thread", err)
		return
	}
	body := plan.threadChanges(ctx, t, &resp.Diagnostics)

	var msgBody discord.Payload
	if !plan.Message.IsNull() {
		desired, diags := plan.starter(ctx, &resp.Diagnostics).payload(ctx)
		resp.Diagnostics.Append(diags...)
		current, diags := state.starter(ctx, &resp.Diagnostics).payload(ctx)
		resp.Diagnostics.Append(diags...)
		msgBody = diffPayload(desired, current)
		_, contentChanged := msgBody["content"]
		_, embedsChanged := msgBody["embeds"]
		if !contentChanged && !embedsChanged {
			msgBody = nil
		} else {
			// Without allowed_mentions, Discord parses mentions in edited
			// content with its defaults and pings @everyone and roles.
			msgBody["allowed_mentions"] = desired["allowed_mentions"]
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	// An archived thread accepts changes only in a request that unarchives
	// it, and its messages cannot be edited, so it is unarchived first and
	// archived again afterwards when archived = true is set.
	archived := t.ThreadMetadata != nil && t.ThreadMetadata.Archived
	if len(body) > 0 || msgBody != nil {
		switch {
		case archived:
			body["archived"] = false
		case isSet(plan.Archived) && msgBody == nil:
			body["archived"] = plan.Archived.ValueBool()
		}
		if len(body) > 0 {
			if t, err = r.client.ModifyThread(ctx, id, body); err != nil {
				apiError(&resp.Diagnostics, "update thread", err)
				return
			}
		}
		if msgBody != nil {
			msg, err := r.client.EditMessage(ctx, id, id, msgBody)
			if err != nil {
				apiError(&resp.Diagnostics, "edit starter message", err)
				return
			}
			plan.setStarter(ctx, msg, &resp.Diagnostics)
		}
	}
	if isSet(plan.Archived) && t.ThreadMetadata != nil && t.ThreadMetadata.Archived != plan.Archived.ValueBool() {
		if t, err = r.client.ModifyThread(ctx, id, discord.Payload{"archived": plan.Archived.ValueBool()}); err != nil {
			apiError(&resp.Diagnostics, "archive thread", err)
			return
		}
	}

	message := plan.Message
	plan.apply(ctx, t, &resp.Diagnostics)
	plan.Message = message
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *threadResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state threadModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	err := r.client.DeleteChannel(ctx, state.ID.ValueString())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete thread", err)
	}
}

func (r *threadResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

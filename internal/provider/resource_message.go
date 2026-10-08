package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure   = &messageResource{}
	_ resource.ResourceWithImportState = &messageResource{}
)

type messageResource struct {
	client *discord.Client
}

type messageModel struct {
	ID              types.String `tfsdk:"id"`
	ChannelID       types.String `tfsdk:"channel_id"`
	Content         types.String `tfsdk:"content"`
	Embeds          types.List   `tfsdk:"embeds"`
	Pinned          types.Bool   `tfsdk:"pinned"`
	AllowedMentions types.Set    `tfsdk:"allowed_mentions"`
	AuthorID        types.String `tfsdk:"author_id"`
	AuditLogReason  types.String `tfsdk:"audit_log_reason"`
}

type embedModel struct {
	Title         types.String `tfsdk:"title"`
	Description   types.String `tfsdk:"description"`
	URL           types.String `tfsdk:"url"`
	Color         types.Int64  `tfsdk:"color"`
	FooterText    types.String `tfsdk:"footer_text"`
	FooterIconURL types.String `tfsdk:"footer_icon_url"`
	ImageURL      types.String `tfsdk:"image_url"`
	ThumbnailURL  types.String `tfsdk:"thumbnail_url"`
	AuthorName    types.String `tfsdk:"author_name"`
	AuthorURL     types.String `tfsdk:"author_url"`
	AuthorIconURL types.String `tfsdk:"author_icon_url"`
	Fields        types.List   `tfsdk:"fields"`
}

type embedFieldModel struct {
	Name   types.String `tfsdk:"name"`
	Value  types.String `tfsdk:"value"`
	Inline types.Bool   `tfsdk:"inline"`
}

var (
	embedFieldAttrTypes = map[string]attr.Type{"name": types.StringType, "value": types.StringType, "inline": types.BoolType}
	embedAttrTypes      = map[string]attr.Type{
		"title": types.StringType, "description": types.StringType, "url": types.StringType, "color": types.Int64Type,
		"footer_text": types.StringType, "footer_icon_url": types.StringType, "image_url": types.StringType,
		"thumbnail_url": types.StringType, "author_name": types.StringType, "author_url": types.StringType,
		"author_icon_url": types.StringType, "fields": types.ListType{ElemType: types.ObjectType{AttrTypes: embedFieldAttrTypes}},
	}
)

func newMessageResource() resource.Resource { return &messageResource{} }

func (r *messageResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_message"
}

func optionalString(desc string, maxLen int) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Optional:            true,
		Validators:          []validator.String{stringvalidator.LengthBetween(1, maxLen)},
	}
}

func (r *messageResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a message posted by the bot, such as a rules or welcome message. Edits are applied " +
			"in place. If the message is deleted outside Terraform it is posted again on the next apply.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttributeFor("pinning, unpinning and deleting the message " +
				"(Discord records no reason for posting or editing it)"),
			"id": idAttribute("Message ID."),
			"channel_id": schema.StringAttribute{
				MarkdownDescription: "ID of the channel to post in.",
				Required:            true,
				Validators:          []validator.String{snowflakeValidator()},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"content": schema.StringAttribute{
				MarkdownDescription: "Message text (up to 2000 characters). At least one of `content` or `embeds` is required.",
				Optional:            true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 2000),
					stringvalidator.AtLeastOneOf(path.MatchRoot("embeds")),
				},
			},
			"embeds": embedsAttribute(),
			"pinned": schema.BoolAttribute{
				MarkdownDescription: "Whether the message is pinned. Requires the Pin Messages permission. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"allowed_mentions": allowedMentionsAttribute(),
			"author_id": schema.StringAttribute{
				MarkdownDescription: "ID of the user that posted the message (the bot).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// embedsAttribute is the rich embeds of a message, shared with the starter
// message of discord_thread.
func embedsAttribute() schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		MarkdownDescription: "Rich embeds (at most 10, 6000 characters in total).",
		Optional:            true,
		Validators:          []validator.List{listvalidator.SizeBetween(1, 10)},
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"title":       optionalString("Title (up to 256 characters).", 256),
				"description": optionalString("Description (up to 4096 characters).", 4096),
				"url":         optionalString("URL the title links to.", 2048),
				"color": schema.Int64Attribute{
					MarkdownDescription: "RGB color of the left border. Use `provider::discord::color()` to convert hex.",
					Optional:            true,
					Validators:          []validator.Int64{int64validator.Between(0, 0xFFFFFF)},
				},
				"footer_text": optionalString("Footer text (up to 2048 characters).", 2048),
				"footer_icon_url": schema.StringAttribute{
					MarkdownDescription: "Footer icon URL. Requires `footer_text`.",
					Optional:            true,
					Validators: []validator.String{
						stringvalidator.LengthBetween(1, 2048),
						stringvalidator.AlsoRequires(path.MatchRelative().AtParent().AtName("footer_text")),
					},
				},
				"image_url":     optionalString("Image URL.", 2048),
				"thumbnail_url": optionalString("Thumbnail URL.", 2048),
				"author_name":   optionalString("Author name (up to 256 characters).", 256),
				"author_url": schema.StringAttribute{
					MarkdownDescription: "URL the author name links to. Requires `author_name`.",
					Optional:            true,
					Validators: []validator.String{
						stringvalidator.LengthBetween(1, 2048),
						stringvalidator.AlsoRequires(path.MatchRelative().AtParent().AtName("author_name")),
					},
				},
				"author_icon_url": schema.StringAttribute{
					MarkdownDescription: "Author icon URL. Requires `author_name`.",
					Optional:            true,
					Validators: []validator.String{
						stringvalidator.LengthBetween(1, 2048),
						stringvalidator.AlsoRequires(path.MatchRelative().AtParent().AtName("author_name")),
					},
				},
				"fields": schema.ListNestedAttribute{
					MarkdownDescription: "Fields (at most 25).",
					Optional:            true,
					Validators:          []validator.List{listvalidator.SizeBetween(1, 25)},
					NestedObject: schema.NestedAttributeObject{
						Attributes: map[string]schema.Attribute{
							"name": schema.StringAttribute{
								MarkdownDescription: "Field name (up to 256 characters).",
								Required:            true,
								Validators:          []validator.String{stringvalidator.LengthBetween(1, 256)},
							},
							"value": schema.StringAttribute{
								MarkdownDescription: "Field value (up to 1024 characters).",
								Required:            true,
								Validators:          []validator.String{stringvalidator.LengthBetween(1, 1024)},
							},
							"inline": schema.BoolAttribute{
								MarkdownDescription: "Whether the field is displayed inline. Defaults to `false`.",
								Optional:            true,
								Computed:            true,
								Default:             booldefault.StaticBool(false),
							},
						},
					},
				},
			},
		},
	}
}

func allowedMentionsAttribute() schema.SetAttribute {
	return schema.SetAttribute{
		MarkdownDescription: "Mention types that notify people: any of `roles`, `users` and `everyone`. " +
			"Defaults to none, so posting or editing the message never pings anyone.",
		ElementType: types.StringType,
		Optional:    true,
		Validators:  []validator.Set{setvalidator.ValueStringsAre(stringvalidator.OneOf("roles", "users", "everyone"))},
	}
}

func (r *messageResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *messageModel) payload(ctx context.Context) (discord.Payload, diag.Diagnostics) {
	var diags diag.Diagnostics
	p := discord.Payload{"content": m.Content.ValueString()}

	var embeds []embedModel
	if !m.Embeds.IsNull() {
		diags.Append(m.Embeds.ElementsAs(ctx, &embeds, false)...)
	}
	out := make([]discord.Embed, 0, len(embeds))
	for _, e := range embeds {
		embed := discord.Embed{
			Title:       e.Title.ValueString(),
			Description: e.Description.ValueString(),
			URL:         e.URL.ValueString(),
			Color:       e.Color.ValueInt64Pointer(),
		}
		if !e.FooterText.IsNull() {
			embed.Footer = &discord.EmbedFooter{Text: e.FooterText.ValueString(), IconURL: e.FooterIconURL.ValueString()}
		}
		if !e.ImageURL.IsNull() {
			embed.Image = &discord.EmbedMedia{URL: e.ImageURL.ValueString()}
		}
		if !e.ThumbnailURL.IsNull() {
			embed.Thumbnail = &discord.EmbedMedia{URL: e.ThumbnailURL.ValueString()}
		}
		if !e.AuthorName.IsNull() {
			embed.Author = &discord.EmbedAuthor{Name: e.AuthorName.ValueString(), URL: e.AuthorURL.ValueString(), IconURL: e.AuthorIconURL.ValueString()}
		}
		var fields []embedFieldModel
		if !e.Fields.IsNull() {
			diags.Append(e.Fields.ElementsAs(ctx, &fields, false)...)
		}
		for _, f := range fields {
			embed.Fields = append(embed.Fields, discord.EmbedField{Name: f.Name.ValueString(), Value: f.Value.ValueString(), Inline: f.Inline.ValueBool()})
		}
		out = append(out, embed)
	}
	p["embeds"] = out

	parse := []string{}
	if !m.AllowedMentions.IsNull() {
		diags.Append(m.AllowedMentions.ElementsAs(ctx, &parse, false)...)
	}
	p["allowed_mentions"] = map[string]any{"parse": parse}
	return p, diags
}

func textValue(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// keepText returns the prior value when Discord's copy differs from it only by
// the leading and trailing whitespace Discord trims, e.g. from a heredoc.
func keepText(prior types.String, returned string) types.String {
	if !prior.IsNull() && !prior.IsUnknown() && strings.TrimSpace(prior.ValueString()) == returned {
		return prior
	}
	return textValue(returned)
}

func (m *messageModel) apply(ctx context.Context, msg *discord.Message) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = types.StringValue(msg.ID)
	m.ChannelID = types.StringValue(msg.ChannelID)
	m.Content = keepText(m.Content, msg.Content)
	m.Pinned = types.BoolValue(msg.Pinned)
	if msg.Author != nil {
		m.AuthorID = types.StringValue(msg.Author.ID)
	}

	var prior []embedModel
	if !m.Embeds.IsNull() && !m.Embeds.IsUnknown() {
		diags.Append(m.Embeds.ElementsAs(ctx, &prior, false)...)
	}
	// Link previews Discord generates from the content are not managed.
	var rich []discord.Embed
	for _, e := range msg.Embeds {
		if e.Type == "" || e.Type == "rich" {
			rich = append(rich, e)
		}
	}
	embedType := types.ObjectType{AttrTypes: embedAttrTypes}
	if len(rich) == 0 {
		m.Embeds = types.ListNull(embedType)
		return diags
	}
	embeds := make([]embedModel, 0, len(rich))
	for i, e := range rich {
		var p embedModel
		if i < len(prior) {
			p = prior[i]
		}
		var priorFields []embedFieldModel
		if !p.Fields.IsNull() && !p.Fields.IsUnknown() {
			diags.Append(p.Fields.ElementsAs(ctx, &priorFields, false)...)
		}
		em := embedModel{
			Title:         keepText(p.Title, e.Title),
			Description:   keepText(p.Description, e.Description),
			URL:           textValue(e.URL),
			Color:         types.Int64PointerValue(e.Color),
			FooterText:    types.StringNull(),
			FooterIconURL: types.StringNull(),
			ImageURL:      types.StringNull(),
			ThumbnailURL:  types.StringNull(),
			AuthorName:    types.StringNull(),
			AuthorURL:     types.StringNull(),
			AuthorIconURL: types.StringNull(),
			Fields:        types.ListNull(types.ObjectType{AttrTypes: embedFieldAttrTypes}),
		}
		if e.Footer != nil {
			em.FooterText, em.FooterIconURL = keepText(p.FooterText, e.Footer.Text), textValue(e.Footer.IconURL)
		}
		if e.Image != nil {
			em.ImageURL = textValue(e.Image.URL)
		}
		if e.Thumbnail != nil {
			em.ThumbnailURL = textValue(e.Thumbnail.URL)
		}
		if e.Author != nil {
			em.AuthorName, em.AuthorURL, em.AuthorIconURL = keepText(p.AuthorName, e.Author.Name), textValue(e.Author.URL), textValue(e.Author.IconURL)
		}
		if len(e.Fields) > 0 {
			fields := make([]embedFieldModel, 0, len(e.Fields))
			for j, f := range e.Fields {
				var pf embedFieldModel
				if j < len(priorFields) {
					pf = priorFields[j]
				}
				fields = append(fields, embedFieldModel{Name: keepText(pf.Name, f.Name), Value: keepText(pf.Value, f.Value), Inline: types.BoolValue(f.Inline)})
			}
			list, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: embedFieldAttrTypes}, fields)
			diags.Append(d...)
			em.Fields = list
		}
		embeds = append(embeds, em)
	}
	list, d := types.ListValueFrom(ctx, embedType, embeds)
	diags.Append(d...)
	m.Embeds = list
	return diags
}

func (r *messageResource) setPinned(ctx context.Context, m *messageModel, pinned bool) error {
	if pinned {
		return r.client.PinMessage(ctx, m.ChannelID.ValueString(), m.ID.ValueString())
	}
	return r.client.UnpinMessage(ctx, m.ChannelID.ValueString(), m.ID.ValueString())
}

func (r *messageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan messageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	p, diags := plan.payload(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	msg, err := r.client.CreateMessage(ctx, plan.ChannelID.ValueString(), p)
	if err != nil {
		apiError(&resp.Diagnostics, "create message", err)
		return
	}
	pinned := plan.Pinned.ValueBool()
	resp.Diagnostics.Append(plan.apply(ctx, msg)...)
	if pinned {
		if err := r.setPinned(ctx, &plan, true); err != nil {
			// Roll back so the failed create leaves no untracked message.
			if delErr := r.client.DeleteMessage(ctx, plan.ChannelID.ValueString(), plan.ID.ValueString()); delErr != nil {
				err = fmt.Errorf("%w (and deleting the unpinned message %s failed: %w)", err, plan.ID.ValueString(), delErr)
			}
			apiError(&resp.Diagnostics, "pin message", err)
			return
		}
		plan.Pinned = types.BoolValue(true)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *messageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state messageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	msg, err := r.client.GetMessage(ctx, state.ChannelID.ValueString(), state.ID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read message", err)
		return
	}
	resp.Diagnostics.Append(state.apply(ctx, msg)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *messageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan, state messageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	plan.ID = state.ID
	desired, diags := plan.payload(ctx)
	resp.Diagnostics.Append(diags...)
	current, diags := state.payload(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	pinned := plan.Pinned.ValueBool()
	body := diffPayload(desired, current)
	// Without allowed_mentions, Discord parses mentions in edited content
	// with its defaults and pings @everyone and roles.
	_, contentChanged := body["content"]
	_, embedsChanged := body["embeds"]
	if contentChanged || embedsChanged {
		body["allowed_mentions"] = desired["allowed_mentions"]
	}
	msg, err := r.client.EditMessage(ctx, state.ChannelID.ValueString(), state.ID.ValueString(), body)
	if err != nil {
		apiError(&resp.Diagnostics, "edit message", err)
		return
	}
	resp.Diagnostics.Append(plan.apply(ctx, msg)...)
	if pinned != state.Pinned.ValueBool() {
		if err := r.setPinned(ctx, &plan, pinned); err != nil {
			apiError(&resp.Diagnostics, "change message pin", err)
			return
		}
	}
	plan.Pinned = types.BoolValue(pinned)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *messageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state messageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	err := r.client.DeleteMessage(ctx, state.ChannelID.ValueString(), state.ID.ValueString())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete message", err)
	}
}

func (r *messageResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitID(req.ID, 2, "channel_id/message_id")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("channel_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}

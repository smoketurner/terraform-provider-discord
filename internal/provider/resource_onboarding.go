package provider

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	onboardingModes = enumMapping{"default", "advanced"}
	promptTypes     = enumMapping{"multiple_choice", "dropdown"}
)

// minOnboardingChannels is the number of channels Discord requires before
// onboarding can be enabled.
const minOnboardingChannels = 7

// discordEpoch is the first millisecond of 2015, the epoch of snowflake IDs.
const discordEpoch = 1420070400000

var (
	_ resource.ResourceWithConfigure      = &onboardingResource{}
	_ resource.ResourceWithImportState    = &onboardingResource{}
	_ resource.ResourceWithIdentity       = &onboardingResource{}
	_ resource.ResourceWithModifyPlan     = &onboardingResource{}
	_ resource.ResourceWithValidateConfig = &onboardingResource{}
)

type onboardingResource struct {
	resourceIdentity
	client *discord.Client
}

type onboardingModel struct {
	ID                types.String `tfsdk:"id"`
	ServerID          types.String `tfsdk:"server_id"`
	Enabled           types.Bool   `tfsdk:"enabled"`
	Mode              types.String `tfsdk:"mode"`
	DefaultChannelIDs types.Set    `tfsdk:"default_channel_ids"`
	Prompts           types.List   `tfsdk:"prompts"`
	AuditLogReason    types.String `tfsdk:"audit_log_reason"`
}

type onboardingPromptModel struct {
	ID           types.String `tfsdk:"id"`
	Title        types.String `tfsdk:"title"`
	Type         types.String `tfsdk:"type"`
	SingleSelect types.Bool   `tfsdk:"single_select"`
	Required     types.Bool   `tfsdk:"required"`
	InOnboarding types.Bool   `tfsdk:"in_onboarding"`
	Options      types.List   `tfsdk:"options"`
}

type onboardingOptionModel struct {
	ID            types.String `tfsdk:"id"`
	Title         types.String `tfsdk:"title"`
	Description   types.String `tfsdk:"description"`
	EmojiID       types.String `tfsdk:"emoji_id"`
	EmojiName     types.String `tfsdk:"emoji_name"`
	EmojiAnimated types.Bool   `tfsdk:"emoji_animated"`
	RoleIDs       types.Set    `tfsdk:"role_ids"`
	ChannelIDs    types.Set    `tfsdk:"channel_ids"`
}

var (
	onboardingOptionAttrTypes = map[string]attr.Type{
		"id": types.StringType, "title": types.StringType, "description": types.StringType,
		"emoji_id": types.StringType, "emoji_name": types.StringType, "emoji_animated": types.BoolType,
		"role_ids": types.SetType{ElemType: types.StringType}, "channel_ids": types.SetType{ElemType: types.StringType},
	}
	onboardingPromptAttrTypes = map[string]attr.Type{
		"id": types.StringType, "title": types.StringType, "type": types.StringType,
		"single_select": types.BoolType, "required": types.BoolType, "in_onboarding": types.BoolType,
		"options": types.ListType{ElemType: types.ObjectType{AttrTypes: onboardingOptionAttrTypes}},
	}
)

func newOnboardingResource() resource.Resource {
	return &onboardingResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{serverIdentity("server_id", "id")}}}
}

func (r *onboardingResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_onboarding"
}

func snowflakeSet(desc string, maxItems int) schema.SetAttribute {
	return schema.SetAttribute{
		MarkdownDescription: desc,
		ElementType:         types.StringType,
		Optional:            true,
		Validators: []validator.Set{
			setvalidator.SizeAtMost(maxItems),
			setvalidator.ValueStringsAre(snowflakeValidator()),
		},
	}
}

func (r *onboardingResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Community server's onboarding: the questions new members answer and the channels " +
			"they are added to. Discord replaces the whole configuration on every change, so prompts and default channels " +
			"missing from the configuration are removed. Destroying this resource disables onboarding and leaves its " +
			"prompts and default channels unchanged. Requires the Manage Server and Manage Roles permissions.\n\n" +
			"While onboarding is enabled, Discord requires at least 7 default channels, at least 5 of which `@everyone` " +
			"can send messages in. In `advanced` mode, channels added by prompt options count too.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttribute(),
			"id":               idAttribute("Server ID."),
			"server_id":        serverIDAttribute(),
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether onboarding is enabled.",
				Required:            true,
			},
			"mode": schema.StringAttribute{
				MarkdownDescription: "Which channels count towards the requirements for enabling onboarding: " +
					"`default` counts default channels only, `advanced` also counts channels added by prompt options. " +
					"Defaults to `default`.",
				Optional:   true,
				Computed:   true,
				Default:    stringdefault.StaticString("default"),
				Validators: []validator.String{onboardingModes.validator()},
			},
			"default_channel_ids": snowflakeSet("Channels every new member is added to (at most 500).", 500),
			"prompts": schema.ListNestedAttribute{
				MarkdownDescription: "Questions shown during onboarding and in Channels & Roles, in order (at most 15). " +
					"Prompts are matched by title, and options by title within their prompt, so reordering keeps their IDs; " +
					"renaming one replaces it.",
				Optional:   true,
				Validators: []validator.List{listvalidator.SizeAtMost(15)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							MarkdownDescription: "Prompt ID assigned by Discord.",
							Computed:            true,
						},
						"title": schema.StringAttribute{
							MarkdownDescription: "Question shown to members (up to 100 characters).",
							Required:            true,
							Validators:          []validator.String{stringvalidator.LengthBetween(1, 100)},
						},
						"type": schema.StringAttribute{
							MarkdownDescription: "How options are shown: " + promptTypes.doc() + ". Defaults to `multiple_choice`.",
							Optional:            true,
							Computed:            true,
							Default:             stringdefault.StaticString("multiple_choice"),
							Validators:          []validator.String{promptTypes.validator()},
						},
						"single_select": schema.BoolAttribute{
							MarkdownDescription: "Whether members can choose only one option. Defaults to `false`.",
							Optional:            true,
							Computed:            true,
							Default:             booldefault.StaticBool(false),
						},
						"required": schema.BoolAttribute{
							MarkdownDescription: "Whether members must answer before finishing onboarding. Defaults to `false`.",
							Optional:            true,
							Computed:            true,
							Default:             booldefault.StaticBool(false),
						},
						"in_onboarding": schema.BoolAttribute{
							MarkdownDescription: "Whether the prompt is part of onboarding. When `false` it appears only in " +
								"Channels & Roles. Defaults to `true`.",
							Optional: true,
							Computed: true,
							Default:  booldefault.StaticBool(true),
						},
						"options": schema.ListNestedAttribute{
							MarkdownDescription: "Answers members can choose, in order (1 to 50).",
							Required:            true,
							Validators:          []validator.List{listvalidator.SizeBetween(1, 50)},
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"id": schema.StringAttribute{
										MarkdownDescription: "Option ID assigned by Discord.",
										Computed:            true,
									},
									"title": schema.StringAttribute{
										MarkdownDescription: "Option title (up to 50 characters).",
										Required:            true,
										Validators:          []validator.String{stringvalidator.LengthBetween(1, 50)},
									},
									"description": schema.StringAttribute{
										MarkdownDescription: "Option description (up to 100 characters).",
										Optional:            true,
										Validators:          []validator.String{stringvalidator.LengthBetween(1, 100)},
									},
									"emoji_id": schema.StringAttribute{
										MarkdownDescription: "ID of a custom emoji shown with the option.",
										Optional:            true,
										Validators:          []validator.String{snowflakeValidator()},
									},
									"emoji_name": schema.StringAttribute{
										MarkdownDescription: "Unicode emoji shown with the option, or the name of the custom emoji in `emoji_id`.",
										Optional:            true,
										Validators:          []validator.String{stringvalidator.LengthBetween(1, 100)},
									},
									"emoji_animated": schema.BoolAttribute{
										MarkdownDescription: "Whether the custom emoji in `emoji_id` is animated.",
										Optional:            true,
									},
									"role_ids":    snowflakeSet("Roles given to members who choose the option (at most 50).", 50),
									"channel_ids": snowflakeSet("Channels members who choose the option are added to (at most 50).", 50),
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *onboardingResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

// ValidateConfig checks the channel requirement Discord enforces while
// onboarding is enabled, when every value it depends on is known. Whether
// @everyone can send messages in the channels is left to Discord.
func (r *onboardingResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m onboardingModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !m.Enabled.ValueBool() || m.Mode.IsUnknown() {
		return
	}
	channels := map[string]bool{}
	if !addKnownIDs(channels, m.DefaultChannelIDs) {
		return
	}
	if m.Mode.ValueString() == "advanced" {
		if m.Prompts.IsUnknown() {
			return
		}
		var prompts []onboardingPromptModel
		resp.Diagnostics.Append(m.Prompts.ElementsAs(ctx, &prompts, false)...)
		for _, p := range prompts {
			if p.Options.IsUnknown() {
				return
			}
			var options []onboardingOptionModel
			resp.Diagnostics.Append(p.Options.ElementsAs(ctx, &options, false)...)
			for _, o := range options {
				if !addKnownIDs(channels, o.ChannelIDs) {
					return
				}
			}
		}
	}
	if len(channels) < minOnboardingChannels {
		resp.Diagnostics.AddAttributeError(path.Root("default_channel_ids"), "Too few onboarding channels",
			fmt.Sprintf("Enabling onboarding requires at least %d channels; %d are configured. In advanced mode, "+
				"channels of prompt options count too.", minOnboardingChannels, len(channels)))
	}
}

// addKnownIDs adds the elements of ids to set and reports whether all of
// them were known.
func addKnownIDs(set map[string]bool, ids types.Set) bool {
	if ids.IsUnknown() {
		return false
	}
	for _, id := range ids.Elements() {
		if id.IsUnknown() {
			return false
		}
		set[id.String()] = true
	}
	return true
}

// ModifyPlan gives each planned prompt the ID of the prior prompt with the
// same title, and each option the ID of the prior option with the same title
// in that prompt; the rest are new and left unknown. Without this, Terraform
// pairs prompts and options by list index, so reordering them would rewrite
// every prompt and lose members' answers.
func (r *onboardingResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var planned, prior types.List
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("prompts"), &planned)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("prompts"), &prior)...)
	if resp.Diagnostics.HasError() || planned.IsNull() || planned.IsUnknown() {
		return
	}
	var plannedPrompts, priorPrompts []onboardingPromptModel
	resp.Diagnostics.Append(planned.ElementsAs(ctx, &plannedPrompts, false)...)
	resp.Diagnostics.Append(prior.ElementsAs(ctx, &priorPrompts, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	unmatchedPrompts := slices.Clone(priorPrompts)
	for i := range plannedPrompts {
		p := &plannedPrompts[i]
		match, ok := takeByTitle(&unmatchedPrompts, p.Title, func(m onboardingPromptModel) types.String { return m.Title })
		p.ID = types.StringUnknown()
		if ok {
			p.ID = match.ID
		}
		if p.Options.IsUnknown() {
			continue
		}
		var options, priorOptions []onboardingOptionModel
		resp.Diagnostics.Append(p.Options.ElementsAs(ctx, &options, false)...)
		if ok {
			resp.Diagnostics.Append(match.Options.ElementsAs(ctx, &priorOptions, false)...)
		}
		for j := range options {
			o, found := takeByTitle(&priorOptions, options[j].Title, func(m onboardingOptionModel) types.String { return m.Title })
			options[j].ID = types.StringUnknown()
			if found {
				options[j].ID = o.ID
			}
		}
		list, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: onboardingOptionAttrTypes}, options)
		resp.Diagnostics.Append(d...)
		p.Options = list
	}
	list, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: onboardingPromptAttrTypes}, plannedPrompts)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("prompts"), list)...)
}

// takeByTitle removes and returns the first element of items whose title
// equals title. Duplicate titles are matched in order.
func takeByTitle[T any](items *[]T, title types.String, titleOf func(T) types.String) (T, bool) {
	for i, item := range *items {
		if titleOf(item).Equal(title) {
			*items = append((*items)[:i:i], (*items)[i+1:]...)
			return item, true
		}
	}
	var zero T
	return zero, false
}

// placeholderPromptID returns an ID for a new prompt. Discord requires an ID
// on every prompt and rejects invalid ones such as "0", so new prompts get a
// snowflake for the current time, made unique by seq; Discord's response
// carries the IDs to keep.
func placeholderPromptID(now time.Time, seq int) string {
	return strconv.FormatInt((now.UnixMilli()-discordEpoch)<<22|int64(seq), 10)
}

func setStrings(ctx context.Context, s types.Set, diags *diag.Diagnostics) []string {
	out := []string{}
	diags.Append(s.ElementsAs(ctx, &out, false)...)
	return out
}

func (m *onboardingModel) payload(ctx context.Context, now time.Time) (discord.Payload, diag.Diagnostics) {
	var diags diag.Diagnostics
	var prompts []onboardingPromptModel
	diags.Append(m.Prompts.ElementsAs(ctx, &prompts, false)...)
	out := make([]discord.Payload, 0, len(prompts))
	for i, p := range prompts {
		id := p.ID.ValueString()
		if p.ID.IsUnknown() || p.ID.IsNull() {
			id = placeholderPromptID(now, i)
		}
		var options []onboardingOptionModel
		diags.Append(p.Options.ElementsAs(ctx, &options, false)...)
		opts := make([]discord.Payload, 0, len(options))
		for _, o := range options {
			opt := discord.Payload{
				"title":       o.Title.ValueString(),
				"description": o.Description.ValueStringPointer(),
				"emoji_id":    o.EmojiID.ValueStringPointer(),
				"emoji_name":  o.EmojiName.ValueStringPointer(),
				"role_ids":    setStrings(ctx, o.RoleIDs, &diags),
				"channel_ids": setStrings(ctx, o.ChannelIDs, &diags),
			}
			putKnownString(opt, "id", o.ID)
			putBool(opt, "emoji_animated", o.EmojiAnimated)
			opts = append(opts, opt)
		}
		prompt := discord.Payload{
			"id":            id,
			"title":         p.Title.ValueString(),
			"options":       opts,
			"single_select": p.SingleSelect.ValueBool(),
			"required":      p.Required.ValueBool(),
			"in_onboarding": p.InOnboarding.ValueBool(),
		}
		promptTypes.put(prompt, "type", p.Type)
		out = append(out, prompt)
	}
	pl := discord.Payload{
		"prompts":             out,
		"default_channel_ids": setStrings(ctx, m.DefaultChannelIDs, &diags),
	}
	putBool(pl, "enabled", m.Enabled)
	onboardingModes.put(pl, "mode", m.Mode)
	return pl, diags
}

// optionalSet stores ids, or keeps null when there are none and the prior
// value was null, so an omitted argument does not plan a change.
func optionalSet(ctx context.Context, prior types.Set, ids []string, diags *diag.Diagnostics) types.Set {
	if len(ids) == 0 && (prior.IsNull() || prior.IsUnknown()) {
		return types.SetNull(types.StringType)
	}
	return stringSetValue(ctx, ids, diags)
}

// apply stores the onboarding read from Discord, using the prior values of
// the matching prompt and option, by title, to keep omitted arguments null.
func (m *onboardingModel) apply(ctx context.Context, o *discord.Onboarding) diag.Diagnostics {
	var diags diag.Diagnostics
	var priorPrompts []onboardingPromptModel
	if !m.Prompts.IsUnknown() {
		diags.Append(m.Prompts.ElementsAs(ctx, &priorPrompts, false)...)
	}
	prompts := make([]onboardingPromptModel, 0, len(o.Prompts))
	for _, p := range o.Prompts {
		priorPrompt, _ := takeByTitle(&priorPrompts, types.StringValue(p.Title), func(m onboardingPromptModel) types.String { return m.Title })
		var priorOptions []onboardingOptionModel
		if !priorPrompt.Options.IsNull() && !priorPrompt.Options.IsUnknown() {
			diags.Append(priorPrompt.Options.ElementsAs(ctx, &priorOptions, false)...)
		}
		options := make([]onboardingOptionModel, 0, len(p.Options))
		for _, opt := range p.Options {
			prior, _ := takeByTitle(&priorOptions, types.StringValue(opt.Title), func(m onboardingOptionModel) types.String { return m.Title })
			options = append(options, applyOption(ctx, opt, prior, &diags))
		}
		list, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: onboardingOptionAttrTypes}, options)
		diags.Append(d...)
		prompts = append(prompts, onboardingPromptModel{
			ID:           types.StringValue(p.ID),
			Title:        types.StringValue(p.Title),
			Type:         promptTypes.name(p.Type),
			SingleSelect: types.BoolValue(p.SingleSelect),
			Required:     types.BoolValue(p.Required),
			InOnboarding: types.BoolValue(p.InOnboarding),
			Options:      list,
		})
	}
	m.ID = m.ServerID
	m.Enabled = types.BoolValue(o.Enabled)
	m.Mode = onboardingModes.name(o.Mode)
	m.DefaultChannelIDs = optionalSet(ctx, m.DefaultChannelIDs, o.DefaultChannelIDs, &diags)
	if len(prompts) == 0 && m.Prompts.IsNull() {
		return diags
	}
	list, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: onboardingPromptAttrTypes}, prompts)
	diags.Append(d...)
	m.Prompts = list
	return diags
}

// applyOption converts an option read from Discord. Discord names a custom
// emoji even when only its ID was sent and always reports whether it is
// animated, so those are kept only when the prior state had them.
func applyOption(ctx context.Context, opt discord.OnboardingPromptOption, prior onboardingOptionModel, diags *diag.Diagnostics) onboardingOptionModel {
	m := onboardingOptionModel{
		ID:            types.StringValue(opt.ID),
		Title:         types.StringValue(opt.Title),
		Description:   stringPtrValue(opt.Description),
		EmojiID:       types.StringNull(),
		EmojiName:     types.StringNull(),
		EmojiAnimated: types.BoolNull(),
		RoleIDs:       optionalSet(ctx, prior.RoleIDs, opt.RoleIDs, diags),
		ChannelIDs:    optionalSet(ctx, prior.ChannelIDs, opt.ChannelIDs, diags),
	}
	if e := opt.Emoji; e != nil {
		m.EmojiID = stringPtrValue(e.ID)
		m.EmojiName = stringPtrValue(e.Name)
		if e.ID != nil && (prior.EmojiName.IsNull() || prior.EmojiName.IsUnknown()) {
			m.EmojiName = types.StringNull()
		}
		if !prior.EmojiAnimated.IsNull() && !prior.EmojiAnimated.IsUnknown() {
			m.EmojiAnimated = types.BoolValue(e.Animated)
		}
	}
	return m
}

func (r *onboardingResource) write(ctx context.Context, m *onboardingModel, diags *diag.Diagnostics) {
	p, d := m.payload(ctx, time.Now())
	diags.Append(d...)
	if diags.HasError() {
		return
	}
	o, err := r.client.ModifyOnboarding(ctx, m.ServerID.ValueString(), p)
	if err != nil {
		apiError(diags, "update onboarding", err)
		return
	}
	diags.Append(m.apply(ctx, o)...)
}

func (r *onboardingResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan onboardingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	if r.write(ctx, &plan, &resp.Diagnostics); resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *onboardingResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state onboardingModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	o, err := r.client.GetOnboarding(ctx, state.ServerID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read onboarding", err)
		return
	}
	resp.Diagnostics.Append(state.apply(ctx, o)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *onboardingResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan onboardingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	if r.write(ctx, &plan, &resp.Diagnostics); resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *onboardingResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state onboardingModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	_, err := r.client.ModifyOnboarding(ctx, state.ServerID.ValueString(), discord.Payload{"enabled": false})
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "disable onboarding", err)
	}
}

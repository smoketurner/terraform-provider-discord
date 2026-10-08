package provider

import (
	"context"
	"fmt"
	"slices"

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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure      = &autoModerationRuleResource{}
	_ resource.ResourceWithImportState    = &autoModerationRuleResource{}
	_ resource.ResourceWithIdentity       = &autoModerationRuleResource{}
	_ resource.ResourceWithValidateConfig = &autoModerationRuleResource{}
)

var (
	automodEventTypes   = enumMapping{"", "message_send", "member_update"}
	automodTriggerTypes = enumMapping{"", "keyword", "", "spam", "keyword_preset", "mention_spam", "member_profile"}
	automodPresets      = enumMapping{"", "profanity", "sexual_content", "slurs"}
	automodActionTypes  = enumMapping{"", "block_message", "send_alert_message", "timeout", "block_member_interaction"}
)

// automodMetadataFields lists the trigger_metadata fields each trigger type
// uses.
var automodMetadataFields = map[string][]string{
	"keyword":        {"keyword_filter", "regex_patterns", "allow_list"},
	"member_profile": {"keyword_filter", "regex_patterns", "allow_list"},
	"keyword_preset": {"presets", "allow_list"},
	"mention_spam":   {"mention_total_limit", "mention_raid_protection_enabled"},
	"spam":           {},
}

// automodActionFields lists the action fields each action type uses, and
// which of them it requires.
var automodActionFields = map[string]struct{ allowed, required []string }{
	"block_message":            {allowed: []string{"custom_message"}},
	"send_alert_message":       {allowed: []string{"channel_id"}, required: []string{"channel_id"}},
	"timeout":                  {allowed: []string{"duration_seconds"}, required: []string{"duration_seconds"}},
	"block_member_interaction": {},
}

// automodKeywordAllowListMax is the allow_list limit for keyword and
// member_profile rules; keyword_preset rules accept up to 1000.
const automodKeywordAllowListMax = 100

type autoModerationRuleResource struct {
	resourceIdentity
	client *discord.Client
}

type autoModerationRuleModel struct {
	ID               types.String `tfsdk:"id"`
	ServerID         types.String `tfsdk:"server_id"`
	Name             types.String `tfsdk:"name"`
	EventType        types.String `tfsdk:"event_type"`
	TriggerType      types.String `tfsdk:"trigger_type"`
	TriggerMetadata  types.Object `tfsdk:"trigger_metadata"`
	Actions          types.List   `tfsdk:"actions"`
	Enabled          types.Bool   `tfsdk:"enabled"`
	ExemptRoleIDs    types.Set    `tfsdk:"exempt_role_ids"`
	ExemptChannelIDs types.Set    `tfsdk:"exempt_channel_ids"`
	CreatorID        types.String `tfsdk:"creator_id"`
	AuditLogReason   types.String `tfsdk:"audit_log_reason"`
}

type automodTriggerMetadataModel struct {
	KeywordFilter                types.Set   `tfsdk:"keyword_filter"`
	RegexPatterns                types.Set   `tfsdk:"regex_patterns"`
	Presets                      types.Set   `tfsdk:"presets"`
	AllowList                    types.Set   `tfsdk:"allow_list"`
	MentionTotalLimit            types.Int64 `tfsdk:"mention_total_limit"`
	MentionRaidProtectionEnabled types.Bool  `tfsdk:"mention_raid_protection_enabled"`
}

type automodActionModel struct {
	Type            types.String `tfsdk:"type"`
	CustomMessage   types.String `tfsdk:"custom_message"`
	ChannelID       types.String `tfsdk:"channel_id"`
	DurationSeconds types.Int64  `tfsdk:"duration_seconds"`
}

var automodTriggerMetadataAttrTypes = map[string]attr.Type{
	"keyword_filter":                  types.SetType{ElemType: types.StringType},
	"regex_patterns":                  types.SetType{ElemType: types.StringType},
	"presets":                         types.SetType{ElemType: types.StringType},
	"allow_list":                      types.SetType{ElemType: types.StringType},
	"mention_total_limit":             types.Int64Type,
	"mention_raid_protection_enabled": types.BoolType,
}

var automodActionAttrTypes = map[string]attr.Type{
	"type":             types.StringType,
	"custom_message":   types.StringType,
	"channel_id":       types.StringType,
	"duration_seconds": types.Int64Type,
}

func newAutoModerationRuleResource() resource.Resource {
	return &autoModerationRuleResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		serverIdentity("server_id"),
		{name: "rule_id", description: "ID of the AutoMod rule.", state: []string{"id"}},
	}}}
}

func (r *autoModerationRuleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_auto_moderation_rule"
}

// keywordSet is a set of keywords or patterns. Empty sets are rejected so
// that an omitted argument and an empty list mean the same thing.
func keywordSet(desc string, maxItems, maxLength int) schema.SetAttribute {
	return schema.SetAttribute{
		MarkdownDescription: desc,
		ElementType:         types.StringType,
		Optional:            true,
		Validators: []validator.Set{
			setvalidator.SizeBetween(1, maxItems),
			setvalidator.ValueStringsAre(stringvalidator.LengthBetween(1, maxLength)),
		},
	}
}

func (r *autoModerationRuleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a server AutoMod rule. Requires the Manage Server permission, and Moderate " +
			"Members for a `timeout` action.\n\n" +
			"A server can have up to 6 `keyword` rules and one rule of each other trigger type. Discord creates a " +
			"`mention_spam` rule in Community servers; import it to manage it.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttribute(),
			"id":               idAttribute("Rule ID."),
			"server_id":        serverIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Rule name (1-100 characters).",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 100)},
			},
			"event_type": schema.StringAttribute{
				MarkdownDescription: "When the rule is checked: `message_send` when a member sends or edits a message, " +
					"or `member_update` when a member edits their profile, which `member_profile` rules check.",
				Required:   true,
				Validators: []validator.String{automodEventTypes.validator()},
			},
			"trigger_type": schema.StringAttribute{
				MarkdownDescription: "What triggers the rule: " + automodTriggerTypes.doc() + ". `keyword` checks content " +
					"for the configured keywords and patterns, `spam` for generic spam, `keyword_preset` for Discord's " +
					"word lists, `mention_spam` for too many unique mentions, and `member_profile` checks member " +
					"profiles for keywords. Changing it replaces the rule.",
				Required:      true,
				Validators:    []validator.String{automodTriggerTypes.validator()},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"trigger_metadata": schema.SingleNestedAttribute{
				MarkdownDescription: "Settings of the trigger. Each trigger type uses only some of the fields: " +
					"`keyword` and `member_profile` use `keyword_filter`, `regex_patterns` and `allow_list`; " +
					"`keyword_preset` uses `presets` and `allow_list`; `mention_spam` uses `mention_total_limit` and " +
					"`mention_raid_protection_enabled`; `spam` uses none. Lists left out are cleared, while left-out mention settings keep their " +
					"current values. Leave the whole argument unset to keep Discord's current settings.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"keyword_filter": keywordSet("Keywords to match (up to 1000, each up to 60 characters). A keyword "+
						"can be a phrase. A leading or trailing `*` matches it as a prefix, suffix or anywhere in a word; "+
						"without one it must be a whole word. Matching is case insensitive.", 1000, 60),
					"regex_patterns": keywordSet("Rust-flavored regular expressions to match (up to 10, each up to "+
						"260 characters).", 10, 260),
					"presets": schema.SetAttribute{
						MarkdownDescription: "Discord word lists to match: " + automodPresets.doc() + ".",
						ElementType:         types.StringType,
						Optional:            true,
						Validators: []validator.Set{
							setvalidator.SizeAtLeast(1),
							setvalidator.ValueStringsAre(automodPresets.validator()),
						},
					},
					"allow_list": keywordSet("Keywords that do not trigger the rule, matched like `keyword_filter` "+
						"(each up to 60 characters; up to 100 for `keyword` and `member_profile` rules, 1000 for "+
						"`keyword_preset` rules).", 1000, 60),
					"mention_total_limit": schema.Int64Attribute{
						MarkdownDescription: "Most unique role and user mentions allowed in a message (up to 50).",
						Optional:            true,
						Computed:            true,
						Validators:          []validator.Int64{int64validator.Between(0, 50)},
						PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
					},
					"mention_raid_protection_enabled": schema.BoolAttribute{
						MarkdownDescription: "Whether to detect sudden spikes in mentions that indicate a mention raid.",
						Optional:            true,
						Computed:            true,
						PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
					},
				},
			},
			"actions": schema.ListNestedAttribute{
				MarkdownDescription: "Actions taken when the rule triggers (1-5).",
				Required:            true,
				Validators:          []validator.List{listvalidator.SizeBetween(1, 5)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							MarkdownDescription: "Action: `block_message` blocks the message, `send_alert_message` logs " +
								"the content to `channel_id`, `timeout` times the member out for `duration_seconds` " +
								"(only for `keyword` and `mention_spam` rules), and `block_member_interaction` stops the " +
								"member from using text, voice and other interactions.",
							Required:   true,
							Validators: []validator.String{automodActionTypes.validator()},
						},
						"custom_message": schema.StringAttribute{
							MarkdownDescription: "Explanation shown to the member when `block_message` blocks their " +
								"message (up to 150 characters).",
							Optional:   true,
							Validators: []validator.String{stringvalidator.LengthBetween(1, 150)},
						},
						"channel_id": schema.StringAttribute{
							MarkdownDescription: "Channel `send_alert_message` logs to. Required for that action.",
							Optional:            true,
							Validators:          []validator.String{snowflakeValidator()},
						},
						"duration_seconds": schema.Int64Attribute{
							MarkdownDescription: "Length of a `timeout` in seconds, up to `2419200` (4 weeks). Required " +
								"for that action.",
							Optional:   true,
							Validators: []validator.Int64{int64validator.Between(0, 2419200)},
						},
					},
				},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the rule is enabled. Defaults to `false`, as in Discord.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"exempt_role_ids": schema.SetAttribute{
				MarkdownDescription: "IDs of roles the rule does not apply to (up to 20).",
				ElementType:         types.StringType,
				Optional:            true,
				Validators:          []validator.Set{setvalidator.SizeBetween(1, 20), setvalidator.ValueStringsAre(snowflakeValidator())},
			},
			"exempt_channel_ids": schema.SetAttribute{
				MarkdownDescription: "IDs of channels the rule does not apply to (up to 50).",
				ElementType:         types.StringType,
				Optional:            true,
				Validators:          []validator.Set{setvalidator.SizeBetween(1, 50), setvalidator.ValueStringsAre(snowflakeValidator())},
			},
			"creator_id": schema.StringAttribute{
				MarkdownDescription: "ID of the user that created the rule.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *autoModerationRuleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (r *autoModerationRuleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m autoModerationRuleModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.TriggerType.IsUnknown() {
		return
	}
	trigger := m.TriggerType.ValueString()
	if allowed, ok := automodMetadataFields[trigger]; ok && isSet(m.TriggerMetadata) {
		var meta automodTriggerMetadataModel
		resp.Diagnostics.Append(m.TriggerMetadata.As(ctx, &meta, basetypes.ObjectAsOptions{})...)
		for name, v := range meta.values() {
			if !v.IsNull() && !slices.Contains(allowed, name) {
				resp.Diagnostics.AddAttributeError(path.Root("trigger_metadata").AtName(name), "Invalid argument",
					fmt.Sprintf("trigger_metadata.%s does not apply to %s rules.", name, trigger))
			}
		}
		if (trigger == "keyword" || trigger == "member_profile") && isSet(meta.AllowList) && len(meta.AllowList.Elements()) > automodKeywordAllowListMax {
			resp.Diagnostics.AddAttributeError(path.Root("trigger_metadata").AtName("allow_list"), "Invalid argument",
				fmt.Sprintf("%s rules accept at most %d allow_list entries.", trigger, automodKeywordAllowListMax))
		}
	}
	if m.Actions.IsUnknown() || m.Actions.IsNull() {
		return
	}
	var actions []automodActionModel
	resp.Diagnostics.Append(m.Actions.ElementsAs(ctx, &actions, false)...)
	for i, a := range actions {
		if a.Type.IsUnknown() {
			continue
		}
		typ := a.Type.ValueString()
		fields, ok := automodActionFields[typ]
		if !ok {
			continue
		}
		for name, v := range a.values() {
			p := path.Root("actions").AtListIndex(i).AtName(name)
			switch {
			case v.IsNull() && slices.Contains(fields.required, name):
				resp.Diagnostics.AddAttributeError(p, "Missing argument", fmt.Sprintf("A %s action requires %s.", typ, name))
			case !v.IsNull() && !slices.Contains(fields.allowed, name):
				resp.Diagnostics.AddAttributeError(p, "Invalid argument", fmt.Sprintf("%s does not apply to a %s action.", name, typ))
			}
		}
		if typ == "timeout" && trigger != "keyword" && trigger != "mention_spam" {
			resp.Diagnostics.AddAttributeError(path.Root("actions").AtListIndex(i).AtName("type"), "Invalid argument",
				fmt.Sprintf("A timeout action is only allowed in keyword and mention_spam rules, not %s rules.", trigger))
		}
	}
}

func (m automodTriggerMetadataModel) values() map[string]attr.Value {
	return map[string]attr.Value{
		"keyword_filter":                  m.KeywordFilter,
		"regex_patterns":                  m.RegexPatterns,
		"presets":                         m.Presets,
		"allow_list":                      m.AllowList,
		"mention_total_limit":             m.MentionTotalLimit,
		"mention_raid_protection_enabled": m.MentionRaidProtectionEnabled,
	}
}

func (a automodActionModel) values() map[string]attr.Value {
	return map[string]attr.Value{
		"custom_message":   a.CustomMessage,
		"channel_id":       a.ChannelID,
		"duration_seconds": a.DurationSeconds,
	}
}

// stringsOrEmpty returns a set's strings, or an empty slice for a null set,
// so that the payload clears the list rather than sending null.
func stringsOrEmpty(ctx context.Context, v types.Set, diags *diag.Diagnostics) []string {
	out := []string{}
	if isSet(v) {
		diags.Append(v.ElementsAs(ctx, &out, false)...)
	}
	return out
}

// optionalStringSet stores an empty list as null, as the schema rejects
// empty sets.
func optionalStringSet(ctx context.Context, values []string, diags *diag.Diagnostics) types.Set {
	if len(values) == 0 {
		return types.SetNull(types.StringType)
	}
	return stringSetValue(ctx, values, diags)
}

// payload returns the Create or Modify Auto Moderation Rule body. Lists the
// configuration leaves out are sent empty so that removing them clears them.
func (m *autoModerationRuleModel) payload(ctx context.Context, diags *diag.Diagnostics) discord.Payload {
	p := discord.Payload{
		"name":            m.Name.ValueString(),
		"exempt_roles":    stringsOrEmpty(ctx, m.ExemptRoleIDs, diags),
		"exempt_channels": stringsOrEmpty(ctx, m.ExemptChannelIDs, diags),
	}
	automodEventTypes.put(p, "event_type", m.EventType)
	putBool(p, "enabled", m.Enabled)

	var actions []automodActionModel
	diags.Append(m.Actions.ElementsAs(ctx, &actions, false)...)
	body := make([]discord.Payload, 0, len(actions))
	for _, a := range actions {
		action := discord.Payload{}
		automodActionTypes.put(action, "type", a.Type)
		meta := discord.Payload{}
		putKnownString(meta, "custom_message", a.CustomMessage)
		putKnownString(meta, "channel_id", a.ChannelID)
		putKnownInt(meta, "duration_seconds", a.DurationSeconds)
		if len(meta) > 0 {
			action["metadata"] = meta
		}
		body = append(body, action)
	}
	p["actions"] = body

	if isSet(m.TriggerMetadata) {
		var meta automodTriggerMetadataModel
		diags.Append(m.TriggerMetadata.As(ctx, &meta, basetypes.ObjectAsOptions{})...)
		tm := discord.Payload{}
		for _, name := range automodMetadataFields[m.TriggerType.ValueString()] {
			switch name {
			case "keyword_filter":
				tm[name] = stringsOrEmpty(ctx, meta.KeywordFilter, diags)
			case "regex_patterns":
				tm[name] = stringsOrEmpty(ctx, meta.RegexPatterns, diags)
			case "allow_list":
				tm[name] = stringsOrEmpty(ctx, meta.AllowList, diags)
			case "presets":
				presets := []int64{}
				for _, s := range stringsOrEmpty(ctx, meta.Presets, diags) {
					if n, ok := automodPresets.value(s); ok {
						presets = append(presets, n)
					}
				}
				tm[name] = presets
			case "mention_total_limit":
				putKnownInt(tm, name, meta.MentionTotalLimit)
			case "mention_raid_protection_enabled":
				putBool(tm, name, meta.MentionRaidProtectionEnabled)
			}
		}
		p["trigger_metadata"] = tm
	}
	return p
}

func (m *autoModerationRuleModel) apply(ctx context.Context, rule *discord.AutoModerationRule, diags *diag.Diagnostics) {
	m.ID = types.StringValue(rule.ID)
	m.ServerID = types.StringValue(rule.GuildID)
	m.Name = types.StringValue(rule.Name)
	m.EventType = automodEventTypes.name(rule.EventType)
	m.TriggerType = automodTriggerTypes.name(rule.TriggerType)
	m.Enabled = types.BoolValue(rule.Enabled)
	m.CreatorID = types.StringValue(rule.CreatorID)
	m.ExemptRoleIDs = optionalStringSet(ctx, rule.ExemptRoles, diags)
	m.ExemptChannelIDs = optionalStringSet(ctx, rule.ExemptChannels, diags)

	tm := rule.TriggerMetadata
	meta := automodTriggerMetadataModel{
		KeywordFilter:                types.SetNull(types.StringType),
		RegexPatterns:                types.SetNull(types.StringType),
		Presets:                      types.SetNull(types.StringType),
		AllowList:                    types.SetNull(types.StringType),
		MentionTotalLimit:            types.Int64Null(),
		MentionRaidProtectionEnabled: types.BoolNull(),
	}
	for _, name := range automodMetadataFields[m.TriggerType.ValueString()] {
		switch name {
		case "keyword_filter":
			meta.KeywordFilter = optionalStringSet(ctx, tm.KeywordFilter, diags)
		case "regex_patterns":
			meta.RegexPatterns = optionalStringSet(ctx, tm.RegexPatterns, diags)
		case "allow_list":
			meta.AllowList = optionalStringSet(ctx, tm.AllowList, diags)
		case "presets":
			presets := make([]string, 0, len(tm.Presets))
			for _, p := range tm.Presets {
				presets = append(presets, automodPresets.name(p).ValueString())
			}
			meta.Presets = optionalStringSet(ctx, presets, diags)
		case "mention_total_limit":
			meta.MentionTotalLimit = types.Int64PointerValue(tm.MentionTotalLimit)
		case "mention_raid_protection_enabled":
			meta.MentionRaidProtectionEnabled = types.BoolPointerValue(tm.MentionRaidProtectionEnabled)
		}
	}
	obj, d := types.ObjectValueFrom(ctx, automodTriggerMetadataAttrTypes, meta)
	diags.Append(d...)
	m.TriggerMetadata = obj

	actions := make([]automodActionModel, 0, len(rule.Actions))
	for _, a := range rule.Actions {
		am := automodActionModel{
			Type:            automodActionTypes.name(a.Type),
			CustomMessage:   types.StringNull(),
			ChannelID:       types.StringNull(),
			DurationSeconds: types.Int64Null(),
		}
		if md := a.Metadata; md != nil {
			switch am.Type.ValueString() {
			case "block_message":
				am.CustomMessage = stringPtrValue(md.CustomMessage)
			case "send_alert_message":
				am.ChannelID = types.StringValue(md.ChannelID)
			case "timeout":
				am.DurationSeconds = types.Int64PointerValue(md.DurationSeconds)
			}
		}
		actions = append(actions, am)
	}
	list, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: automodActionAttrTypes}, actions)
	diags.Append(d...)
	m.Actions = list
}

func (r *autoModerationRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan autoModerationRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	p := plan.payload(ctx, &resp.Diagnostics)
	automodTriggerTypes.put(p, "trigger_type", plan.TriggerType)
	if resp.Diagnostics.HasError() {
		return
	}
	rule, err := r.client.CreateAutoModerationRule(ctx, plan.ServerID.ValueString(), p)
	if err != nil {
		apiError(&resp.Diagnostics, "create AutoMod rule", err)
		return
	}
	plan.apply(ctx, rule, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *autoModerationRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state autoModerationRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rule, err := r.client.GetAutoModerationRule(ctx, state.ServerID.ValueString(), state.ID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read AutoMod rule", err)
		return
	}
	state.apply(ctx, rule, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *autoModerationRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan autoModerationRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	p := plan.payload(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	rule, err := r.client.ModifyAutoModerationRule(ctx, plan.ServerID.ValueString(), plan.ID.ValueString(), p)
	if err != nil {
		apiError(&resp.Diagnostics, "update AutoMod rule", err)
		return
	}
	plan.apply(ctx, rule, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *autoModerationRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state autoModerationRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	err := r.client.DeleteAutoModerationRule(ctx, state.ServerID.ValueString(), state.ID.ValueString())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete AutoMod rule", err)
	}
}

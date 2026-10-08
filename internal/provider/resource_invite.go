package provider

import (
	"context"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure      = &inviteResource{}
	_ resource.ResourceWithImportState    = &inviteResource{}
	_ resource.ResourceWithIdentity       = &inviteResource{}
	_ resource.ResourceWithValidateConfig = &inviteResource{}
)

// maxInviteTargetUsers is the most users an invite can be limited to.
const maxInviteTargetUsers = 1000

var inviteTargetTypes = enumMapping{"", "stream", "embedded_application"}

type inviteResource struct {
	resourceIdentity
	client *discord.Client
}

type inviteModel struct {
	ID             types.String `tfsdk:"id"`
	ChannelID      types.String `tfsdk:"channel_id"`
	MaxAge         types.Int64  `tfsdk:"max_age"`
	MaxUses        types.Int64  `tfsdk:"max_uses"`
	Temporary      types.Bool   `tfsdk:"temporary"`
	Unique         types.Bool   `tfsdk:"unique"`
	Code           types.String `tfsdk:"code"`
	URL            types.String `tfsdk:"url"`
	ExpiresAt      types.String `tfsdk:"expires_at"`
	AuditLogReason types.String `tfsdk:"audit_log_reason"`
	RoleIDs        types.Set    `tfsdk:"role_ids"`
	TargetType     types.String `tfsdk:"target_type"`
	TargetUserID   types.String `tfsdk:"target_user_id"`
	TargetAppID    types.String `tfsdk:"target_application_id"`
	TargetUserIDs  types.Set    `tfsdk:"target_user_ids"`
}

func newInviteResource() resource.Resource {
	return &inviteResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		channelIdentity("channel_id"),
		{name: "code", description: "Invite code.", state: []string{"id"}},
	}}}
}

func (r *inviteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_invite"
}

func (r *inviteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a channel invite. Invites cannot be edited, so every change creates a new invite. " +
			"An invite that expires or is revoked outside Terraform is created again on the next apply. Reading invites " +
			"requires the Manage Channels permission on the channel.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttribute(),
			"id":               idAttribute("Invite code."),
			"channel_id": schema.StringAttribute{
				MarkdownDescription: "ID of the channel to invite to.",
				Required:            true,
				Validators:          []validator.String{snowflakeValidator()},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"max_age": schema.Int64Attribute{
				MarkdownDescription: "Seconds until the invite expires, between `0` (never) and `604800` (7 days). Defaults to `86400`.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(86400),
				Validators:          []validator.Int64{int64validator.Between(0, 604800)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"max_uses": schema.Int64Attribute{
				MarkdownDescription: "Maximum number of uses between `0` (unlimited) and `100`. Defaults to `0`.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(0),
				Validators:          []validator.Int64{int64validator.Between(0, 100)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"temporary": schema.BoolAttribute{
				MarkdownDescription: "Whether the invite grants temporary membership. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"unique": schema.BoolAttribute{
				MarkdownDescription: "Whether to always create a new invite. When `false`, Discord may return an existing invite " +
					"with the same settings, which other resources could then share. Defaults to `true`.",
				Optional:      true,
				Computed:      true,
				Default:       booldefault.StaticBool(true),
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"code": schema.StringAttribute{
				MarkdownDescription: "Invite code.",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "Invite URL, `https://discord.gg/<code>`.",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"expires_at": schema.StringAttribute{
				MarkdownDescription: "When the invite expires (RFC 3339), or null if it never expires.",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"role_ids": schema.SetAttribute{
				MarkdownDescription: "IDs of roles given to the users who accept the invite. Requires the Manage Roles " +
					"permission, and the roles must be below the bot's highest role.",
				ElementType:   types.StringType,
				Optional:      true,
				Validators:    []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueStringsAre(snowflakeValidator())},
				PlanModifiers: []planmodifier.Set{setplanmodifier.RequiresReplace()},
			},
			"target_type": schema.StringAttribute{
				MarkdownDescription: "What a voice channel invite opens: `stream` shows `target_user_id`'s stream and " +
					"`embedded_application` opens the `target_application_id` activity.",
				Optional:      true,
				Validators:    []validator.String{inviteTargetTypes.validator()},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"target_user_id": schema.StringAttribute{
				MarkdownDescription: "ID of the user whose stream a `stream` invite shows. The user must be streaming in " +
					"the channel.",
				Optional:      true,
				Validators:    []validator.String{snowflakeValidator()},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"target_application_id": schema.StringAttribute{
				MarkdownDescription: "ID of the application an `embedded_application` invite opens. The application must " +
					"have the `EMBEDDED` flag.",
				Optional:      true,
				Validators:    []validator.String{snowflakeValidator()},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"target_user_ids": schema.SetAttribute{
				MarkdownDescription: "IDs of the only users who can see and accept the invite, at most 1000. Users are " +
					"added and removed in place; setting or removing the argument creates a new invite. Changes made " +
					"outside Terraform are detected only while the argument is set, and it is not imported. Changing the " +
					"users requires the bot to have created the invite or to have the Manage Server permission.",
				ElementType: types.StringType,
				Optional:    true,
				Validators: []validator.Set{
					setvalidator.SizeBetween(1, maxInviteTargetUsers),
					setvalidator.ValueStringsAre(snowflakeValidator()),
				},
				PlanModifiers: []planmodifier.Set{setplanmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.SetRequest, resp *setplanmodifier.RequiresReplaceIfFuncResponse) {
						resp.RequiresReplace = req.StateValue.IsNull() != req.PlanValue.IsNull()
					},
					"Setting or removing target_user_ids creates a new invite.",
					"Setting or removing `target_user_ids` creates a new invite.",
				)},
			},
		},
	}
}

func (r *inviteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

// ValidateConfig checks that a target matches target_type, as Discord
// requires.
func (r *inviteResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg inviteModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.TargetType.IsUnknown() || cfg.TargetUserID.IsUnknown() || cfg.TargetAppID.IsUnknown() {
		return
	}
	want := map[string]string{"": "", "stream": "target_user_id", "embedded_application": "target_application_id"}[cfg.TargetType.ValueString()]
	for name, v := range map[string]types.String{"target_user_id": cfg.TargetUserID, "target_application_id": cfg.TargetAppID} {
		switch {
		case name == want && v.IsNull():
			resp.Diagnostics.AddAttributeError(path.Root(name), "Missing invite target",
				fmt.Sprintf("%s is required when target_type is %q.", name, cfg.TargetType.ValueString()))
		case name != want && !v.IsNull():
			resp.Diagnostics.AddAttributeError(path.Root(name), "Invalid invite target",
				fmt.Sprintf("%s can only be set when target_type is %q.", name, map[string]string{
					"target_user_id": "stream", "target_application_id": "embedded_application",
				}[name]))
		}
	}
}

func (m *inviteModel) payload(ctx context.Context) (discord.Payload, diag.Diagnostics) {
	var diags diag.Diagnostics
	p := discord.Payload{
		"max_age":   m.MaxAge.ValueInt64(),
		"max_uses":  m.MaxUses.ValueInt64(),
		"temporary": m.Temporary.ValueBool(),
		"unique":    m.Unique.ValueBool(),
	}
	inviteTargetTypes.put(p, "target_type", m.TargetType)
	putKnownString(p, "target_user_id", m.TargetUserID)
	putKnownString(p, "target_application_id", m.TargetAppID)
	for key, set := range map[string]types.Set{"role_ids": m.RoleIDs, "target_user_ids": m.TargetUserIDs} {
		if set.IsNull() {
			continue
		}
		var ids []string
		diags.Append(set.ElementsAs(ctx, &ids, false)...)
		p[key] = ids
	}
	return p, diags
}

// applyTargets sets the role grants and targets Discord returns with an
// invite. Target users are read separately.
func (m *inviteModel) applyTargets(ctx context.Context, inv *discord.Invite, diags *diag.Diagnostics) {
	m.RoleIDs = types.SetNull(types.StringType)
	if len(inv.Roles) > 0 {
		ids := make([]string, len(inv.Roles))
		for i, role := range inv.Roles {
			ids[i] = role.ID
		}
		m.RoleIDs = stringSetValue(ctx, ids, diags)
	}
	m.TargetType, m.TargetUserID, m.TargetAppID = types.StringNull(), types.StringNull(), types.StringNull()
	if inv.TargetType != 0 {
		m.TargetType = inviteTargetTypes.name(inv.TargetType)
	}
	if inv.TargetUser != nil {
		m.TargetUserID = types.StringValue(inv.TargetUser.ID)
	}
	if inv.TargetApplication != nil {
		m.TargetAppID = types.StringValue(inv.TargetApplication.ID)
	}
}

func (m *inviteModel) apply(inv *discord.Invite) {
	m.ID = types.StringValue(inv.Code)
	m.Code = types.StringValue(inv.Code)
	m.URL = types.StringValue("https://discord.gg/" + inv.Code)
	if inv.Channel != nil {
		m.ChannelID = types.StringValue(inv.Channel.ID)
	}
	m.MaxAge = types.Int64Value(inv.MaxAge)
	m.MaxUses = types.Int64Value(inv.MaxUses)
	m.Temporary = types.BoolValue(inv.Temporary)
	m.ExpiresAt = stringPtrValue(inv.ExpiresAt)
}

func (r *inviteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan inviteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := plan.payload(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	inv, err := r.client.CreateInvite(ctx, plan.ChannelID.ValueString(), body)
	if err != nil {
		apiError(&resp.Diagnostics, "create invite", err)
		return
	}
	// Discord documents invite metadata (max_age, max_uses, temporary) only
	// on Get Channel Invites, so keep the requested values; Read refreshes
	// them. Role grants and targets are kept from the plan as well, and
	// target users are processed after the invite is created.
	maxAge, maxUses, temporary := plan.MaxAge, plan.MaxUses, plan.Temporary
	plan.apply(inv)
	plan.MaxAge, plan.MaxUses, plan.Temporary = maxAge, maxUses, temporary
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *inviteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state inviteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	invites, err := r.client.ListChannelInvites(ctx, state.ChannelID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "list channel invites", err)
		return
	}
	for i := range invites {
		if invites[i].Code == state.ID.ValueString() {
			state.apply(&invites[i])
			state.applyTargets(ctx, &invites[i], &resp.Diagnostics)
			if state.Unique.IsNull() {
				state.Unique = types.BoolValue(true)
			}
			if !state.TargetUserIDs.IsNull() {
				users, err := r.client.GetInviteTargetUsers(ctx, state.ID.ValueString())
				if err != nil {
					apiError(&resp.Diagnostics, "read invite target users", err)
					return
				}
				state.TargetUserIDs = stringSetValue(ctx, users, &resp.Diagnostics)
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *inviteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	// Everything but audit_log_reason and the target users forces
	// replacement.
	var plan, state inviteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var want, have []string
	resp.Diagnostics.Append(plan.TargetUserIDs.ElementsAs(ctx, &want, false)...)
	resp.Diagnostics.Append(state.TargetUserIDs.ElementsAs(ctx, &have, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	code := state.ID.ValueString()
	if add := setDifference(want, have); len(add) > 0 {
		if err := r.client.AddInviteTargetUsers(ctx, code, add); err != nil {
			apiError(&resp.Diagnostics, "add invite target users", err)
			return
		}
	}
	if remove := setDifference(have, want); len(remove) > 0 {
		if err := r.client.RemoveInviteTargetUsers(ctx, code, remove); err != nil {
			apiError(&resp.Diagnostics, "remove invite target users", err)
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// setDifference returns the values of a that are not in b, sorted.
func setDifference(a, b []string) []string {
	var out []string
	for _, v := range a {
		if !slices.Contains(b, v) {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return out
}

func (r *inviteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state inviteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	if err := r.client.DeleteInvite(ctx, state.ID.ValueString()); err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete invite", err)
	}
}

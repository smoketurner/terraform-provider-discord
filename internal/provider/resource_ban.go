package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure   = &banResource{}
	_ resource.ResourceWithImportState = &banResource{}
	_ resource.ResourceWithIdentity    = &banResource{}
)

// maxBanDeleteMessageSeconds is the most message history, 7 days, Discord
// deletes when banning.
const maxBanDeleteMessageSeconds = 604800

type banResource struct {
	resourceIdentity
	client *discord.Client
}

type banModel struct {
	ID                   types.String `tfsdk:"id"`
	ServerID             types.String `tfsdk:"server_id"`
	UserID               types.String `tfsdk:"user_id"`
	Reason               types.String `tfsdk:"reason"`
	DeleteMessageSeconds types.Int64  `tfsdk:"delete_message_seconds"`
	AuditLogReason       types.String `tfsdk:"audit_log_reason"`
}

func newBanResource() resource.Resource {
	return &banResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		serverIdentity("server_id"),
		{name: "user_id", description: "ID of the banned user.", state: []string{"user_id"}},
	}}}
}

func (r *banResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ban"
}

func (r *banResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Bans a user from a server. The user does not need to be a member; a member is removed " +
			"from the server. Destroying the resource lifts the ban. If the ban is lifted outside Terraform the user " +
			"is banned again on the next apply. Requires the `BAN_MEMBERS` permission.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttributeFor("lifting the ban, and for the ban itself when `reason` " +
				"is not set"),
			"id":        idAttribute("`server_id/user_id`."),
			"server_id": serverIDAttribute(),
			"user_id": schema.StringAttribute{
				MarkdownDescription: "ID of the user to ban.",
				Required:            true,
				Validators:          []validator.String{snowflakeValidator()},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"reason": schema.StringAttribute{
				MarkdownDescription: "Reason for the ban, shown in the server's ban list and audit log. Up to 512 " +
					"characters. Discord takes the ban reason from the audit log reason of the request that creates " +
					"the ban, so when this is not set the ban records `audit_log_reason` or the provider's " +
					"`audit_log_reason`, and this attribute reports it. Discord cannot change the reason of an " +
					"existing ban, so changing it lifts the ban and bans the user again.",
				Optional:   true,
				Computed:   true,
				Validators: []validator.String{auditLogReasonValidator()},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"delete_message_seconds": schema.Int64Attribute{
				MarkdownDescription: "Number of seconds of the user's message history to delete when banning, " +
					"from 0 to 604800 (7 days). Defaults to 0. Only used when the ban is created; changing it later " +
					"updates state without calling Discord, and it is not read back or imported.",
				Optional:   true,
				Validators: []validator.Int64{int64validator.Between(0, maxBanDeleteMessageSeconds)},
			},
		},
	}
}

func (r *banResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *banModel) id() string {
	return m.ServerID.ValueString() + "/" + m.UserID.ValueString()
}

func (r *banResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan banModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The ban's reason is the request's audit log reason, so a configured
	// reason takes the place of audit_log_reason.
	reason := plan.AuditLogReason
	if !plan.Reason.IsNull() && !plan.Reason.IsUnknown() {
		reason = plan.Reason
	}
	body := discord.Payload{}
	if !plan.DeleteMessageSeconds.IsNull() {
		body["delete_message_seconds"] = plan.DeleteMessageSeconds.ValueInt64()
	}
	serverID, userID := plan.ServerID.ValueString(), plan.UserID.ValueString()
	if err := r.client.CreateBan(withAuditLogReason(ctx, reason), serverID, userID, body); err != nil {
		apiError(&resp.Diagnostics, "ban user", err)
		return
	}
	ban, err := r.client.GetBan(ctx, serverID, userID)
	if err != nil {
		apiError(&resp.Diagnostics, "read ban", err)
		return
	}
	plan.ID = types.StringValue(plan.id())
	plan.Reason = types.StringPointerValue(ban.Reason)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *banResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state banModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ban, err := r.client.GetBan(ctx, state.ServerID.ValueString(), state.UserID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read ban", err)
		return
	}
	state.ID = types.StringValue(state.id())
	state.Reason = types.StringPointerValue(ban.Reason)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only runs for audit_log_reason and delete_message_seconds, which
// Discord has nothing to update for once the ban exists.
func (r *banResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan banModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *banResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state banModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	err := r.client.RemoveBan(ctx, state.ServerID.ValueString(), state.UserID.ValueString())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "lift ban", err)
	}
}

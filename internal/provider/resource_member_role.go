package provider

import (
	"context"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure   = &memberRoleResource{}
	_ resource.ResourceWithImportState = &memberRoleResource{}
	_ resource.ResourceWithIdentity    = &memberRoleResource{}
)

type memberRoleResource struct {
	resourceIdentity
	client *discord.Client
}

type memberRoleModel struct {
	ID             types.String `tfsdk:"id"`
	ServerID       types.String `tfsdk:"server_id"`
	UserID         types.String `tfsdk:"user_id"`
	RoleID         types.String `tfsdk:"role_id"`
	AuditLogReason types.String `tfsdk:"audit_log_reason"`
}

func newMemberRoleResource() resource.Resource {
	return &memberRoleResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		serverIdentity("server_id"),
		{name: "user_id", description: "ID of the member.", state: []string{"user_id"}},
		{name: "role_id", description: "ID of the role.", state: []string{"role_id"}},
	}}}
}

func (r *memberRoleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_member_role"
}

func (r *memberRoleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	snowflake := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: desc,
			Required:            true,
			Validators:          []validator.String{snowflakeValidator()},
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Grants one role to a server member. Other roles the member has are left alone. If the role " +
			"is removed outside Terraform it is granted again on the next apply.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttribute(),
			"id":               idAttribute("`server_id/user_id/role_id`."),
			"server_id":        serverIDAttribute(),
			"user_id":          snowflake("ID of the member's user."),
			"role_id":          snowflake("ID of the role to grant."),
		},
	}
}

func (r *memberRoleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *memberRoleModel) id() string {
	return m.ServerID.ValueString() + "/" + m.UserID.ValueString() + "/" + m.RoleID.ValueString()
}

func (r *memberRoleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan memberRoleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	if err := r.client.AddMemberRole(ctx, plan.ServerID.ValueString(), plan.UserID.ValueString(), plan.RoleID.ValueString()); err != nil {
		apiError(&resp.Diagnostics, "grant role", err)
		return
	}
	plan.ID = types.StringValue(plan.id())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *memberRoleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state memberRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	member, err := r.client.GetMember(ctx, state.ServerID.ValueString(), state.UserID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read member", err)
		return
	}
	if !slices.Contains(member.Roles, state.RoleID.ValueString()) {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(state.id())
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *memberRoleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	resp.Diagnostics.AddError("Unexpected update", "All discord_member_role attributes force replacement.")
}

func (r *memberRoleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state memberRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	err := r.client.RemoveMemberRole(ctx, state.ServerID.ValueString(), state.UserID.ValueString(), state.RoleID.ValueString())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "revoke role", err)
	}
}

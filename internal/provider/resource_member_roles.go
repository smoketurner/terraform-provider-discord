package provider

import (
	"context"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure      = &memberRolesResource{}
	_ resource.ResourceWithImportState    = &memberRolesResource{}
	_ resource.ResourceWithIdentity       = &memberRolesResource{}
	_ resource.ResourceWithValidateConfig = &memberRolesResource{}
)

// maxMemberRoles is the most roles Modify Guild Member accepts in one request.
const maxMemberRoles = 350

type memberRolesResource struct {
	resourceIdentity
	client *discord.Client
}

type memberRolesModel struct {
	ID             types.String `tfsdk:"id"`
	ServerID       types.String `tfsdk:"server_id"`
	UserID         types.String `tfsdk:"user_id"`
	RoleIDs        types.Set    `tfsdk:"role_ids"`
	AuditLogReason types.String `tfsdk:"audit_log_reason"`
}

func newMemberRolesResource() resource.Resource {
	return &memberRolesResource{resourceIdentity: memberIdentity()}
}

// memberIdentity identifies the resources that manage one server member.
func memberIdentity() resourceIdentity {
	return resourceIdentity{attrs: []identityAttribute{
		serverIdentity("server_id"),
		{name: "user_id", description: "ID of the member.", state: []string{"user_id"}},
	}}
}

// memberUserIDAttribute is the user ID of an existing member, which forces
// replacement.
func memberUserIDAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "ID of the member's user. The user must already be a member of the server.",
		Required:            true,
		Validators:          []validator.String{snowflakeValidator()},
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

func (r *memberRolesResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_member_roles"
}

func (r *memberRolesResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Sets the complete list of roles a server member has. Roles not listed in `role_ids` are " +
			"removed from the member, including roles granted outside Terraform, which the next apply removes again.\n\n" +
			"Managed roles, which Discord grants and revokes itself (bot, integration and Server Booster roles), are " +
			"left alone and cannot be listed. The member always has `@everyone`, which cannot be listed either. The bot " +
			"needs `MANAGE_ROLES` and can only grant and revoke roles below its highest role.\n\n" +
			"Do not use this resource together with `discord_member_role` for the same member: each would undo the " +
			"other's changes. Destroying the resource revokes the roles in `role_ids`.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttribute(),
			"id":               idAttribute("`server_id/user_id`."),
			"server_id":        serverIDAttribute(),
			"user_id":          memberUserIDAttribute(),
			"role_ids": schema.SetAttribute{
				MarkdownDescription: "IDs of every role the member should have, apart from managed roles and " +
					"`@everyone`. An empty set revokes all of them. Up to 350 roles.",
				Required:    true,
				ElementType: types.StringType,
				Validators: []validator.Set{
					setvalidator.SizeAtMost(maxMemberRoles),
					setvalidator.ValueStringsAre(snowflakeValidator()),
				},
			},
		},
	}
}

func (r *memberRolesResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

// ValidateConfig rejects @everyone, whose ID is the server ID: every member
// has it implicitly and Discord does not list it among a member's roles.
func (r *memberRolesResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg memberRolesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.ServerID.IsUnknown() || cfg.RoleIDs.IsUnknown() {
		return
	}
	var ids []types.String
	resp.Diagnostics.Append(cfg.RoleIDs.ElementsAs(ctx, &ids, false)...)
	if slices.Contains(ids, cfg.ServerID) {
		resp.Diagnostics.AddAttributeError(path.Root("role_ids"), "Invalid role",
			"role_ids must not contain the @everyone role (the server ID): every member has it.")
	}
}

func (m *memberRolesModel) id() string {
	return m.ServerID.ValueString() + "/" + m.UserID.ValueString()
}

func (m *memberRolesModel) roleIDs(ctx context.Context, diags *diag.Diagnostics) []string {
	var ids []string
	diags.Append(m.RoleIDs.ElementsAs(ctx, &ids, false)...)
	return ids
}

// managedRoles returns the IDs of the server's managed roles.
func (r *memberRolesResource) managedRoles(ctx context.Context, serverID string) (map[string]bool, error) {
	roles, err := r.client.ListRoles(ctx, serverID)
	if err != nil {
		return nil, err
	}
	managed := map[string]bool{}
	for _, role := range roles {
		if role.Managed {
			managed[role.ID] = true
		}
	}
	return managed, nil
}

func (r *memberRolesResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan memberRolesModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *memberRolesResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan memberRolesModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// apply replaces the member's roles with the planned ones plus the managed
// roles the member already has, which Discord refuses to add or remove.
func (r *memberRolesResource) apply(ctx context.Context, plan *memberRolesModel, diags *diag.Diagnostics) {
	serverID, userID := plan.ServerID.ValueString(), plan.UserID.ValueString()
	// An empty role_ids is sent as an empty list rather than JSON null.
	want := append([]string{}, plan.roleIDs(ctx, diags)...)
	if diags.HasError() {
		return
	}
	managed, err := r.managedRoles(ctx, serverID)
	if err != nil {
		apiError(diags, "list roles", err)
		return
	}
	for _, id := range want {
		if managed[id] {
			diags.AddAttributeError(path.Root("role_ids"), "Managed role",
				fmt.Sprintf("Role %s is managed by Discord or an integration and cannot be granted or revoked; remove it from role_ids.", id))
		}
	}
	if diags.HasError() {
		return
	}
	member, err := r.client.GetMember(ctx, serverID, userID)
	if err != nil {
		apiError(diags, "read member", err)
		return
	}
	for _, id := range member.Roles {
		if managed[id] {
			want = append(want, id)
		}
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	if _, err := r.client.ModifyMember(ctx, serverID, userID, discord.Payload{"roles": want}); err != nil {
		apiError(diags, "set member roles", err)
		return
	}
	plan.ID = types.StringValue(plan.id())
}

func (r *memberRolesResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state memberRolesModel
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
	managed, err := r.managedRoles(ctx, state.ServerID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "list roles", err)
		return
	}
	ids := slices.DeleteFunc(slices.Clone(member.Roles), func(id string) bool { return managed[id] })
	state.RoleIDs = stringSetValue(ctx, ids, &resp.Diagnostics)
	state.ID = types.StringValue(state.id())
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *memberRolesResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state memberRolesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	revoke := state.roleIDs(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	serverID, userID := state.ServerID.ValueString(), state.UserID.ValueString()
	member, err := r.client.GetMember(ctx, serverID, userID)
	if discord.IsNotFound(err) {
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read member", err)
		return
	}
	keep := slices.DeleteFunc(append([]string{}, member.Roles...), func(id string) bool { return slices.Contains(revoke, id) })
	if len(keep) == len(member.Roles) {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	_, err = r.client.ModifyMember(ctx, serverID, userID, discord.Payload{"roles": keep})
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "revoke member roles", err)
	}
}

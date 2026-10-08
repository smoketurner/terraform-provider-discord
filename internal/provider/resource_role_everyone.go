package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure   = &roleEveryoneResource{}
	_ resource.ResourceWithImportState = &roleEveryoneResource{}
	_ resource.ResourceWithIdentity    = &roleEveryoneResource{}
)

type roleEveryoneResource struct {
	resourceIdentity
	client *discord.Client
}

type roleEveryoneModel struct {
	ID             types.String `tfsdk:"id"`
	ServerID       types.String `tfsdk:"server_id"`
	Permissions    types.String `tfsdk:"permissions"`
	AuditLogReason types.String `tfsdk:"audit_log_reason"`
}

func newRoleEveryoneResource() resource.Resource {
	return &roleEveryoneResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{serverIdentity("server_id", "id")}}}
}

func (r *roleEveryoneResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role_everyone"
}

func (r *roleEveryoneResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the permissions of a server's `@everyone` role. The role always exists, so " +
			"destroying this resource only removes it from Terraform state and leaves the permissions unchanged.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttribute(),
			"id":               idAttribute("Role ID (identical to the server ID)."),
			"server_id":        serverIDAttribute(),
			"permissions": schema.StringAttribute{
				MarkdownDescription: "Permission bitfield as a decimal string. Use `provider::discord::permissions([...])` to build it.",
				Required:            true,
				Validators:          []validator.String{permissionsValidator()},
			},
		},
	}
}

func (r *roleEveryoneResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (r *roleEveryoneResource) write(ctx context.Context, m *roleEveryoneModel) error {
	role, err := r.client.ModifyRole(ctx, m.ServerID.ValueString(), m.ServerID.ValueString(),
		discord.Payload{"permissions": m.Permissions.ValueString()})
	if err != nil {
		return err
	}
	m.ID = types.StringValue(role.ID)
	m.Permissions = types.StringValue(role.Permissions)
	return nil
}

func (r *roleEveryoneResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan roleEveryoneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	if err := r.write(ctx, &plan); err != nil {
		apiError(&resp.Diagnostics, "update @everyone role", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *roleEveryoneResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state roleEveryoneModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	role, err := r.client.GetRole(ctx, state.ServerID.ValueString(), state.ServerID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read @everyone role", err)
		return
	}
	state.ID = types.StringValue(role.ID)
	state.Permissions = types.StringValue(role.Permissions)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *roleEveryoneResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan roleEveryoneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	if err := r.write(ctx, &plan); err != nil {
		apiError(&resp.Diagnostics, "update @everyone role", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *roleEveryoneResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

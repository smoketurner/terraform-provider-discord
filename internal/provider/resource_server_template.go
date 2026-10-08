package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure   = &serverTemplateResource{}
	_ resource.ResourceWithImportState = &serverTemplateResource{}
	_ resource.ResourceWithIdentity    = &serverTemplateResource{}
)

type serverTemplateResource struct {
	resourceIdentity
	client *discord.Client
}

type serverTemplateModel struct {
	ID          types.String `tfsdk:"id"`
	ServerID    types.String `tfsdk:"server_id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Code        types.String `tfsdk:"code"`
	CreatorID   types.String `tfsdk:"creator_id"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
	UsageCount  types.Int64  `tfsdk:"usage_count"`
	IsDirty     types.Bool   `tfsdk:"is_dirty"`
}

func newServerTemplateResource() resource.Resource {
	return &serverTemplateResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		serverIdentity("server_id"),
		{name: "code", description: "Template code.", state: []string{"id", "code"}},
	}}}
}

func (r *serverTemplateResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_template"
}

func (r *serverTemplateResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a server template, a snapshot of a server's settings, roles and channels that " +
			"people can create new servers from. A server can have one template. The snapshot is taken when the " +
			"template is created; `is_dirty` reports when the server has changed since, and the " +
			"`discord_sync_server_template` action updates it. Bots cannot create servers from templates. Requires " +
			"the Manage Server permission. Discord records no audit log reason for templates.",
		Attributes: map[string]schema.Attribute{
			"id":        idAttribute("Template code."),
			"server_id": serverIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Template name (1-100 characters).",
				Required:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 100)},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Template description (up to 120 characters).",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 120)},
			},
			"code": schema.StringAttribute{
				MarkdownDescription: "Template code, the unique ID people use to create a server from the template.",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"creator_id": schema.StringAttribute{
				MarkdownDescription: "ID of the user who created the template.",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the template was created (ISO 8601).",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"updated_at": schema.StringAttribute{
				MarkdownDescription: "When the template was last synced to the server (ISO 8601).",
				Computed:            true,
			},
			"usage_count": schema.Int64Attribute{
				MarkdownDescription: "Number of times the template has been used.",
				Computed:            true,
			},
			"is_dirty": schema.BoolAttribute{
				MarkdownDescription: "Whether the server has changed since the template was last synced.",
				Computed:            true,
			},
		},
	}
}

func (r *serverTemplateResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *serverTemplateModel) apply(t *discord.GuildTemplate) {
	m.ID = types.StringValue(t.Code)
	m.Code = types.StringValue(t.Code)
	m.ServerID = types.StringValue(t.SourceGuildID)
	m.Name = types.StringValue(t.Name)
	m.Description = stringPtrValue(t.Description)
	m.CreatorID = types.StringValue(t.CreatorID)
	m.CreatedAt = types.StringValue(t.CreatedAt)
	m.UpdatedAt = types.StringValue(t.UpdatedAt)
	m.UsageCount = types.Int64Value(t.UsageCount)
	m.IsDirty = types.BoolValue(t.IsDirty != nil && *t.IsDirty)
}

func (m *serverTemplateModel) payload() discord.Payload {
	p := discord.Payload{"name": m.Name.ValueString()}
	putString(p, "description", m.Description)
	return p
}

func (r *serverTemplateResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan serverTemplateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t, err := r.client.CreateGuildTemplate(ctx, plan.ServerID.ValueString(), plan.payload())
	if err != nil {
		apiError(&resp.Diagnostics, "create server template", err)
		return
	}
	plan.apply(t)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverTemplateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state serverTemplateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	templates, err := r.client.ListGuildTemplates(ctx, state.ServerID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "list server templates", err)
		return
	}
	for i := range templates {
		if templates[i].Code == state.ID.ValueString() {
			state.apply(&templates[i])
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *serverTemplateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan, state serverTemplateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t, err := r.client.ModifyGuildTemplate(ctx, state.ServerID.ValueString(), state.ID.ValueString(), plan.payload())
	if err != nil {
		apiError(&resp.Diagnostics, "update server template", err)
		return
	}
	plan.apply(t)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverTemplateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serverTemplateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteGuildTemplate(ctx, state.ServerID.ValueString(), state.ID.ValueString())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete server template", err)
	}
}

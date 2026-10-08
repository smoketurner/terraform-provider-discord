package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// entitlementOwnerTypes are the owner_type values of Create Test Entitlement.
var entitlementOwnerTypes = enumMapping{"", "server", "user"}

var (
	_ resource.ResourceWithConfigure   = &testEntitlementResource{}
	_ resource.ResourceWithImportState = &testEntitlementResource{}
	_ resource.ResourceWithIdentity    = &testEntitlementResource{}
)

type testEntitlementResource struct {
	resourceIdentity
	client *discord.Client
}

type testEntitlementModel struct {
	ID            types.String `tfsdk:"id"`
	ApplicationID types.String `tfsdk:"application_id"`
	SKUID         types.String `tfsdk:"sku_id"`
	OwnerType     types.String `tfsdk:"owner_type"`
	OwnerID       types.String `tfsdk:"owner_id"`
	Type          types.String `tfsdk:"type"`
	Consumed      types.Bool   `tfsdk:"consumed"`
}

func newTestEntitlementResource() resource.Resource {
	return &testEntitlementResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		applicationIdentity("application_id"),
		{name: "entitlement_id", description: "ID of the entitlement.", state: []string{"id"}},
	}}}
}

func (r *testEntitlementResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_test_entitlement"
}

func (r *testEntitlementResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Grants a server or user a test entitlement to an SKU of the application, so premium " +
			"features can be tested without a purchase. Discord treats the owner as having the SKU until the " +
			"entitlement is deleted, which destroying this resource does; reload the Discord client to see the change. " +
			"A test entitlement never ends. Every argument forces a new entitlement.",
		Attributes: map[string]schema.Attribute{
			"id":             idAttribute("Entitlement ID."),
			"application_id": resourceApplicationIDAttribute(),
			"sku_id": schema.StringAttribute{
				MarkdownDescription: "ID of the SKU, such as one from `data.discord_skus`. For a subscription, use the SKU " +
					"of type `subscription`.",
				Required:      true,
				Validators:    []validator.String{snowflakeValidator()},
				PlanModifiers: replace,
			},
			"owner_type": schema.StringAttribute{
				MarkdownDescription: "Whether the entitlement is granted to a server or a user: " + entitlementOwnerTypes.doc() + ".",
				Required:            true,
				Validators:          []validator.String{entitlementOwnerTypes.validator()},
				PlanModifiers:       replace,
			},
			"owner_id": schema.StringAttribute{
				MarkdownDescription: "ID of the server or user.",
				Required:            true,
				Validators:          []validator.String{snowflakeValidator()},
				PlanModifiers:       replace,
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "Entitlement type: " + entitlementTypes.doc() + ".",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"consumed": schema.BoolAttribute{
				MarkdownDescription: "For a consumable SKU, whether the entitlement has been consumed.",
				Computed:            true,
			},
		},
	}
}

func (r *testEntitlementResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *testEntitlementModel) apply(e *discord.Entitlement) {
	m.ID = types.StringValue(e.ID)
	m.SKUID = types.StringValue(e.SKUID)
	switch {
	case e.GuildID != nil:
		m.OwnerType, m.OwnerID = types.StringValue("server"), types.StringValue(*e.GuildID)
	case e.UserID != nil:
		m.OwnerType, m.OwnerID = types.StringValue("user"), types.StringValue(*e.UserID)
	}
	m.Type = entitlementTypes.name(e.Type)
	m.Consumed = types.BoolPointerValue(e.Consumed)
}

func (r *testEntitlementResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan testEntitlementModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.ApplicationID.IsUnknown() {
		id, err := r.client.ApplicationID(ctx)
		if err != nil {
			apiError(&resp.Diagnostics, "read the bot's application", err)
			return
		}
		plan.ApplicationID = types.StringValue(id)
	}
	ownerType, _ := entitlementOwnerTypes.value(plan.OwnerType.ValueString())
	e, err := r.client.CreateTestEntitlement(ctx, plan.ApplicationID.ValueString(), discord.Payload{
		"sku_id": plan.SKUID.ValueString(), "owner_id": plan.OwnerID.ValueString(), "owner_type": ownerType,
	})
	if err != nil {
		apiError(&resp.Diagnostics, "create test entitlement", err)
		return
	}
	plan.apply(e)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read removes the resource when the entitlement was deleted outside
// Terraform: Discord keeps deleted entitlements and marks them deleted.
func (r *testEntitlementResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state testEntitlementModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.client.GetEntitlement(ctx, state.ApplicationID.ValueString(), state.ID.ValueString())
	if discord.IsNotFound(err) || err == nil && e.Deleted {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read test entitlement", err)
		return
	}
	state.apply(e)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only stores the plan: every argument forces replacement.
func (r *testEntitlementResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan testEntitlementModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *testEntitlementResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state testEntitlementModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteTestEntitlement(ctx, state.ApplicationID.ValueString(), state.ID.ValueString())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete test entitlement", err)
	}
}

package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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
	_ resource.ResourceWithConfigure   = &emojiResource{}
	_ resource.ResourceWithImportState = &emojiResource{}
)

type emojiResource struct {
	client *discord.Client
}

type emojiModel struct {
	ID       types.String `tfsdk:"id"`
	ServerID types.String `tfsdk:"server_id"`
	Name     types.String `tfsdk:"name"`
	Image    types.String `tfsdk:"image"`
	Roles    types.Set    `tfsdk:"roles"`
	Animated types.Bool   `tfsdk:"animated"`
	Managed  types.Bool   `tfsdk:"managed"`
}

func newEmojiResource() resource.Resource { return &emojiResource{} }

func (r *emojiResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_emoji"
}

func (r *emojiResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a custom server emoji. Requires the Create Expressions permission.",
		Attributes: map[string]schema.Attribute{
			"id":        idAttribute("Emoji ID."),
			"server_id": serverIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Emoji name (2-32 letters, digits or underscores).",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z0-9_]{2,32}$`), "must be 2-32 letters, digits or underscores"),
				},
			},
			"image": schema.StringAttribute{
				MarkdownDescription: "Image as a data URI (PNG, JPEG, GIF or WebP, at most 256 KiB), e.g. " +
					"`\"data:image/png;base64,${filebase64(\"emoji.png\")}\"`. Changing it uploads a new emoji.",
				Required:   true,
				Validators: []validator.String{dataURIValidator()},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
						// Imported emojis have no image in state; adopting the configured one needs no new upload.
						resp.RequiresReplace = !req.StateValue.IsNull()
					},
					"Changing the image uploads a new emoji.", "Changing the image uploads a new emoji.",
				)},
			},
			"roles": schema.SetAttribute{
				MarkdownDescription: "Role IDs allowed to use the emoji. Omit to allow everyone.",
				ElementType:         types.StringType,
				Optional:            true,
				Validators:          []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueStringsAre(snowflakeValidator())},
			},
			"animated": schema.BoolAttribute{
				MarkdownDescription: "Whether the emoji is animated.",
				Computed:            true,
			},
			"managed": schema.BoolAttribute{
				MarkdownDescription: "Whether the emoji is managed by an integration.",
				Computed:            true,
			},
		},
	}
}

func (r *emojiResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *emojiModel) roles(ctx context.Context, diags *diag.Diagnostics) []string {
	roles := []string{}
	if !m.Roles.IsNull() {
		diags.Append(m.Roles.ElementsAs(ctx, &roles, false)...)
	}
	return roles
}

func (m *emojiModel) apply(ctx context.Context, e *discord.Emoji, diags *diag.Diagnostics) {
	m.ID = types.StringValue(e.ID)
	m.Name = types.StringValue(e.Name)
	m.Animated = types.BoolValue(e.Animated)
	m.Managed = types.BoolValue(e.Managed)
	if len(e.Roles) == 0 {
		m.Roles = types.SetNull(types.StringType)
	} else {
		m.Roles = stringSetValue(ctx, e.Roles, diags)
	}
}

func (r *emojiResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan emojiModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	roles := plan.roles(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.client.CreateEmoji(ctx, plan.ServerID.ValueString(), discord.Payload{
		"name": plan.Name.ValueString(), "image": plan.Image.ValueString(), "roles": roles,
	})
	if err != nil {
		apiError(&resp.Diagnostics, "create emoji", err)
		return
	}
	plan.apply(ctx, e, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *emojiResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state emojiModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.client.GetEmoji(ctx, state.ServerID.ValueString(), state.ID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read emoji", err)
		return
	}
	state.apply(ctx, e, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *emojiResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan emojiModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	roles := plan.roles(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.client.ModifyEmoji(ctx, plan.ServerID.ValueString(), plan.ID.ValueString(), discord.Payload{
		"name": plan.Name.ValueString(), "roles": roles,
	})
	if err != nil {
		apiError(&resp.Diagnostics, "update emoji", err)
		return
	}
	plan.apply(ctx, e, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *emojiResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state emojiModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteEmoji(ctx, state.ServerID.ValueString(), state.ID.ValueString())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete emoji", err)
	}
}

func (r *emojiResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitID(req.ID, 2, "server_id/emoji_id")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}

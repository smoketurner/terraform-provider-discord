package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure   = &inviteResource{}
	_ resource.ResourceWithImportState = &inviteResource{}
)

type inviteResource struct {
	client *discord.Client
}

type inviteModel struct {
	ID        types.String `tfsdk:"id"`
	ChannelID types.String `tfsdk:"channel_id"`
	MaxAge    types.Int64  `tfsdk:"max_age"`
	MaxUses   types.Int64  `tfsdk:"max_uses"`
	Temporary types.Bool   `tfsdk:"temporary"`
	Unique    types.Bool   `tfsdk:"unique"`
	Code      types.String `tfsdk:"code"`
	URL       types.String `tfsdk:"url"`
	ExpiresAt types.String `tfsdk:"expires_at"`
}

func newInviteResource() resource.Resource { return &inviteResource{} }

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
			"id": idAttribute("Invite code."),
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
		},
	}
}

func (r *inviteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
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
	var plan inviteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	inv, err := r.client.CreateInvite(ctx, plan.ChannelID.ValueString(), discord.Payload{
		"max_age":   plan.MaxAge.ValueInt64(),
		"max_uses":  plan.MaxUses.ValueInt64(),
		"temporary": plan.Temporary.ValueBool(),
		"unique":    plan.Unique.ValueBool(),
	})
	if err != nil {
		apiError(&resp.Diagnostics, "create invite", err)
		return
	}
	// Discord documents invite metadata (max_age, max_uses, temporary) only
	// on Get Channel Invites, so keep the requested values; Read refreshes them.
	maxAge, maxUses, temporary := plan.MaxAge, plan.MaxUses, plan.Temporary
	plan.apply(inv)
	plan.MaxAge, plan.MaxUses, plan.Temporary = maxAge, maxUses, temporary
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *inviteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
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
			if state.Unique.IsNull() {
				state.Unique = types.BoolValue(true)
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *inviteResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Unexpected update", "All discord_invite arguments force replacement.")
}

func (r *inviteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state inviteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteInvite(ctx, state.ID.ValueString()); err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete invite", err)
	}
}

func (r *inviteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitID(req.ID, 2, "channel_id/code")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("channel_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}

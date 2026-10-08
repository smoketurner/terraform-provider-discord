package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure   = &botMemberResource{}
	_ resource.ResourceWithImportState = &botMemberResource{}
	_ resource.ResourceWithIdentity    = &botMemberResource{}
)

type botMemberResource struct {
	resourceIdentity
	client *discord.Client
}

type botMemberModel struct {
	ID              types.String `tfsdk:"id"`
	ServerID        types.String `tfsdk:"server_id"`
	UserID          types.String `tfsdk:"user_id"`
	Nick            types.String `tfsdk:"nick"`
	Bio             types.String `tfsdk:"bio"`
	Avatar          types.String `tfsdk:"avatar"`
	AvatarWO        types.String `tfsdk:"avatar_wo"`
	AvatarWOVersion types.Int64  `tfsdk:"avatar_wo_version"`
	AvatarHash      types.String `tfsdk:"avatar_hash"`
	Banner          types.String `tfsdk:"banner"`
	BannerWO        types.String `tfsdk:"banner_wo"`
	BannerWOVersion types.Int64  `tfsdk:"banner_wo_version"`
	BannerHash      types.String `tfsdk:"banner_hash"`
	AuditLogReason  types.String `tfsdk:"audit_log_reason"`
}

func newBotMemberResource() resource.Resource {
	return &botMemberResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{serverIdentity("server_id", "id")}}}
}

func (r *botMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bot_member"
}

func (r *botMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"audit_log_reason": auditLogReasonAttribute(),
		"id":               idAttribute("Server ID."),
		"server_id":        serverIDAttribute(),
		"user_id": schema.StringAttribute{
			MarkdownDescription: "ID of the bot's user.",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"nick": schema.StringAttribute{
			MarkdownDescription: "The bot's nickname in the server, 1 to 32 characters without leading or trailing " +
				"whitespace. Requires `CHANGE_NICKNAME`.",
			Optional: true,
			Validators: []validator.String{
				stringvalidator.UTF8LengthBetween(1, 32),
				stringvalidator.RegexMatches(untrimmedRegexp, "must not start or end with whitespace"),
			},
		},
		"bio": schema.StringAttribute{
			MarkdownDescription: "The bot's bio in the server, at most 300 characters. Discord does not return the " +
				"bio, so a change made outside Terraform is not detected.",
			Optional:   true,
			Validators: []validator.String{stringvalidator.UTF8LengthBetween(1, 300)},
		},
	}
	imageAttributes(attrs, "avatar", "Server profile", "avatar", "")
	imageAttributes(attrs, "banner", "Server profile", "banner", "")
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages how the bot the provider authenticates as appears in one server: its nickname, " +
			"bio, avatar and banner, which override its global profile there. To change the global profile, use " +
			"`discord_bot_user`.\n\n" +
			"Each argument is managed only while it is set. Removing `nick` or `bio` from the configuration clears " +
			"it; removing an image leaves it in place. Destroying the resource clears the nickname, bio and images it " +
			"manages, so the bot appears with its global profile again.",
		Attributes: attrs,
	}
}

func (r *botMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *botMemberModel) images() []imageArg[discord.Member] {
	return []imageArg[discord.Member]{
		{"avatar", &m.Avatar, &m.AvatarWOVersion, &m.AvatarHash, func(mb *discord.Member) *string { return mb.Avatar }},
		{"banner", &m.Banner, &m.BannerWOVersion, &m.BannerHash, func(mb *discord.Member) *string { return mb.Banner }},
	}
}

func (m *botMemberModel) apply(mb *discord.Member) {
	m.ID = m.ServerID
	if mb.User != nil {
		m.UserID = types.StringValue(mb.User.ID)
	}
	setImageHashes(m.images(), mb)
}

// modify sends the changes in p, or reads the bot's membership when there are
// none.
func (r *botMemberResource) modify(ctx context.Context, m *botMemberModel, p discord.Payload, diags *diag.Diagnostics) *discord.Member {
	serverID := m.ServerID.ValueString()
	if len(p) > 0 {
		mb, err := r.client.ModifyCurrentMember(withAuditLogReason(ctx, m.AuditLogReason), serverID, p)
		if err != nil {
			apiError(diags, "update bot member", err)
			return nil
		}
		return mb
	}
	mb, err := r.get(ctx, m)
	if err != nil {
		apiError(diags, "read bot member", err)
	}
	return mb
}

// get fetches the bot's membership, looking up the bot's user ID if it is not
// in state yet, as after an import.
func (r *botMemberResource) get(ctx context.Context, m *botMemberModel) (*discord.Member, error) {
	userID := m.UserID.ValueString()
	if userID == "" {
		u, err := r.client.GetCurrentUser(ctx)
		if err != nil {
			return nil, err
		}
		userID = u.ID
	}
	return r.client.GetMember(ctx, m.ServerID.ValueString(), userID)
}

func (r *botMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan botMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := discord.Payload{}
	putKnownString(p, "nick", plan.Nick)
	putKnownString(p, "bio", plan.Bio)
	for _, img := range plan.images() {
		putKnownString(p, img.name, *img.image)
	}
	putWriteOnlyImages(ctx, req.Config, p, plan.images(), nil, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	mb := r.modify(ctx, &plan, p, &resp.Diagnostics)
	if mb == nil {
		return
	}
	plan.apply(mb)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *botMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state botMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	mb, err := r.get(ctx, &state)
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read bot member", err)
		return
	}
	// Import sets only server_id and id, so a null user_id means the
	// arguments have not been read yet.
	if state.UserID.IsNull() || !state.Nick.IsNull() {
		state.Nick = stringPtrValue(mb.Nick)
	}
	clearImagesOnDrift(state.images(), mb)
	state.apply(mb)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *botMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan, state botMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := discord.Payload{}
	if !plan.Nick.Equal(state.Nick) {
		putString(p, "nick", plan.Nick)
	}
	if !plan.Bio.Equal(state.Bio) {
		putString(p, "bio", plan.Bio)
	}
	prior := state.images()
	for i, img := range plan.images() {
		if !img.image.Equal(*prior[i].image) {
			putKnownString(p, img.name, *img.image)
		}
	}
	putWriteOnlyImages(ctx, req.Config, p, plan.images(), prior, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	mb := r.modify(ctx, &plan, p, &resp.Diagnostics)
	if mb == nil {
		return
	}
	plan.apply(mb)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *botMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state botMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := discord.Payload{}
	for name, managed := range map[string]bool{
		"nick":   !state.Nick.IsNull(),
		"bio":    !state.Bio.IsNull(),
		"avatar": !state.Avatar.IsNull() || !state.AvatarWOVersion.IsNull(),
		"banner": !state.Banner.IsNull() || !state.BannerWOVersion.IsNull(),
	} {
		if managed {
			p[name] = nil
		}
	}
	if len(p) == 0 {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	_, err := r.client.ModifyCurrentMember(ctx, state.ServerID.ValueString(), p)
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "reset bot member", err)
	}
}

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure   = &botUserResource{}
	_ resource.ResourceWithImportState = &botUserResource{}
	_ resource.ResourceWithIdentity    = &botUserResource{}
)

type botUserResource struct {
	resourceIdentity
	client *discord.Client
}

type botUserModel struct {
	ID              types.String `tfsdk:"id"`
	Username        types.String `tfsdk:"username"`
	Avatar          types.String `tfsdk:"avatar"`
	AvatarWO        types.String `tfsdk:"avatar_wo"`
	AvatarWOVersion types.Int64  `tfsdk:"avatar_wo_version"`
	AvatarHash      types.String `tfsdk:"avatar_hash"`
	Banner          types.String `tfsdk:"banner"`
	BannerWO        types.String `tfsdk:"banner_wo"`
	BannerWOVersion types.Int64  `tfsdk:"banner_wo_version"`
	BannerHash      types.String `tfsdk:"banner_hash"`
}

func newBotUserResource() resource.Resource {
	return &botUserResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		{name: "user_id", description: "ID of the bot's user.", state: []string{"id"}},
	}}}
}

func (r *botUserResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bot_user"
}

func (r *botUserResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"id": idAttribute("ID of the bot's user."),
		"username": optionalComputedString("The bot's username (2-32 characters). Discord limits how often it can "+
			"change, and changing it may change the bot's discriminator.", stringvalidator.UTF8LengthBetween(2, 32)),
	}
	imageAttributes(attrs, "avatar", "Bot", "avatar", "")
	imageAttributes(attrs, "banner", "Bot", "banner", "")
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the global profile of the bot the provider authenticates as: its username, " +
			"avatar and banner. There is one per bot token. Settings omitted from configuration are left unmanaged. " +
			"Destroying the resource only removes it from Terraform state. To change how the bot appears in one " +
			"server, use `discord_bot_member`.",
		Attributes: attrs,
	}
}

func (r *botUserResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *botUserModel) images() []imageArg[discord.User] {
	return []imageArg[discord.User]{
		{"avatar", &m.Avatar, &m.AvatarWOVersion, &m.AvatarHash, func(u *discord.User) *string { return u.Avatar }},
		{"banner", &m.Banner, &m.BannerWOVersion, &m.BannerHash, func(u *discord.User) *string { return u.Banner }},
	}
}

func (m *botUserModel) payload() discord.Payload {
	p := discord.Payload{}
	putKnownString(p, "username", m.Username)
	for _, img := range m.images() {
		putKnownString(p, img.name, *img.image)
	}
	return p
}

func (m *botUserModel) apply(u *discord.User) {
	m.ID = types.StringValue(u.ID)
	m.Username = types.StringValue(u.Username)
	setImageHashes(m.images(), u)
}

// update sends the changes in p, or reads the user when there are none.
func (r *botUserResource) update(ctx context.Context, p discord.Payload, diags *diag.Diagnostics) *discord.User {
	var u *discord.User
	var err error
	if len(p) == 0 {
		u, err = r.client.GetCurrentUser(ctx)
	} else {
		u, err = r.client.ModifyCurrentUser(ctx, p)
	}
	if err != nil {
		apiError(diags, "update bot user", err)
		return nil
	}
	return u
}

func (r *botUserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan botUserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	u, err := r.client.GetCurrentUser(ctx)
	if err != nil {
		apiError(&resp.Diagnostics, "read bot user", err)
		return
	}
	var current botUserModel
	current.apply(u)
	p := diffPayload(plan.payload(), current.payload())
	putWriteOnlyImages(ctx, req.Config, p, plan.images(), nil, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(p) > 0 {
		if u = r.update(ctx, p, &resp.Diagnostics); u == nil {
			return
		}
	}
	plan.apply(u)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *botUserResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state botUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	u, err := r.client.GetCurrentUser(ctx)
	if err != nil {
		apiError(&resp.Diagnostics, "read bot user", err)
		return
	}
	if state.ID.ValueString() != u.ID {
		resp.Diagnostics.AddError("Wrong bot user", fmt.Sprintf(
			"The provider's token belongs to bot user %s, not %s. A bot can only manage its own user.", u.ID, state.ID.ValueString()))
		return
	}
	clearImagesOnDrift(state.images(), u)
	state.apply(u)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *botUserResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan, state botUserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := diffPayload(plan.payload(), state.payload())
	putWriteOnlyImages(ctx, req.Config, p, plan.images(), state.images(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	u := r.update(ctx, p, &resp.Diagnostics)
	if u == nil {
		return
	}
	plan.apply(u)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete leaves the bot's profile as it is: a user cannot be deleted, and
// its username cannot be cleared.
func (r *botUserResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

type userDataModel struct {
	ID            types.String `tfsdk:"id"`
	Username      types.String `tfsdk:"username"`
	Discriminator types.String `tfsdk:"discriminator"`
	GlobalName    types.String `tfsdk:"global_name"`
	AvatarHash    types.String `tfsdk:"avatar_hash"`
	BannerHash    types.String `tfsdk:"banner_hash"`
	AccentColor   types.Int64  `tfsdk:"accent_color"`
	Bot           types.Bool   `tfsdk:"bot"`
	System        types.Bool   `tfsdk:"system"`
	PublicFlags   types.Int64  `tfsdk:"public_flags"`
}

func userAttributes(id schema.StringAttribute) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":            id,
		"username":      computedString("Username."),
		"discriminator": computedString("Discord tag (discriminator)."),
		"global_name":   computedString("Display name, if set."),
		"avatar_hash":   computedString("Hash of the avatar image."),
		"banner_hash":   computedString("Hash of the profile banner image."),
		"accent_color":  computedInt("Banner color as an RGB integer, if set."),
		"bot":           computedBool("Whether the user belongs to an application."),
		"system":        computedBool("Whether the user is an official Discord system user."),
		"public_flags":  computedInt("Public flags on the account, such as badges, as a bitfield."),
	}
}

func userDataValue(u *discord.User) userDataModel {
	return userDataModel{
		ID:            types.StringValue(u.ID),
		Username:      types.StringValue(u.Username),
		Discriminator: types.StringValue(u.Discriminator),
		GlobalName:    stringPtrValue(u.GlobalName),
		AvatarHash:    stringPtrValue(u.Avatar),
		BannerHash:    stringPtrValue(u.Banner),
		AccentColor:   types.Int64PointerValue(u.AccentColor),
		Bot:           types.BoolValue(u.Bot),
		System:        types.BoolValue(u.System),
		PublicFlags:   types.Int64Value(u.PublicFlags),
	}
}

// User.

type userDataSource struct{ client *discord.Client }

func newUserDataSource() datasource.DataSource { return &userDataSource{} }

func (d *userDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (d *userDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up any Discord user by ID. The user does not need to share a server with the bot.",
		Attributes: userAttributes(schema.StringAttribute{
			MarkdownDescription: "User ID.",
			Required:            true,
			Validators:          []validator.String{snowflakeValidator()},
		}),
	}
}

func (d *userDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromDataSource(req, resp)
}

func (d *userDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m userDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	u, err := d.client.GetUser(ctx, m.ID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "read user", err)
		return
	}
	m = userDataValue(u)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Current user.

type currentUserDataSource struct{ client *discord.Client }

func newCurrentUserDataSource() datasource.DataSource { return &currentUserDataSource{} }

func (d *currentUserDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_current_user"
}

func (d *currentUserDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The bot user the provider authenticates as, e.g. to grant the bot itself a permission overwrite.",
		Attributes:          userAttributes(computedString("ID of the bot user.")),
	}
}

func (d *currentUserDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromDataSource(req, resp)
}

func (d *currentUserDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	u, err := d.client.GetCurrentUser(ctx)
	if err != nil {
		apiError(&resp.Diagnostics, "read current user", err)
		return
	}
	m := userDataValue(u)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Current application.

type currentApplicationDataSource struct{ client *discord.Client }

type currentApplicationDataModel struct {
	ID                      types.String `tfsdk:"id"`
	Name                    types.String `tfsdk:"name"`
	Description             types.String `tfsdk:"description"`
	IconHash                types.String `tfsdk:"icon_hash"`
	BotID                   types.String `tfsdk:"bot_id"`
	OwnerID                 types.String `tfsdk:"owner_id"`
	ServerID                types.String `tfsdk:"server_id"`
	BotPublic               types.Bool   `tfsdk:"bot_public"`
	BotRequireCodeGrant     types.Bool   `tfsdk:"bot_require_code_grant"`
	VerifyKey               types.String `tfsdk:"verify_key"`
	Flags                   types.Int64  `tfsdk:"flags"`
	Tags                    types.Set    `tfsdk:"tags"`
	ApproximateGuildCount   types.Int64  `tfsdk:"approximate_server_count"`
	InteractionsEndpointURL types.String `tfsdk:"interactions_endpoint_url"`
}

func newCurrentApplicationDataSource() datasource.DataSource {
	return &currentApplicationDataSource{}
}

func (d *currentApplicationDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_current_application"
}

func (d *currentApplicationDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The application (app) the bot token belongs to, e.g. for arguments that need an application ID.",
		Attributes: map[string]schema.Attribute{
			"id":                        computedString("Application ID."),
			"name":                      computedString("Application name."),
			"description":               computedString("Application description."),
			"icon_hash":                 computedString("Hash of the application icon."),
			"bot_id":                    computedString("ID of the application's bot user."),
			"owner_id":                  computedString("ID of the application owner. For applications owned by a team, this is the team's placeholder user."),
			"server_id":                 computedString("ID of the server associated with the application, such as a support server."),
			"bot_public":                computedBool("Whether anyone, not only the owner, can add the bot to servers."),
			"bot_require_code_grant":    computedBool("Whether the bot joins servers only after the full OAuth2 code grant flow."),
			"verify_key":                computedString("Hex-encoded public key for verifying interaction requests."),
			"flags":                     computedInt("Public application flags as a bitfield."),
			"tags":                      schema.SetAttribute{MarkdownDescription: "Tags describing the application.", ElementType: types.StringType, Computed: true},
			"approximate_server_count":  computedInt("Approximate number of servers the application has been added to."),
			"interactions_endpoint_url": computedString("URL that receives interactions, if set."),
		},
	}
}

func (d *currentApplicationDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromDataSource(req, resp)
}

func (d *currentApplicationDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	a, err := d.client.GetCurrentApplication(ctx)
	if err != nil {
		apiError(&resp.Diagnostics, "read current application", err)
		return
	}
	m := currentApplicationDataModel{
		ID:                      types.StringValue(a.ID),
		Name:                    types.StringValue(a.Name),
		Description:             types.StringValue(a.Description),
		IconHash:                stringPtrValue(a.Icon),
		BotID:                   types.StringNull(),
		OwnerID:                 types.StringNull(),
		ServerID:                stringPtrValue(&a.GuildID),
		BotPublic:               types.BoolValue(a.BotPublic),
		BotRequireCodeGrant:     types.BoolValue(a.BotRequireCodeGrant),
		VerifyKey:               types.StringValue(a.VerifyKey),
		Flags:                   types.Int64Value(a.Flags),
		Tags:                    stringSetValue(ctx, a.Tags, &resp.Diagnostics),
		ApproximateGuildCount:   types.Int64Value(a.ApproximateGuildCount),
		InteractionsEndpointURL: stringPtrValue(a.InteractionsEndpointURL),
	}
	if a.Bot != nil {
		m.BotID = types.StringValue(a.Bot.ID)
	}
	if a.Owner != nil {
		m.OwnerID = types.StringValue(a.Owner.ID)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Package provider implements the Discord Terraform provider.
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ provider.Provider              = &discordProvider{}
	_ provider.ProviderWithFunctions = &discordProvider{}
)

type discordProvider struct {
	version string
}

type providerModel struct {
	Token   types.String `tfsdk:"token"`
	BaseURL types.String `tfsdk:"base_url"`
}

// New returns a constructor for the provider at the given version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &discordProvider{version: version}
	}
}

func (p *discordProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "discord"
	resp.Version = p.version
}

func (p *discordProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage Discord servers (guilds) with a bot token.",
		Attributes: map[string]schema.Attribute{
			"token": schema.StringAttribute{
				MarkdownDescription: "Discord bot token. May be given with or without the `Bot ` prefix. Can also be set with the `DISCORD_TOKEN` environment variable.",
				Optional:            true,
				Sensitive:           true,
			},
			"base_url": schema.StringAttribute{
				MarkdownDescription: "Discord REST API base URL. Defaults to `" + discord.DefaultBaseURL + "`. Can also be set with the `DISCORD_BASE_URL` environment variable. Intended for testing.",
				Optional:            true,
			},
		},
	}
}

func (p *discordProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.Token.IsUnknown() || cfg.BaseURL.IsUnknown() {
		resp.Diagnostics.AddWarning("Provider configuration unknown",
			"The Discord provider configuration depends on values not known until apply; resources cannot be read during this plan.")
		return
	}

	token := os.Getenv("DISCORD_TOKEN")
	if !cfg.Token.IsNull() {
		token = cfg.Token.ValueString()
	}
	baseURL := os.Getenv("DISCORD_BASE_URL")
	if !cfg.BaseURL.IsNull() {
		baseURL = cfg.BaseURL.ValueString()
	}
	if discord.NormalizeToken(token) == "" {
		resp.Diagnostics.AddAttributeError(path.Root("token"), "Missing Discord bot token",
			"Set the provider's token attribute or the DISCORD_TOKEN environment variable.")
		return
	}

	client := discord.NewClient(baseURL, token, p.version)
	resp.ResourceData = client
	resp.DataSourceData = client
}

func (p *discordProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newServerSettingsResource,
		newRoleResource,
		newRoleEveryoneResource,
		newRolePositionsResource,
		newCategoryChannelResource,
		newTextChannelResource,
		newAnnouncementChannelResource,
		newVoiceChannelResource,
		newStageChannelResource,
		newForumChannelResource,
		newMediaChannelResource,
		newChannelPermissionResource,
		newChannelPositionsResource,
		newMemberRoleResource,
		newWebhookResource,
		newInviteResource,
		newMessageResource,
		newEmojiResource,
	}
}

func (p *discordProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newServerDataSource,
		newRoleDataSource,
		newChannelDataSource,
		newMemberDataSource,
	}
}

func (p *discordProvider) Functions(_ context.Context) []func() function.Function {
	return []func() function.Function{
		newPermissionsFunction,
		newColorFunction,
	}
}

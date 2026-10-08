// Package provider implements the Discord Terraform provider.
package provider

import (
	"context"
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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
	Token          types.String `tfsdk:"token"`
	BaseURL        types.String `tfsdk:"base_url"`
	AuditLogReason types.String `tfsdk:"audit_log_reason"`
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
			"audit_log_reason": schema.StringAttribute{
				MarkdownDescription: "Reason recorded in the server's audit log for every change the provider makes through an " +
					"endpoint that accepts one, for example `Managed by Terraform`. Up to 512 characters. Resources with an " +
					"`audit_log_reason` argument can override it. Can also be set with the `DISCORD_AUDIT_LOG_REASON` " +
					"environment variable.",
				Optional:   true,
				Validators: []validator.String{auditLogReasonValidator()},
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
	if cfg.Token.IsUnknown() || cfg.BaseURL.IsUnknown() || cfg.AuditLogReason.IsUnknown() {
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
	reason := os.Getenv("DISCORD_AUDIT_LOG_REASON")
	if !cfg.AuditLogReason.IsNull() {
		reason = cfg.AuditLogReason.ValueString()
	}
	if n := utf8.RuneCountInString(reason); n > discord.MaxAuditLogReasonLength {
		resp.Diagnostics.AddError("Audit log reason too long",
			fmt.Sprintf("DISCORD_AUDIT_LOG_REASON is %d characters; Discord accepts at most %d.", n, discord.MaxAuditLogReasonLength))
		return
	}
	if discord.NormalizeToken(token) == "" {
		resp.Diagnostics.AddAttributeError(path.Root("token"), "Missing Discord bot token",
			"Set the provider's token attribute or the DISCORD_TOKEN environment variable.")
		return
	}

	client := discord.NewClient(baseURL, token, p.version)
	client.SetAuditLogReason(reason)
	resp.ResourceData = client
	resp.DataSourceData = client
}

func (p *discordProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newServerSettingsResource,
		newServerWidgetResource,
		newWelcomeScreenResource,
		newOnboardingResource,
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
		newChannelFollowerResource,
		newMemberResource,
		newMemberRoleResource,
		newMemberRolesResource,
		newWebhookResource,
		newInviteResource,
		newMessageResource,
		newMessageReactionResource,
		newThreadResource,
		newEmojiResource,
		newScheduledEventResource,
		newStageInstanceResource,
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

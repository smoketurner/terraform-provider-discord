package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var _ ephemeral.EphemeralResourceWithConfigure = &webhookEphemeralResource{}

type webhookEphemeralResource struct {
	client *discord.Client
}

type webhookEphemeralModel struct {
	ID    types.String `tfsdk:"id"`
	Token types.String `tfsdk:"token"`
	URL   types.String `tfsdk:"url"`
}

func newWebhookEphemeralResource() ephemeral.EphemeralResource {
	return &webhookEphemeralResource{}
}

func (r *webhookEphemeralResource) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook"
}

func (r *webhookEphemeralResource) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads the token and URL of an incoming webhook without storing them in Terraform state or " +
			"plan files. Requires Terraform 1.10 or later. Pass the values to write-only arguments, such as " +
			"`value_wo` of `aws_ssm_parameter`, to keep them out of every state. The bot needs the Manage Webhooks " +
			"permission in the webhook's channel unless its application created the webhook.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "ID of the webhook.",
				Required:            true,
				Validators:          []validator.String{snowflakeValidator()},
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "Secure token of the webhook.",
				Computed:            true,
				Sensitive:           true,
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "URL for executing the webhook.",
				Computed:            true,
				Sensitive:           true,
			},
		},
	}
}

func (r *webhookEphemeralResource) Configure(_ context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*discord.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *discord.Client, got %T", req.ProviderData))
	}
	r.client = c
}

func (r *webhookEphemeralResource) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var data webhookEphemeralModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if r.client == nil {
		resp.Diagnostics.AddError("Provider not configured",
			"The Discord provider configuration is not known yet, so the webhook cannot be read.")
		return
	}
	w, err := r.client.GetWebhook(ctx, data.ID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "read webhook", err)
		return
	}
	if w.Token == "" {
		resp.Diagnostics.AddError("Webhook has no token",
			fmt.Sprintf("Webhook %s is not an incoming webhook; Discord returns a token only for incoming webhooks.", w.ID))
		return
	}
	data.Token = types.StringValue(w.Token)
	data.URL = types.StringValue(webhookURL(w))
	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}

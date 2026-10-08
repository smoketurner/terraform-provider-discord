package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure   = &applicationEmojiResource{}
	_ resource.ResourceWithImportState = &applicationEmojiResource{}
	_ resource.ResourceWithIdentity    = &applicationEmojiResource{}
)

type applicationEmojiResource struct {
	resourceIdentity
	client *discord.Client
}

type applicationEmojiModel struct {
	ID             types.String `tfsdk:"id"`
	ApplicationID  types.String `tfsdk:"application_id"`
	Name           types.String `tfsdk:"name"`
	Image          types.String `tfsdk:"image"`
	ImageWO        types.String `tfsdk:"image_wo"`
	ImageWOVersion types.Int64  `tfsdk:"image_wo_version"`
	Animated       types.Bool   `tfsdk:"animated"`
}

func newApplicationEmojiResource() resource.Resource {
	return &applicationEmojiResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		applicationIdentity("application_id"),
		{name: "emoji_id", description: "ID of the emoji.", state: []string{"id"}},
	}}}
}

func (r *applicationEmojiResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_application_emoji"
}

func (r *applicationEmojiResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceImage := "Changing the image uploads a new emoji."
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an emoji owned by an application, which its bot can use in any server, up to " +
			"2000 per application. Discord cannot change an emoji's image, so a new image uploads a new emoji.",
		Attributes: map[string]schema.Attribute{
			"id":             idAttribute("Emoji ID."),
			"application_id": resourceApplicationIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Emoji name (2-32 letters, digits or underscores), unique within the application.",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z0-9_]{2,32}$`), "must be 2-32 letters, digits or underscores"),
				},
			},
			"image": schema.StringAttribute{
				MarkdownDescription: "Image as a data URI (PNG, JPEG, GIF or WebP, 128x128, at most 256 KiB), e.g. " +
					"`\"data:image/png;base64,${filebase64(\"emoji.png\")}\"`. " + replaceImage + " Stored in state; " +
					"prefer `image_wo` on Terraform 1.11 or later. Exactly one of `image` and `image_wo` is required.",
				Optional: true,
				Validators: []validator.String{
					dataURIValidator(),
					stringvalidator.ExactlyOneOf(path.MatchRoot("image_wo")),
				},
				PlanModifiers: []planmodifier.String{replaceOnUpload(replaceImage)},
			},
			"image_wo": writeOnlyImage("Image as a data URI (PNG, JPEG, GIF or WebP, 128x128, at most 256 KiB).", "image"),
			"image_wo_version": writeOnlyVersion("image", "Changing it uploads `image_wo` as a new emoji.",
				replaceOnUploadVersion(replaceImage)),
			"animated": schema.BoolAttribute{
				MarkdownDescription: "Whether the emoji is animated.",
				Computed:            true,
			},
		},
	}
}

func (r *applicationEmojiResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *applicationEmojiModel) apply(e *discord.Emoji) {
	m.ID = types.StringValue(e.ID)
	m.Name = types.StringValue(e.Name)
	m.Animated = types.BoolValue(e.Animated)
}

func (r *applicationEmojiResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan applicationEmojiModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	image := plan.Image
	if image.IsNull() {
		image = writeOnlyString(ctx, req.Config, "image_wo", &resp.Diagnostics)
	}
	appID := applicationID(ctx, r.client, plan.ApplicationID, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.client.CreateApplicationEmoji(ctx, appID, discord.Payload{
		"name": plan.Name.ValueString(), "image": image.ValueString(),
	})
	if err != nil {
		apiError(&resp.Diagnostics, "create application emoji", err)
		return
	}
	plan.ApplicationID = types.StringValue(appID)
	plan.apply(e)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *applicationEmojiResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state applicationEmojiModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.client.GetApplicationEmoji(ctx, state.ApplicationID.ValueString(), state.ID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read application emoji", err)
		return
	}
	state.apply(e)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *applicationEmojiResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan applicationEmojiModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.client.ModifyApplicationEmoji(ctx, plan.ApplicationID.ValueString(), plan.ID.ValueString(), discord.Payload{
		"name": plan.Name.ValueString(),
	})
	if err != nil {
		apiError(&resp.Diagnostics, "update application emoji", err)
		return
	}
	plan.apply(e)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *applicationEmojiResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state applicationEmojiModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteApplicationEmoji(ctx, state.ApplicationID.ValueString(), state.ID.ValueString())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete application emoji", err)
	}
}

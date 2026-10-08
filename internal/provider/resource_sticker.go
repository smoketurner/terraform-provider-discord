package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

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
	_ resource.ResourceWithConfigure   = &stickerResource{}
	_ resource.ResourceWithImportState = &stickerResource{}
	_ resource.ResourceWithIdentity    = &stickerResource{}
)

// maxStickerFileSize is Discord's documented limit for sticker files.
const maxStickerFileSize = 512 << 10

var stickerFormatTypes = enumMapping{1: "png", 2: "apng", 3: "lottie", 4: "gif"}

type stickerResource struct {
	resourceIdentity
	client *discord.Client
}

type stickerModel struct {
	ID             types.String `tfsdk:"id"`
	ServerID       types.String `tfsdk:"server_id"`
	Name           types.String `tfsdk:"name"`
	Description    types.String `tfsdk:"description"`
	Tags           types.String `tfsdk:"tags"`
	File           types.String `tfsdk:"file"`
	FileWO         types.String `tfsdk:"file_wo"`
	FileWOVersion  types.Int64  `tfsdk:"file_wo_version"`
	FormatType     types.String `tfsdk:"format_type"`
	Available      types.Bool   `tfsdk:"available"`
	AuditLogReason types.String `tfsdk:"audit_log_reason"`
}

func newStickerResource() resource.Resource {
	return &stickerResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		serverIdentity("server_id"),
		{name: "sticker_id", description: "ID of the sticker.", state: []string{"id"}},
	}}}
}

func (r *stickerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sticker"
}

const stickerFileDescription = "Sticker file as base64 (PNG, APNG, GIF or Lottie JSON, at most 512 KiB and 320x320 " +
	"pixels, animations at most 5 seconds), e.g. `filebase64(\"sticker.png\")`. Lottie stickers need the server " +
	"feature `VERIFIED` or `PARTNERED`."

func (r *stickerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a custom server sticker. Requires the Create Expressions permission, and Manage " +
			"Expressions for stickers uploaded by others. Free sticker slots depend on the server's boost level.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttribute(),
			"id":               idAttribute("Sticker ID."),
			"server_id":        serverIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Sticker name (2-30 characters).",
				Required:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(2, 30)},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Sticker description (2-100 characters).",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(2, 100)},
			},
			"tags": schema.StringAttribute{
				MarkdownDescription: "Autocomplete and suggestion tags (1-200 characters). The Discord client uses " +
					"the name of an emoji related to the sticker.",
				Required:   true,
				Validators: []validator.String{stringvalidator.UTF8LengthBetween(1, 200)},
			},
			"file": schema.StringAttribute{
				MarkdownDescription: stickerFileDescription + " Changing it uploads a new sticker. Stored in state; " +
					"prefer `file_wo` on Terraform 1.11 or later. Exactly one of `file` and `file_wo` is required.",
				Optional: true,
				Validators: []validator.String{
					stickerFileValidator{},
					stringvalidator.ExactlyOneOf(path.MatchRoot("file_wo")),
				},
				PlanModifiers: []planmodifier.String{replaceOnUpload("Changing the file uploads a new sticker.")},
			},
			"file_wo": writeOnlyUpload(stickerFileDescription, "file", stickerFileValidator{}),
			"file_wo_version": writeOnlyVersion("file", "Changing it uploads `file_wo` as a new sticker.",
				replaceOnUploadVersion("Changing the file version uploads a new sticker.")),
			"format_type": schema.StringAttribute{
				MarkdownDescription: "File format Discord detected: " + stickerFormatTypes.doc() + ".",
				Computed:            true,
			},
			"available": schema.BoolAttribute{
				MarkdownDescription: "Whether the sticker can be used. It may be false after the server loses boosts.",
				Computed:            true,
			},
		},
	}
}

func (r *stickerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *stickerModel) apply(s *discord.Sticker) {
	m.ID = types.StringValue(s.ID)
	m.Name = types.StringValue(s.Name)
	m.Description = stringPtrValue(s.Description)
	m.Tags = types.StringValue(s.Tags)
	m.FormatType = stickerFormatTypes.name(int64(s.FormatType))
	m.Available = types.BoolValue(s.Available)
}

func (r *stickerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan stickerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	file := plan.File
	if file.IsNull() {
		file = writeOnlyString(ctx, req.Config, "file_wo", &resp.Diagnostics)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	data, err := discord.FileContent("", file.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid sticker file", err.Error())
		return
	}
	contentType, filename, _ := detectStickerFile(data)
	s, err := r.client.CreateSticker(ctx, plan.ServerID.ValueString(), &discord.Multipart{
		Payload: discord.Payload{
			"name": plan.Name.ValueString(), "description": plan.Description.ValueString(), "tags": plan.Tags.ValueString(),
		},
		Files: []discord.File{{Field: "file", Name: filename, ContentType: contentType, Data: data}},
	})
	if err != nil {
		apiError(&resp.Diagnostics, "create sticker", err)
		return
	}
	plan.apply(s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *stickerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state stickerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.client.GetSticker(ctx, state.ServerID.ValueString(), state.ID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read sticker", err)
		return
	}
	state.apply(s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *stickerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan stickerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	p := discord.Payload{"name": plan.Name.ValueString(), "tags": plan.Tags.ValueString()}
	putString(p, "description", plan.Description)
	s, err := r.client.ModifySticker(ctx, plan.ServerID.ValueString(), plan.ID.ValueString(), p)
	if err != nil {
		apiError(&resp.Diagnostics, "update sticker", err)
		return
	}
	plan.apply(s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *stickerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state stickerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	err := r.client.DeleteSticker(ctx, state.ServerID.ValueString(), state.ID.ValueString())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete sticker", err)
	}
}

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// detectStickerFile identifies a sticker file by its content, returning the
// content type and file name to upload it with. APNG files are PNG files and
// Lottie files are JSON objects.
func detectStickerFile(data []byte) (contentType, filename string, ok bool) {
	switch {
	case bytes.HasPrefix(data, pngSignature):
		return "image/png", "sticker.png", true
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif", "sticker.gif", true
	case bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) && json.Valid(data):
		return "application/json", "sticker.json", true
	}
	return "", "", false
}

// stickerFileValidator checks the encoding, size and format of a sticker
// file. Dimensions and animation length are left to Discord.
type stickerFileValidator struct{}

func (stickerFileValidator) Description(context.Context) string {
	return "must be a base64-encoded PNG, APNG, GIF or Lottie JSON file of at most 512 KiB"
}

func (v stickerFileValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (stickerFileValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	data, err := discord.FileContent("", req.ConfigValue.ValueString())
	switch {
	case err != nil:
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid sticker file",
			fmt.Sprintf("Value must be base64-encoded file content, e.g. filebase64(\"sticker.png\"): %s.", err))
	case len(data) > maxStickerFileSize:
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid sticker file",
			fmt.Sprintf("The file is %d bytes; stickers are at most 512 KiB (%d bytes).", len(data), maxStickerFileSize))
	default:
		if _, _, ok := detectStickerFile(data); !ok {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid sticker file",
				"The file must be a PNG, APNG, GIF or Lottie JSON file.")
		}
	}
}

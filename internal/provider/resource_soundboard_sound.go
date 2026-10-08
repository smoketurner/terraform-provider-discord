package provider

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/float64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/float64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure   = &soundboardSoundResource{}
	_ resource.ResourceWithImportState = &soundboardSoundResource{}
	_ resource.ResourceWithIdentity    = &soundboardSoundResource{}
)

// maxSoundSize is Discord's documented 512 KB limit for soundboard sounds,
// read as KiB so that no file Discord accepts is rejected here.
const maxSoundSize = 512 << 10

var soundDataURIRegexp = regexp.MustCompile(`^data:audio/(mpeg|mp3|ogg);base64,([A-Za-z0-9+/=]+)$`)

type soundboardSoundResource struct {
	resourceIdentity
	client *discord.Client
}

type soundboardSoundModel struct {
	ID             types.String  `tfsdk:"id"`
	ServerID       types.String  `tfsdk:"server_id"`
	Name           types.String  `tfsdk:"name"`
	Sound          types.String  `tfsdk:"sound"`
	SoundWO        types.String  `tfsdk:"sound_wo"`
	SoundWOVersion types.Int64   `tfsdk:"sound_wo_version"`
	Volume         types.Float64 `tfsdk:"volume"`
	EmojiID        types.String  `tfsdk:"emoji_id"`
	EmojiName      types.String  `tfsdk:"emoji_name"`
	Available      types.Bool    `tfsdk:"available"`
	AuditLogReason types.String  `tfsdk:"audit_log_reason"`
}

func newSoundboardSoundResource() resource.Resource {
	return &soundboardSoundResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		serverIdentity("server_id"),
		{name: "sound_id", description: "ID of the soundboard sound.", state: []string{"id"}},
	}}}
}

func (r *soundboardSoundResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_soundboard_sound"
}

const soundDescription = "Sound as a base64 data URI (MP3 or Ogg, at most 512 KB and 5.2 seconds), e.g. " +
	"`\"data:audio/mpeg;base64,${filebase64(\"sound.mp3\")}\"`."

func (r *soundboardSoundResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a server soundboard sound. Requires the Create Expressions permission, and " +
			"Manage Expressions for sounds uploaded by others.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttribute(),
			"id":               idAttribute("Soundboard sound ID."),
			"server_id":        serverIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Sound name (2-32 characters).",
				Required:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(2, 32)},
			},
			"sound": schema.StringAttribute{
				MarkdownDescription: soundDescription + " Changing it uploads a new sound. Stored in state; prefer " +
					"`sound_wo` on Terraform 1.11 or later. Exactly one of `sound` and `sound_wo` is required.",
				Optional: true,
				Validators: []validator.String{
					soundValidator{},
					stringvalidator.ExactlyOneOf(path.MatchRoot("sound_wo")),
				},
				PlanModifiers: []planmodifier.String{replaceOnUpload("Changing the sound uploads a new sound.")},
			},
			"sound_wo": writeOnlyUpload(soundDescription, "sound", soundValidator{}),
			"sound_wo_version": writeOnlyVersion("sound", "Changing it uploads `sound_wo` as a new sound.",
				replaceOnUploadVersion("Changing the sound version uploads a new sound.")),
			"volume": schema.Float64Attribute{
				MarkdownDescription: "Volume from 0 to 1. Defaults to `1`.",
				Optional:            true,
				Computed:            true,
				Default:             float64default.StaticFloat64(1),
				Validators:          []validator.Float64{float64validator.Between(0, 1)},
			},
			"emoji_id": schema.StringAttribute{
				MarkdownDescription: "ID of a custom emoji for the sound. Conflicts with `emoji_name`.",
				Optional:            true,
				Validators: []validator.String{
					snowflakeValidator(),
					stringvalidator.ConflictsWith(path.MatchRoot("emoji_name")),
				},
			},
			"emoji_name": schema.StringAttribute{
				MarkdownDescription: "Unicode character of a standard emoji for the sound, e.g. `\"🦆\"`.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 32)},
			},
			"available": schema.BoolAttribute{
				MarkdownDescription: "Whether the sound can be used. It may be false after the server loses boosts.",
				Computed:            true,
			},
		},
	}
}

func (r *soundboardSoundResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *soundboardSoundModel) apply(s *discord.SoundboardSound) {
	m.ID = types.StringValue(s.SoundID)
	m.Name = types.StringValue(s.Name)
	m.Volume = types.Float64Value(s.Volume)
	m.EmojiID = stringPtrValue(s.EmojiID)
	m.EmojiName = stringPtrValue(s.EmojiName)
	m.Available = types.BoolValue(s.Available)
}

// payload holds the fields both create and update send. Unset emojis are sent
// as null to clear them.
func (m *soundboardSoundModel) payload() discord.Payload {
	p := discord.Payload{"name": m.Name.ValueString(), "volume": m.Volume.ValueFloat64()}
	putString(p, "emoji_id", m.EmojiID)
	putString(p, "emoji_name", m.EmojiName)
	return p
}

func (r *soundboardSoundResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan soundboardSoundModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	sound := plan.Sound
	if sound.IsNull() {
		sound = writeOnlyString(ctx, req.Config, "sound_wo", &resp.Diagnostics)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	p := plan.payload()
	p["sound"] = sound.ValueString()
	s, err := r.client.CreateSoundboardSound(ctx, plan.ServerID.ValueString(), p)
	if err != nil {
		apiError(&resp.Diagnostics, "create soundboard sound", err)
		return
	}
	plan.apply(s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *soundboardSoundResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state soundboardSoundModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.client.GetSoundboardSound(ctx, state.ServerID.ValueString(), state.ID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read soundboard sound", err)
		return
	}
	state.apply(s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *soundboardSoundResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan soundboardSoundModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	s, err := r.client.ModifySoundboardSound(ctx, plan.ServerID.ValueString(), plan.ID.ValueString(), plan.payload())
	if err != nil {
		apiError(&resp.Diagnostics, "update soundboard sound", err)
		return
	}
	plan.apply(s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *soundboardSoundResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state soundboardSoundModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	err := r.client.DeleteSoundboardSound(ctx, state.ServerID.ValueString(), state.ID.ValueString())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete soundboard sound", err)
	}
}

// soundValidator checks that a sound is an MP3 or Ogg base64 data URI within
// the size limit. Its length is left to Discord.
type soundValidator struct{}

func (soundValidator) Description(context.Context) string {
	return "must be a base64 MP3 or Ogg data URI of at most 512 KB"
}

func (v soundValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (soundValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	m := soundDataURIRegexp.FindStringSubmatch(req.ConfigValue.ValueString())
	if m == nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid sound",
			`Value must be a base64 MP3 or Ogg data URI, e.g. "data:audio/mpeg;base64,${filebase64("sound.mp3")}".`)
		return
	}
	data, err := base64.StdEncoding.DecodeString(m[2])
	switch {
	case err != nil:
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid sound", fmt.Sprintf("Decoding the base64 data: %s.", err))
	case len(data) > maxSoundSize:
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid sound",
			fmt.Sprintf("The sound is %d bytes; soundboard sounds are at most 512 KB.", len(data)))
	}
}

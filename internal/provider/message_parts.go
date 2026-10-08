package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"mime"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// Documented limits of a message's attachments, components and poll.
const (
	maxAttachments         = 10
	maxComponentsV2        = 40
	maxLegacyActionRows    = 5
	componentTypeActionRow = 1
)

// The attachments, components, stickers, poll and flags of discord_message.

type attachmentModel struct {
	Filename      types.String `tfsdk:"filename"`
	Source        types.String `tfsdk:"source"`
	ContentBase64 types.String `tfsdk:"content_base64"`
	SourceHash    types.String `tfsdk:"source_hash"`
	Description   types.String `tfsdk:"description"`
	Spoiler       types.Bool   `tfsdk:"spoiler"`
	ID            types.String `tfsdk:"id"`
	Size          types.Int64  `tfsdk:"size"`
	ContentType   types.String `tfsdk:"content_type"`
}

type pollModel struct {
	Question         types.String `tfsdk:"question"`
	Answers          types.List   `tfsdk:"answers"`
	Duration         types.Int64  `tfsdk:"duration"`
	AllowMultiselect types.Bool   `tfsdk:"allow_multiselect"`
}

type pollAnswerModel struct {
	Text      types.String `tfsdk:"text"`
	EmojiID   types.String `tfsdk:"emoji_id"`
	EmojiName types.String `tfsdk:"emoji_name"`
}

var (
	attachmentAttrTypes = map[string]attr.Type{
		"filename": types.StringType, "source": types.StringType, "content_base64": types.StringType,
		"source_hash": types.StringType, "description": types.StringType, "spoiler": types.BoolType,
		"id": types.StringType, "size": types.Int64Type, "content_type": types.StringType,
	}
	pollAnswerAttrTypes = map[string]attr.Type{"text": types.StringType, "emoji_id": types.StringType, "emoji_name": types.StringType}
	pollAttrTypes       = map[string]attr.Type{
		"question": types.StringType, "answers": types.ListType{ElemType: types.ObjectType{AttrTypes: pollAnswerAttrTypes}},
		"duration": types.Int64Type, "allow_multiselect": types.BoolType,
	}
)

func attachmentsAttribute() schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		MarkdownDescription: "Files attached to the message (at most 10, 25 MiB per request in total). Changing a file's " +
			"`filename`, `source`, `content_base64` or `source_hash` uploads it again and removes the old copy; " +
			"`description` and `spoiler` are edited in place. Reference an attachment from an embed or a component " +
			"as `attachment://<filename>`. Download URLs are signed and expire, so they are not stored.",
		Optional:   true,
		Validators: []validator.List{listvalidator.SizeBetween(1, maxAttachments)},
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"filename": schema.StringAttribute{
					MarkdownDescription: "File name shown in Discord and used in `attachment://` references.",
					Required:            true,
					Validators:          []validator.String{stringvalidator.LengthBetween(1, 1024)},
				},
				"source": schema.StringAttribute{
					MarkdownDescription: "Path of a local file to upload. Its content is not stored in state, so set " +
						"`source_hash` to upload the file again when it changes. Exactly one of `source` and " +
						"`content_base64` is required.",
					Optional: true,
					Validators: []validator.String{
						stringvalidator.LengthAtLeast(1),
						stringvalidator.ExactlyOneOf(path.MatchRelative().AtParent().AtName("content_base64")),
					},
				},
				"content_base64": schema.StringAttribute{
					MarkdownDescription: "File content as base64, e.g. `base64encode(templatefile(...))`. Stored in state.",
					Optional:            true,
					Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				},
				"source_hash": schema.StringAttribute{
					MarkdownDescription: "Any value that changes with the content of `source`, such as " +
						"`filesha256(\"rules.pdf\")`. Changing it uploads the file again.",
					Optional:   true,
					Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
				},
				"description": schema.StringAttribute{
					MarkdownDescription: "Description (alt text) of the file (up to 1024 characters).",
					Optional:            true,
					Validators:          []validator.String{stringvalidator.LengthBetween(1, 1024)},
				},
				"spoiler": schema.BoolAttribute{
					MarkdownDescription: "Whether the file is blurred until clicked. Defaults to `false`.",
					Optional:            true,
					Computed:            true,
					Default:             booldefault.StaticBool(false),
				},
				"id": schema.StringAttribute{
					MarkdownDescription: "Attachment ID.",
					Computed:            true,
				},
				"size": schema.Int64Attribute{
					MarkdownDescription: "Size of the file in bytes.",
					Computed:            true,
				},
				"content_type": schema.StringAttribute{
					MarkdownDescription: "Media type of the file.",
					Computed:            true,
				},
			},
		},
	}
}

func componentsAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Message components as a JSON array, e.g. `jsonencode([...])`, following Discord's " +
			"[component reference](https://docs.discord.com/developers/components/reference). Without " +
			"`components_v2` only action rows are allowed (at most 5); with " +
			"`components_v2` the message is laid out with up to 40 components in total. Fields Discord adds, such as " +
			"generated component `id`s, are not reported as changes.",
		Optional:   true,
		Validators: []validator.String{stringvalidator.LengthAtLeast(2)},
	}
}

func stickerIDsAttribute() schema.ListAttribute {
	return schema.ListAttribute{
		MarkdownDescription: "IDs of up to 3 stickers of the server to send with the message. Discord cannot change " +
			"them after posting, so changing them posts a new message.",
		ElementType:   types.StringType,
		Optional:      true,
		Validators:    []validator.List{listvalidator.SizeBetween(1, 3), listvalidator.ValueStringsAre(snowflakeValidator())},
		PlanModifiers: []planmodifier.List{listplanmodifier.RequiresReplace()},
	}
}

func pollAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "A poll. Discord cannot edit a poll or a message with one, so changing the poll or any " +
			"other content of the message posts a new message. End a poll early with the `discord_end_poll` action.",
		Optional:      true,
		PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
		Attributes: map[string]schema.Attribute{
			"question": schema.StringAttribute{
				MarkdownDescription: "Question (up to 300 characters).",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 300)},
			},
			"answers": schema.ListNestedAttribute{
				MarkdownDescription: "Answers (1-10).",
				Required:            true,
				Validators:          []validator.List{listvalidator.SizeBetween(1, 10)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"text": schema.StringAttribute{
							MarkdownDescription: "Answer text (up to 55 characters).",
							Required:            true,
							Validators:          []validator.String{stringvalidator.LengthBetween(1, 55)},
						},
						"emoji_id": schema.StringAttribute{
							MarkdownDescription: "ID of a custom emoji shown with the answer. Conflicts with `emoji_name`.",
							Optional:            true,
							Validators: []validator.String{
								snowflakeValidator(),
								stringvalidator.ConflictsWith(path.MatchRelative().AtParent().AtName("emoji_name")),
							},
						},
						"emoji_name": schema.StringAttribute{
							MarkdownDescription: "Unicode emoji shown with the answer.",
							Optional:            true,
							Validators:          []validator.String{stringvalidator.LengthBetween(1, 32)},
						},
					},
				},
			},
			"duration": schema.Int64Attribute{
				MarkdownDescription: "Hours the poll is open (1-768, up to 32 days). Defaults to `24`.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(24),
				Validators:          []validator.Int64{int64validator.Between(1, 768)},
			},
			"allow_multiselect": schema.BoolAttribute{
				MarkdownDescription: "Whether people can choose more than one answer. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
		},
	}
}

// flags is the message flags bitfield the configuration sets.
func (m *messageModel) flags() int64 {
	var f int64
	if m.SuppressEmbeds.ValueBool() {
		f |= discord.MessageFlagSuppressEmbeds
	}
	if m.SuppressNotifications.ValueBool() {
		f |= discord.MessageFlagSuppressNotifications
	}
	if m.ComponentsV2.ValueBool() {
		f |= discord.MessageFlagIsComponentsV2
	}
	return f
}

// fullPayload is the payload of discord_message: the shared content, embeds
// and allowed mentions plus flags and components. A Components V2 message
// sends null content, which an edit that turns the flag on requires.
func (m *messageModel) fullPayload(ctx context.Context) (discord.Payload, diag.Diagnostics) {
	p, diags := m.payload(ctx)
	p["flags"] = m.flags()
	if m.ComponentsV2.ValueBool() {
		p["content"] = nil
	}
	p["components"] = json.RawMessage("[]")
	if !m.Components.IsNull() && !m.Components.IsUnknown() {
		p["components"] = json.RawMessage(m.Components.ValueString())
	}
	return p, diags
}

// createPayload adds the fields Discord only accepts when posting, and drops
// the empty ones a Components V2 message must not send.
func (m *messageModel) createPayload(ctx context.Context) (discord.Payload, diag.Diagnostics) {
	p, diags := m.fullPayload(ctx)
	if m.ComponentsV2.ValueBool() {
		delete(p, "content")
		delete(p, "embeds")
	}
	if m.Components.IsNull() {
		delete(p, "components")
	}
	if !m.StickerIDs.IsNull() {
		var ids []string
		diags.Append(m.StickerIDs.ElementsAs(ctx, &ids, false)...)
		p["sticker_ids"] = ids
	}
	if !m.Poll.IsNull() {
		poll, d := m.pollPayload(ctx)
		diags.Append(d...)
		p["poll"] = poll
	}
	return p, diags
}

func (m *messageModel) pollPayload(ctx context.Context) (map[string]any, diag.Diagnostics) {
	var poll pollModel
	diags := m.Poll.As(ctx, &poll, basetypes.ObjectAsOptions{})
	var answers []pollAnswerModel
	diags.Append(poll.Answers.ElementsAs(ctx, &answers, false)...)
	out := make([]map[string]any, 0, len(answers))
	for _, a := range answers {
		media := map[string]any{"text": a.Text.ValueString()}
		switch {
		case !a.EmojiID.IsNull():
			media["emoji"] = map[string]any{"id": a.EmojiID.ValueString()}
		case !a.EmojiName.IsNull():
			media["emoji"] = map[string]any{"name": a.EmojiName.ValueString()}
		}
		out = append(out, map[string]any{"poll_media": media})
	}
	return map[string]any{
		"question":          map[string]any{"text": poll.Question.ValueString()},
		"answers":           out,
		"duration":          poll.Duration.ValueInt64(),
		"allow_multiselect": poll.AllowMultiselect.ValueBool(),
	}, diags
}

func (m *messageModel) attachmentList(ctx context.Context) ([]attachmentModel, diag.Diagnostics) {
	var out []attachmentModel
	var diags diag.Diagnostics
	if !m.Attachments.IsNull() && !m.Attachments.IsUnknown() {
		diags.Append(m.Attachments.ElementsAs(ctx, &out, false)...)
	}
	return out, diags
}

// attachmentRequests returns the attachments array of a request and the
// files to upload with it. An attachment with a known ID is kept; one without
// is uploaded as files[n], where n is its placeholder ID in the array.
func (m *messageModel) attachmentRequests(ctx context.Context, upload bool) ([]map[string]any, []discord.File, diag.Diagnostics) {
	atts, diags := m.attachmentList(ctx)
	reqs := make([]map[string]any, 0, len(atts))
	var files []discord.File
	for _, a := range atts {
		req := map[string]any{"description": a.Description.ValueStringPointer(), "is_spoiler": a.Spoiler.ValueBool()}
		if !a.ID.IsUnknown() && !a.ID.IsNull() {
			req["id"] = a.ID.ValueString()
			reqs = append(reqs, req)
			continue
		}
		if !upload {
			continue
		}
		n := len(files)
		data, err := discord.FileContent(a.Source.ValueString(), a.ContentBase64.ValueString())
		if err != nil {
			diags.AddAttributeError(path.Root("attachments"), "Invalid attachment",
				fmt.Sprintf("Unable to read attachment %q: %s", a.Filename.ValueString(), err))
			continue
		}
		req["id"] = n
		req["filename"] = a.Filename.ValueString()
		reqs = append(reqs, req)
		files = append(files, discord.File{
			Field: "files[" + strconv.Itoa(n) + "]", Name: a.Filename.ValueString(),
			ContentType: attachmentContentType(a.Filename.ValueString(), data), Data: data,
		})
	}
	return reqs, files, diags
}

func attachmentContentType(filename string, data []byte) string {
	if t := mime.TypeByExtension(filepath.Ext(filename)); t != "" {
		return t
	}
	return http.DetectContentType(data)
}

// sameFile reports whether a planned attachment is the file an attachment in
// state was uploaded from. An attachment imported or added outside Terraform
// has no source in state and is matched by its file name.
func sameFile(plan, state attachmentModel) bool {
	if !plan.Filename.Equal(state.Filename) {
		return false
	}
	if state.Source.IsNull() && state.ContentBase64.IsNull() {
		return true
	}
	return plan.Source.Equal(state.Source) && plan.ContentBase64.Equal(state.ContentBase64) && plan.SourceHash.Equal(state.SourceHash)
}

// planAttachments carries the ID, size and media type of every planned
// attachment that keeps its file over from state, so only new files show as
// uploads.
func planAttachments(ctx context.Context, plan, state *messageModel) diag.Diagnostics {
	if plan.Attachments.IsNull() || plan.Attachments.IsUnknown() {
		return nil
	}
	planned, diags := plan.attachmentList(ctx)
	current, d := state.attachmentList(ctx)
	diags.Append(d...)
	used := make([]bool, len(current))
	for i := range planned {
		for j, c := range current {
			if !used[j] && sameFile(planned[i], c) {
				used[j] = true
				planned[i].ID, planned[i].Size, planned[i].ContentType = c.ID, c.Size, c.ContentType
				break
			}
		}
	}
	list, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: attachmentAttrTypes}, planned)
	diags.Append(d...)
	plan.Attachments = list
	return diags
}

// applyParts records the flags, components, stickers, poll and attachments
// Discord returned.
func (m *messageModel) applyParts(ctx context.Context, msg *discord.Message) diag.Diagnostics {
	var diags diag.Diagnostics
	m.SuppressEmbeds = types.BoolValue(msg.Flags&discord.MessageFlagSuppressEmbeds != 0)
	m.SuppressNotifications = types.BoolValue(msg.Flags&discord.MessageFlagSuppressNotifications != 0)
	m.ComponentsV2 = types.BoolValue(msg.Flags&discord.MessageFlagIsComponentsV2 != 0)
	m.Components = componentsValue(m.Components, msg.Components)

	m.StickerIDs = types.ListNull(types.StringType)
	if len(msg.StickerItems) > 0 {
		ids := make([]string, 0, len(msg.StickerItems))
		for _, s := range msg.StickerItems {
			ids = append(ids, s.ID)
		}
		m.StickerIDs = stringListValue(ctx, ids, &diags)
	}

	poll, d := m.pollValue(ctx, msg)
	diags.Append(d...)
	m.Poll = poll

	atts, d := m.attachmentsValue(ctx, msg.Attachments)
	diags.Append(d...)
	m.Attachments = atts
	return diags
}

func (m *messageModel) pollValue(ctx context.Context, msg *discord.Message) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics
	if msg.Poll == nil {
		return types.ObjectNull(pollAttrTypes), diags
	}
	var prior pollModel
	var priorAnswers []pollAnswerModel
	if !m.Poll.IsNull() && !m.Poll.IsUnknown() {
		diags.Append(m.Poll.As(ctx, &prior, basetypes.ObjectAsOptions{})...)
		if !prior.Answers.IsNull() && !prior.Answers.IsUnknown() {
			diags.Append(prior.Answers.ElementsAs(ctx, &priorAnswers, false)...)
		}
	}
	answers := make([]pollAnswerModel, 0, len(msg.Poll.Answers))
	for i, a := range msg.Poll.Answers {
		var p pollAnswerModel
		if i < len(priorAnswers) {
			p = priorAnswers[i]
		}
		am := pollAnswerModel{Text: keepText(p.Text, a.PollMedia.Text), EmojiID: types.StringNull(), EmojiName: types.StringNull()}
		// Discord returns the name of a custom emoji too; only its ID is set.
		if e := a.PollMedia.Emoji; e != nil {
			if e.ID != nil && *e.ID != "" {
				am.EmojiID = types.StringValue(*e.ID)
			} else if e.Name != nil {
				am.EmojiName = textValue(*e.Name)
			}
		}
		answers = append(answers, am)
	}
	list, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: pollAnswerAttrTypes}, answers)
	diags.Append(d...)
	// Ending the poll early moves its expiry, so a known duration is kept;
	// an imported poll's duration is derived from its expiry.
	duration := prior.Duration
	if duration.IsNull() || duration.IsUnknown() {
		duration = pollDuration(msg)
	}
	obj, d := types.ObjectValueFrom(ctx, pollAttrTypes, pollModel{
		Question:         keepText(prior.Question, msg.Poll.Question.Text),
		Answers:          list,
		Duration:         duration,
		AllowMultiselect: types.BoolValue(msg.Poll.AllowMultiselect),
	})
	diags.Append(d...)
	return obj, diags
}

// pollDuration is the number of hours between posting the message and the
// poll's expiry.
func pollDuration(msg *discord.Message) types.Int64 {
	if msg.Poll.Expiry == nil {
		return types.Int64Null()
	}
	posted, err1 := time.Parse(time.RFC3339Nano, msg.Timestamp)
	expiry, err2 := time.Parse(time.RFC3339Nano, *msg.Poll.Expiry)
	if err1 != nil || err2 != nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(math.Round(expiry.Sub(posted).Hours())))
}

// attachmentsValue records the returned attachments. Attachments in the
// prior value keep their position and their source arguments, matched by ID
// or, for files just uploaded, in upload order. Attachments added outside
// Terraform are appended.
func (m *messageModel) attachmentsValue(ctx context.Context, returned []discord.Attachment) (types.List, diag.Diagnostics) {
	elemType := types.ObjectType{AttrTypes: attachmentAttrTypes}
	prior, diags := m.attachmentList(ctx)
	claimed := make([]bool, len(returned))
	match := make([]int, len(prior))
	for i, p := range prior {
		match[i] = -1
		for j, a := range returned {
			if !claimed[j] && !p.ID.IsUnknown() && p.ID.ValueString() == a.ID {
				match[i], claimed[j] = j, true
				break
			}
		}
	}
	for i, p := range prior {
		if !p.ID.IsUnknown() {
			continue
		}
		for j := range returned {
			if !claimed[j] {
				match[i], claimed[j] = j, true
				break
			}
		}
	}
	out := make([]attachmentModel, 0, len(returned))
	for i, p := range prior {
		if match[i] >= 0 {
			out = append(out, attachmentValue(p, returned[match[i]]))
		}
	}
	for j, a := range returned {
		if !claimed[j] {
			out = append(out, attachmentValue(attachmentModel{
				Filename: types.StringValue(a.Filename), Source: types.StringNull(),
				ContentBase64: types.StringNull(), SourceHash: types.StringNull(),
			}, a))
		}
	}
	if len(out) == 0 {
		return types.ListNull(elemType), diags
	}
	list, d := types.ListValueFrom(ctx, elemType, out)
	diags.Append(d...)
	return list, diags
}

func attachmentValue(prior attachmentModel, a discord.Attachment) attachmentModel {
	prior.Description = textValue(a.Description)
	prior.Spoiler = types.BoolValue(a.Flags&discord.AttachmentFlagIsSpoiler != 0)
	prior.ID = types.StringValue(a.ID)
	prior.Size = types.Int64Value(a.Size)
	prior.ContentType = textValue(a.ContentType)
	return prior
}

// componentsValue keeps the configured components when Discord's copy only
// adds to them, such as generated IDs and resolved media, and otherwise
// records Discord's copy so the change shows as drift.
func componentsValue(prior types.String, returned []json.RawMessage) types.String {
	if len(returned) == 0 {
		return types.StringNull()
	}
	got, err := json.Marshal(returned)
	if err != nil {
		return types.StringNull()
	}
	if prior.IsNull() || prior.IsUnknown() {
		return types.StringValue(string(got))
	}
	var want, have any
	if json.Unmarshal([]byte(prior.ValueString()), &want) != nil || json.Unmarshal(got, &have) != nil {
		return types.StringValue(string(got))
	}
	if jsonSubset(want, have) {
		return prior
	}
	return types.StringValue(string(got))
}

// jsonSubset reports whether every value in want is present in have. An
// attachment:// reference matches the URL Discord resolves it to.
func jsonSubset(want, have any) bool {
	switch w := want.(type) {
	case map[string]any:
		h, ok := have.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range w {
			hv, ok := h[k]
			if !ok || !jsonSubset(v, hv) {
				return false
			}
		}
		return true
	case []any:
		h, ok := have.([]any)
		if !ok || len(h) != len(w) {
			return false
		}
		for i := range w {
			if !jsonSubset(w[i], h[i]) {
				return false
			}
		}
		return true
	case string:
		if strings.HasPrefix(w, "attachment://") {
			_, ok := have.(string)
			return ok
		}
		return w == have
	default:
		return reflect.DeepEqual(want, have)
	}
}

// parseComponents decodes a components JSON array into its top-level
// component objects.
func parseComponents(s string) ([]map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.UseNumber()
	var comps []map[string]any
	if err := dec.Decode(&comps); err != nil {
		return nil, fmt.Errorf("components must be a JSON array of component objects: %w", err)
	}
	for i, c := range comps {
		if _, ok := componentType(c); !ok {
			return nil, fmt.Errorf("component %d has no integer \"type\"", i)
		}
	}
	return comps, nil
}

func componentType(c map[string]any) (int64, bool) {
	n, ok := c["type"].(json.Number)
	if !ok {
		return 0, false
	}
	t, err := n.Int64()
	return t, err == nil
}

// countComponents counts components at every level, as Discord's limit of
// 40 per Components V2 message does.
func countComponents(v any) int {
	switch c := v.(type) {
	case []any:
		n := 0
		for _, e := range c {
			n += countComponents(e)
		}
		return n
	case map[string]any:
		n := 0
		if _, ok := c["type"]; ok {
			n = 1
		}
		return n + countComponents(c["components"]) + countComponents(c["accessory"])
	default:
		return 0
	}
}

// validateComponents checks the documented limits of components with and
// without the Components V2 flag.
func validateComponents(s string, v2 bool) error {
	comps, err := parseComponents(s)
	if err != nil {
		return err
	}
	if len(comps) == 0 {
		return errors.New("components must contain at least one component")
	}
	if v2 {
		total := 0
		for _, c := range comps {
			total += countComponents(c)
		}
		if total > maxComponentsV2 {
			return fmt.Errorf("a Components V2 message allows at most %d components in total, got %d", maxComponentsV2, total)
		}
		return nil
	}
	if len(comps) > maxLegacyActionRows {
		return fmt.Errorf("without components_v2 a message allows at most %d action rows, got %d", maxLegacyActionRows, len(comps))
	}
	for i, c := range comps {
		if t, _ := componentType(c); t != componentTypeActionRow {
			return fmt.Errorf("component %d has type %d; without components_v2 only action rows (type 1) can be top-level components", i, t)
		}
	}
	return nil
}

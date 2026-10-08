package provider

import (
	"context"
	"errors"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure   = &messageReactionResource{}
	_ resource.ResourceWithImportState = &messageReactionResource{}
	_ resource.ResourceWithIdentity    = &messageReactionResource{}
)

// reactionEmojiRegexp accepts a custom emoji as "name:id", or a unicode emoji:
// at least one non-ASCII character, with no colon, slash or whitespace, so
// the value stays one segment of the import ID.
var reactionEmojiRegexp = regexp.MustCompile(`^(?:[A-Za-z0-9_]{2,32}:[0-9]{1,20}|[^\x00-\x20:/]*[^\x00-\x7F][^\x00-\x20:/]*)$`)

// errCodeUnknownEmoji is Discord's error for an emoji that does not exist,
// such as a deleted custom emoji.
const errCodeUnknownEmoji = 10014

type messageReactionResource struct {
	resourceIdentity
	client *discord.Client
}

type messageReactionModel struct {
	ID        types.String `tfsdk:"id"`
	ChannelID types.String `tfsdk:"channel_id"`
	MessageID types.String `tfsdk:"message_id"`
	Emoji     types.String `tfsdk:"emoji"`
}

func newMessageReactionResource() resource.Resource {
	return &messageReactionResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		channelIdentity("channel_id"),
		{name: "message_id", description: "ID of the message.", state: []string{"message_id"}},
		{name: "emoji", description: "Unicode emoji, or `name:id` for a custom emoji.", state: []string{"emoji"}},
	}}}
}

func (r *messageReactionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_message_reaction"
}

func (r *messageReactionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Adds the bot's own reaction to a message, for example to seed a reaction-role message. " +
			"Reactions from other users are left alone. Requires the Read Message History permission, and Add " +
			"Reactions when nobody has reacted with the emoji yet. If the reaction is removed outside Terraform it is " +
			"added again on the next apply.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("`channel_id/message_id/emoji`."),
			"channel_id": schema.StringAttribute{
				MarkdownDescription: "ID of the channel the message is in.",
				Required:            true,
				Validators:          []validator.String{snowflakeValidator()},
				PlanModifiers:       replace,
			},
			"message_id": schema.StringAttribute{
				MarkdownDescription: "ID of the message to react to.",
				Required:            true,
				Validators:          []validator.String{snowflakeValidator()},
				PlanModifiers:       replace,
			},
			"emoji": schema.StringAttribute{
				MarkdownDescription: "Emoji to react with: a unicode emoji such as `\"👍\"`, or a custom emoji as " +
					"`name:id`, e.g. `\"${discord_emoji.party.name}:${discord_emoji.party.id}\"`.",
				Required: true,
				Validators: []validator.String{stringvalidator.RegexMatches(reactionEmojiRegexp,
					`must be a unicode emoji or a custom emoji as "name:id"`)},
				PlanModifiers: replace,
			},
		},
	}
}

func (r *messageReactionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *messageReactionModel) id() string {
	return m.ChannelID.ValueString() + "/" + m.MessageID.ValueString() + "/" + m.Emoji.ValueString()
}

func (r *messageReactionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan messageReactionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.AddOwnReaction(ctx, plan.ChannelID.ValueString(), plan.MessageID.ValueString(), plan.Emoji.ValueString()); err != nil {
		apiError(&resp.Diagnostics, "add reaction", err)
		return
	}
	plan.ID = types.StringValue(plan.id())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *messageReactionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state messageReactionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	found, err := r.hasOwnReaction(ctx, &state)
	if discord.IsNotFound(err) || isUnknownEmoji(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read reactions", err)
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(state.id())
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// hasOwnReaction pages through the users who reacted with the emoji until it
// finds the bot. Discord has no endpoint that checks one user's reaction.
func (r *messageReactionResource) hasOwnReaction(ctx context.Context, m *messageReactionModel) (bool, error) {
	me, err := r.client.GetCurrentUser(ctx)
	if err != nil {
		return false, err
	}
	after := ""
	for {
		users, err := r.client.ListReactions(ctx, m.ChannelID.ValueString(), m.MessageID.ValueString(), m.Emoji.ValueString(), after)
		if err != nil {
			return false, err
		}
		for _, u := range users {
			if u.ID == me.ID {
				return true, nil
			}
		}
		if len(users) < discord.MaxReactionsPage {
			return false, nil
		}
		after = users[len(users)-1].ID
	}
}

func isUnknownEmoji(err error) bool {
	var apiErr *discord.APIError
	return errors.As(err, &apiErr) && apiErr.Code == errCodeUnknownEmoji
}

func (r *messageReactionResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Unexpected update", "All discord_message_reaction attributes force replacement.")
}

func (r *messageReactionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state messageReactionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteOwnReaction(ctx, state.ChannelID.ValueString(), state.MessageID.ValueString(), state.Emoji.ValueString())
	if err != nil && !discord.IsNotFound(err) && !isUnknownEmoji(err) {
		apiError(&resp.Diagnostics, "remove reaction", err)
	}
}

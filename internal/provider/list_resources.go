package provider

import (
	"context"
	"fmt"
	"iter"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/list"
	listschema "github.com/hashicorp/terraform-plugin-framework/list/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// importableResource is a managed resource that embeds resourceIdentity.
type importableResource interface {
	resource.ResourceWithConfigure
	resource.ResourceWithImportState
	resource.ResourceWithIdentity
	identityNames() []string
}

// listItem is one listed object: the values of the resource's identity
// attributes, in order, with "" for null, and a name to show for it.
type listItem struct {
	identity []string
	name     string
}

// listResource lists the instances of a managed resource for terraform query.
// Each result carries the resource's identity and, when include_resource is
// set, the state that importing it produces: the managed resource's own
// ImportState and Read build it, so it always matches an import.
type listResource struct {
	res    importableResource
	client *discord.Client
	desc   string
	attrs  map[string]listschema.Attribute
	// list returns the objects to import, given the string config
	// attributes ("" when null) and the result limit (0 for none).
	list func(ctx context.Context, c *discord.Client, cfg map[string]string, limit int64) ([]listItem, error)
}

var _ list.ListResourceWithConfigure = &listResource{}

// newListResource panics when res does not embed resourceIdentity, which
// every test that starts the provider catches.
func newListResource(res resource.Resource, desc string, attrs map[string]listschema.Attribute,
	f func(ctx context.Context, c *discord.Client, cfg map[string]string, limit int64) ([]listItem, error),
) *listResource {
	r, ok := res.(importableResource)
	if !ok {
		panic(fmt.Sprintf("%T does not embed resourceIdentity", res))
	}
	return &listResource{res: r, desc: desc, attrs: attrs, list: f}
}

func (l *listResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	l.res.Metadata(ctx, req, resp)
}

func (l *listResource) ListResourceConfigSchema(_ context.Context, _ list.ListResourceSchemaRequest, resp *list.ListResourceSchemaResponse) {
	resp.Schema = listschema.Schema{MarkdownDescription: l.desc, Attributes: l.attrs}
}

func (l *listResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	l.res.Configure(ctx, req, resp)
	l.client, _ = req.ProviderData.(*discord.Client)
}

func (l *listResource) List(ctx context.Context, req list.ListRequest, stream *list.ListResultsStream) {
	stream.Results = l.results(ctx, req)
}

func (l *listResource) results(ctx context.Context, req list.ListRequest) iter.Seq[list.ListResult] {
	cfg := map[string]string{}
	for name := range l.attrs {
		var v types.String
		if diags := req.Config.GetAttribute(ctx, path.Root(name), &v); diags.HasError() {
			return list.ListResultsStreamDiagnostics(diags)
		}
		cfg[name] = v.ValueString()
	}
	items, err := l.list(ctx, l.client, cfg, req.Limit)
	if err != nil {
		var meta resource.MetadataResponse
		l.res.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "discord"}, &meta)
		var diags diag.Diagnostics
		apiError(&diags, "list "+meta.TypeName, err)
		return list.ListResultsStreamDiagnostics(diags)
	}
	names := l.res.identityNames()
	return func(push func(list.ListResult) bool) {
		var n int64
		for _, item := range items {
			if req.Limit > 0 && n >= req.Limit {
				return
			}
			result := req.NewListResult(ctx)
			result.DisplayName = item.name
			for i, v := range item.identity {
				value := types.StringValue(v)
				if v == "" {
					value = types.StringNull()
				}
				result.Diagnostics.Append(result.Identity.SetAttribute(ctx, path.Root(names[i]), value)...)
			}
			if req.IncludeResource && !result.Diagnostics.HasError() && !l.readResource(ctx, req, &result) {
				continue
			}
			n++
			if !push(result) {
				return
			}
		}
	}
}

// readResource sets the result's resource to the state of importing it, and
// returns false when the object was deleted after it was listed.
func (l *listResource) readResource(ctx context.Context, req list.ListRequest, result *list.ListResult) bool {
	imported := resource.ImportStateResponse{
		State:    tfsdk.State{Schema: req.ResourceSchema, Raw: tftypes.NewValue(req.ResourceSchema.Type().TerraformType(ctx), nil)},
		Identity: result.Identity,
	}
	l.res.ImportState(ctx, resource.ImportStateRequest{Identity: result.Identity}, &imported)
	result.Diagnostics.Append(imported.Diagnostics...)
	if result.Diagnostics.HasError() {
		return true
	}
	read := resource.ReadResponse{State: imported.State, Identity: result.Identity}
	l.res.Read(ctx, resource.ReadRequest{State: imported.State, Identity: result.Identity}, &read)
	result.Diagnostics.Append(read.Diagnostics...)
	if read.State.Raw.IsNull() && !result.Diagnostics.HasError() {
		return false
	}
	result.Resource = &tfsdk.Resource{Schema: read.State.Schema, Raw: read.State.Raw}
	return true
}

// Config attributes.

func listServerIDAttr(what string) map[string]listschema.Attribute {
	return map[string]listschema.Attribute{
		"server_id": listschema.StringAttribute{
			MarkdownDescription: "ID of the server (guild) to list " + what + " of.",
			Required:            true,
			Validators:          []validator.String{snowflakeValidator()},
		},
	}
}

// listChannelFilterAttrs adds an optional channel_id filter to server_id.
func listChannelFilterAttrs(what string) map[string]listschema.Attribute {
	attrs := listServerIDAttr(what)
	attrs["channel_id"] = listschema.StringAttribute{
		MarkdownDescription: "Only list the " + what + " of this channel.",
		Optional:            true,
		Validators:          []validator.String{snowflakeValidator()},
	}
	return attrs
}

// scopedItems builds list items identified by a scope, such as the server
// ID, and the object ID.
func scopedItems[T any](scope string, objs []T, keep func(T) bool, id, name func(T) string) []listItem {
	items := []listItem{}
	for _, o := range objs {
		if keep(o) {
			items = append(items, listItem{identity: []string{scope, id(o)}, name: name(o)})
		}
	}
	return items
}

func all[T any](T) bool { return true }

// Channels.

func channelListResource(res resource.Resource, channelType int, what string) list.ListResource {
	return newListResource(res,
		"Lists the "+what+" of a server. Starting November 16, 2026, Discord omits channels the bot cannot view.",
		listServerIDAttr(what),
		func(ctx context.Context, c *discord.Client, cfg map[string]string, _ int64) ([]listItem, error) {
			channels, err := c.ListChannels(ctx, cfg["server_id"])
			items := []listItem{}
			for _, ch := range channels {
				if ch.Type == channelType {
					items = append(items, listItem{identity: []string{ch.ID}, name: ch.Name})
				}
			}
			return items, err
		})
}

func newCategoryChannelListResource() list.ListResource {
	return channelListResource(newCategoryChannelResource(), discord.ChannelTypeCategory, "categories")
}

func newTextChannelListResource() list.ListResource {
	return channelListResource(newTextChannelResource(), discord.ChannelTypeText, "text channels")
}

func newAnnouncementChannelListResource() list.ListResource {
	return channelListResource(newAnnouncementChannelResource(), discord.ChannelTypeAnnouncement, "announcement channels")
}

func newVoiceChannelListResource() list.ListResource {
	return channelListResource(newVoiceChannelResource(), discord.ChannelTypeVoice, "voice channels")
}

func newStageChannelListResource() list.ListResource {
	return channelListResource(newStageChannelResource(), discord.ChannelTypeStage, "stage channels")
}

func newForumChannelListResource() list.ListResource {
	return channelListResource(newForumChannelResource(), discord.ChannelTypeForum, "forum channels")
}

func newMediaChannelListResource() list.ListResource {
	return channelListResource(newMediaChannelResource(), discord.ChannelTypeMedia, "media channels")
}

func newChannelPermissionListResource() list.ListResource {
	return newListResource(newChannelPermissionResource(),
		"Lists the permission overwrites of every channel in a server, or of one channel.",
		listChannelFilterAttrs("permission overwrites"),
		func(ctx context.Context, c *discord.Client, cfg map[string]string, _ int64) ([]listItem, error) {
			channels, err := c.ListChannels(ctx, cfg["server_id"])
			items := []listItem{}
			for _, ch := range channels {
				if cfg["channel_id"] != "" && ch.ID != cfg["channel_id"] {
					continue
				}
				for _, o := range ch.PermissionOverwrites {
					name := fmt.Sprintf("#%s %s %s", ch.Name, overwriteTypes[o.Type], o.ID)
					items = append(items, listItem{identity: []string{ch.ID, o.ID}, name: name})
				}
			}
			return items, err
		})
}

// Server objects.

func newRoleListResource() list.ListResource {
	return newListResource(newRoleResource(),
		"Lists the roles of a server, except `@everyone` (`discord_role_everyone`) and roles managed by an integration.",
		listServerIDAttr("roles"),
		func(ctx context.Context, c *discord.Client, cfg map[string]string, _ int64) ([]listItem, error) {
			roles, err := c.ListRoles(ctx, cfg["server_id"])
			return scopedItems(cfg["server_id"], roles,
				func(r discord.Role) bool { return r.ID != cfg["server_id"] && !r.Managed },
				func(r discord.Role) string { return r.ID },
				func(r discord.Role) string { return r.Name }), err
		})
}

func newMemberListResource() list.ListResource {
	return newListResource(newMemberResource(),
		"Lists the members of a server in ascending order of user ID. Requires the Server Members privileged intent.",
		listServerIDAttr("members"),
		func(ctx context.Context, c *discord.Client, cfg map[string]string, limit int64) ([]listItem, error) {
			members, err := c.ListMembers(ctx, cfg["server_id"], int(limit))
			return scopedItems(cfg["server_id"], members, all,
				func(m discord.Member) string { return m.User.ID },
				func(m discord.Member) string { return m.User.Username }), err
		})
}

func newBanListResource() list.ListResource {
	return newListResource(newBanResource(),
		"Lists the bans of a server in ascending order of user ID. Requires the Ban Members permission.",
		listServerIDAttr("bans"),
		func(ctx context.Context, c *discord.Client, cfg map[string]string, limit int64) ([]listItem, error) {
			bans, err := c.ListBans(ctx, cfg["server_id"], int(limit))
			return scopedItems(cfg["server_id"], bans, all,
				func(b discord.Ban) string { return b.User.ID },
				func(b discord.Ban) string { return b.User.Username }), err
		})
}

func newEmojiListResource() list.ListResource {
	return newListResource(newEmojiResource(),
		"Lists the custom emojis of a server, except those managed by an integration.",
		listServerIDAttr("emojis"),
		func(ctx context.Context, c *discord.Client, cfg map[string]string, _ int64) ([]listItem, error) {
			emojis, err := c.ListEmojis(ctx, cfg["server_id"])
			return scopedItems(cfg["server_id"], emojis,
				func(e discord.Emoji) bool { return !e.Managed },
				func(e discord.Emoji) string { return e.ID },
				func(e discord.Emoji) string { return e.Name }), err
		})
}

func newStickerListResource() list.ListResource {
	return newListResource(newStickerResource(),
		"Lists the custom stickers of a server.",
		listServerIDAttr("stickers"),
		func(ctx context.Context, c *discord.Client, cfg map[string]string, _ int64) ([]listItem, error) {
			stickers, err := c.ListStickers(ctx, cfg["server_id"])
			return scopedItems(cfg["server_id"], stickers, all,
				func(s discord.Sticker) string { return s.ID },
				func(s discord.Sticker) string { return s.Name }), err
		})
}

func newSoundboardSoundListResource() list.ListResource {
	return newListResource(newSoundboardSoundResource(),
		"Lists the soundboard sounds of a server.",
		listServerIDAttr("soundboard sounds"),
		func(ctx context.Context, c *discord.Client, cfg map[string]string, _ int64) ([]listItem, error) {
			sounds, err := c.ListSoundboardSounds(ctx, cfg["server_id"])
			return scopedItems(cfg["server_id"], sounds, all,
				func(s discord.SoundboardSound) string { return s.SoundID },
				func(s discord.SoundboardSound) string { return s.Name }), err
		})
}

func newAutoModerationRuleListResource() list.ListResource {
	return newListResource(newAutoModerationRuleResource(),
		"Lists the AutoMod rules of a server. Requires the Manage Server permission.",
		listServerIDAttr("AutoMod rules"),
		func(ctx context.Context, c *discord.Client, cfg map[string]string, _ int64) ([]listItem, error) {
			rules, err := c.ListAutoModerationRules(ctx, cfg["server_id"])
			return scopedItems(cfg["server_id"], rules, all,
				func(r discord.AutoModerationRule) string { return r.ID },
				func(r discord.AutoModerationRule) string { return r.Name }), err
		})
}

func newScheduledEventListResource() list.ListResource {
	return newListResource(newScheduledEventResource(),
		"Lists the scheduled events of a server that have not ended.",
		listServerIDAttr("scheduled events"),
		func(ctx context.Context, c *discord.Client, cfg map[string]string, _ int64) ([]listItem, error) {
			events, err := c.ListScheduledEvents(ctx, cfg["server_id"])
			return scopedItems(cfg["server_id"], events, all,
				func(e discord.ScheduledEvent) string { return e.ID },
				func(e discord.ScheduledEvent) string { return e.Name }), err
		})
}

func newServerTemplateListResource() list.ListResource {
	return newListResource(newServerTemplateResource(),
		"Lists the templates of a server. Requires the Manage Server permission.",
		listServerIDAttr("templates"),
		func(ctx context.Context, c *discord.Client, cfg map[string]string, _ int64) ([]listItem, error) {
			templates, err := c.ListGuildTemplates(ctx, cfg["server_id"])
			return scopedItems(cfg["server_id"], templates, all,
				func(t discord.GuildTemplate) string { return t.Code },
				func(t discord.GuildTemplate) string { return t.Name }), err
		})
}

// Channel objects.

// webhookTypeIncoming is the type of the webhooks discord_webhook manages.
const webhookTypeIncoming = 1

func webhookListResource(res resource.Resource, desc, what string, webhookType int) list.ListResource {
	return newListResource(res, desc, listChannelFilterAttrs(what),
		func(ctx context.Context, c *discord.Client, cfg map[string]string, _ int64) ([]listItem, error) {
			var webhooks []discord.Webhook
			var err error
			if cfg["channel_id"] == "" {
				webhooks, err = c.ListGuildWebhooks(ctx, cfg["server_id"])
			} else {
				webhooks, err = c.ListChannelWebhooks(ctx, cfg["channel_id"])
			}
			items := []listItem{}
			for _, w := range webhooks {
				if w.Type == webhookType {
					items = append(items, listItem{identity: []string{w.ID}, name: stringPtrValue(w.Name).ValueString()})
				}
			}
			return items, err
		})
}

func newWebhookListResource() list.ListResource {
	return webhookListResource(newWebhookResource(),
		"Lists the incoming webhooks of every channel in a server, or of one channel. Requires the Manage Webhooks "+
			"permission. Imported webhooks do not store their token or URL in state.",
		"webhooks", webhookTypeIncoming)
}

func newChannelFollowerListResource() list.ListResource {
	return webhookListResource(newChannelFollowerResource(),
		"Lists the channels of a server, or one channel, that follow an announcement channel. Requires the Manage "+
			"Webhooks permission.",
		"channel followers", webhookTypeChannelFollower)
}

func newInviteListResource() list.ListResource {
	return newListResource(newInviteResource(),
		"Lists the invites of every channel in a server, or of one channel. Requires the Manage Server permission, "+
			"or Manage Channels for one channel.",
		listChannelFilterAttrs("invites"),
		func(ctx context.Context, c *discord.Client, cfg map[string]string, _ int64) ([]listItem, error) {
			var invites []discord.Invite
			var err error
			if cfg["channel_id"] == "" {
				invites, err = c.ListGuildInvites(ctx, cfg["server_id"])
			} else {
				invites, err = c.ListChannelInvites(ctx, cfg["channel_id"])
			}
			items := []listItem{}
			for _, inv := range invites {
				if inv.Channel != nil {
					items = append(items, listItem{identity: []string{inv.Channel.ID, inv.Code}, name: inv.Code})
				}
			}
			return items, err
		})
}

func newThreadListResource() list.ListResource {
	return newListResource(newThreadResource(),
		"Lists the active (not archived) threads and forum posts of a server, or of one channel, newest first.",
		listChannelFilterAttrs("active threads"),
		func(ctx context.Context, c *discord.Client, cfg map[string]string, _ int64) ([]listItem, error) {
			threads, err := c.ListActiveThreads(ctx, cfg["server_id"])
			items := []listItem{}
			for _, t := range threads {
				if cfg["channel_id"] == "" || (t.ParentID != nil && *t.ParentID == cfg["channel_id"]) {
					items = append(items, listItem{identity: []string{t.ID}, name: t.Name})
				}
			}
			return items, err
		})
}

// Application objects.

func newApplicationCommandListResource() list.ListResource {
	return newListResource(newApplicationCommandResource(),
		"Lists the global commands of the bot's application, or its commands in one server.",
		map[string]listschema.Attribute{
			"server_id": listschema.StringAttribute{
				MarkdownDescription: "ID of the server (guild) to list the commands of. Omit to list global commands.",
				Optional:            true,
				Validators:          []validator.String{snowflakeValidator()},
			},
		},
		func(ctx context.Context, c *discord.Client, cfg map[string]string, _ int64) ([]listItem, error) {
			appID, err := c.ApplicationID(ctx)
			if err != nil {
				return nil, err
			}
			commands, err := c.ListApplicationCommands(ctx, appID, cfg["server_id"])
			items := []listItem{}
			for _, cmd := range commands {
				items = append(items, listItem{identity: []string{appID, cfg["server_id"], cmd.ID}, name: cmd.Name})
			}
			return items, err
		})
}

func newApplicationEmojiListResource() list.ListResource {
	return newListResource(newApplicationEmojiResource(),
		"Lists the emojis of the bot's application.",
		map[string]listschema.Attribute{},
		func(ctx context.Context, c *discord.Client, _ map[string]string, _ int64) ([]listItem, error) {
			appID, err := c.ApplicationID(ctx)
			if err != nil {
				return nil, err
			}
			emojis, err := c.ListApplicationEmojis(ctx, appID)
			return scopedItems(appID, emojis, all,
				func(e discord.Emoji) string { return e.ID },
				func(e discord.Emoji) string { return e.Name }), err
		})
}

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure      = &applicationCommandResource{}
	_ resource.ResourceWithImportState    = &applicationCommandResource{}
	_ resource.ResourceWithIdentity       = &applicationCommandResource{}
	_ resource.ResourceWithValidateConfig = &applicationCommandResource{}
)

var (
	commandTypes       = enumMapping{"", "chat_input", "user", "message"}
	commandOptionTypes = enumMapping{
		"", "sub_command", "sub_command_group", "string", "integer", "boolean",
		"user", "channel", "role", "mentionable", "number", "attachment",
	}
	commandContexts         = enumMapping{"guild", "bot_dm", "private_channel"}
	commandIntegrationTypes = enumMapping{"guild_install", "user_install"}
	commandChannelTypes     = enumMapping{
		0: "text", 1: "dm", 2: "voice", 3: "group_dm", 4: "category", 5: "announcement",
		10: "announcement_thread", 11: "public_thread", 12: "private_thread",
		13: "stage", 14: "directory", 15: "forum", 16: "media",
	}

	// commandLocales are the locales Discord accepts in localization maps.
	commandLocales = []string{
		"id", "da", "de", "en-GB", "en-US", "es-ES", "es-419", "fr", "hr", "it", "lt", "hu", "nl", "no", "pl",
		"pt-BR", "ro", "fi", "sv-SE", "vi", "tr", "cs", "el", "bg", "ru", "uk", "hi", "th", "zh-CN", "ja",
		"zh-TW", "ko",
	}

	// chatInputNameRegexp is Discord's pattern for slash command and option
	// names. Letters must also be lowercase where a lowercase form exists.
	chatInputNameRegexp = regexp.MustCompile(`^[-_\x{02BC}\p{L}\p{N}\p{Devanagari}\p{Thai}]{1,32}$`)
)

// maxCommandOptionDepth is how deep options nest: a subcommand group holds
// subcommands, which hold parameters.
const maxCommandOptionDepth = 3

// maxSafeInteger bounds integer option values, which Discord stores as
// doubles.
const maxSafeInteger = 1<<53 - 1

type applicationCommandResource struct {
	resourceIdentity
	client *discord.Client
}

type applicationCommandModel struct {
	ID                       types.String `tfsdk:"id"`
	ApplicationID            types.String `tfsdk:"application_id"`
	ServerID                 types.String `tfsdk:"server_id"`
	Type                     types.String `tfsdk:"type"`
	Name                     types.String `tfsdk:"name"`
	NameLocalizations        types.Map    `tfsdk:"name_localizations"`
	Description              types.String `tfsdk:"description"`
	DescriptionLocalizations types.Map    `tfsdk:"description_localizations"`
	DefaultMemberPermissions types.String `tfsdk:"default_member_permissions"`
	Contexts                 types.Set    `tfsdk:"contexts"`
	IntegrationTypes         types.Set    `tfsdk:"integration_types"`
	NSFW                     types.Bool   `tfsdk:"nsfw"`
	Options                  types.List   `tfsdk:"options"`
}

// commandOptionFields are the attributes of an option at every depth;
// commandOptionModel adds the nested options of the levels that have them.
type commandOptionFields struct {
	Type                     types.String  `tfsdk:"type"`
	Name                     types.String  `tfsdk:"name"`
	NameLocalizations        types.Map     `tfsdk:"name_localizations"`
	Description              types.String  `tfsdk:"description"`
	DescriptionLocalizations types.Map     `tfsdk:"description_localizations"`
	Required                 types.Bool    `tfsdk:"required"`
	Autocomplete             types.Bool    `tfsdk:"autocomplete"`
	Choices                  types.List    `tfsdk:"choices"`
	ChannelTypes             types.Set     `tfsdk:"channel_types"`
	MinValue                 types.Float64 `tfsdk:"min_value"`
	MaxValue                 types.Float64 `tfsdk:"max_value"`
	MinLength                types.Int64   `tfsdk:"min_length"`
	MaxLength                types.Int64   `tfsdk:"max_length"`
	FileTypes                types.Set     `tfsdk:"file_types"`
}

type commandOptionModel struct {
	commandOptionFields
	Options types.List `tfsdk:"options"`
}

type commandChoiceModel struct {
	Name              types.String `tfsdk:"name"`
	NameLocalizations types.Map    `tfsdk:"name_localizations"`
	Value             types.String `tfsdk:"value"`
}

var commandChoiceAttrTypes = map[string]attr.Type{
	"name":               types.StringType,
	"name_localizations": types.MapType{ElemType: types.StringType},
	"value":              types.StringType,
}

// commandOptionType is the object type of an option at depth (1 for the
// command's own options).
func commandOptionType(depth int) types.ObjectType {
	attrs := map[string]attr.Type{
		"type":                      types.StringType,
		"name":                      types.StringType,
		"name_localizations":        types.MapType{ElemType: types.StringType},
		"description":               types.StringType,
		"description_localizations": types.MapType{ElemType: types.StringType},
		"required":                  types.BoolType,
		"autocomplete":              types.BoolType,
		"choices":                   types.ListType{ElemType: types.ObjectType{AttrTypes: commandChoiceAttrTypes}},
		"channel_types":             types.SetType{ElemType: types.StringType},
		"min_value":                 types.Float64Type,
		"max_value":                 types.Float64Type,
		"min_length":                types.Int64Type,
		"max_length":                types.Int64Type,
		"file_types":                types.SetType{ElemType: types.StringType},
	}
	if depth < maxCommandOptionDepth {
		attrs["options"] = types.ListType{ElemType: commandOptionType(depth + 1)}
	}
	return types.ObjectType{AttrTypes: attrs}
}

func newApplicationCommandResource() resource.Resource {
	return &applicationCommandResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		{name: "application_id", description: "ID of the application.", state: []string{"application_id"}},
		{name: "server_id", description: "ID of the server (guild) of a server command; null for a global command.", state: []string{"server_id"}},
		{name: "command_id", description: "ID of the command.", state: []string{"id"}},
	}}}
}

func (r *applicationCommandResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_application_command"
}

func localizationsAttribute(field string, maxLength int) schema.MapAttribute {
	return schema.MapAttribute{
		MarkdownDescription: fmt.Sprintf("Translations of `%s` keyed by Discord locale, such as `de` or `pt-BR`. "+
			"Values follow the same rules as `%s`.", field, field),
		ElementType: types.StringType,
		Optional:    true,
		Validators: []validator.Map{
			mapvalidator.SizeAtLeast(1),
			mapvalidator.KeysAre(stringvalidator.OneOf(commandLocales...)),
			mapvalidator.ValueStringsAre(stringvalidator.UTF8LengthBetween(1, maxLength)),
		},
	}
}

// commandOptionAttributes returns the attributes of an option at depth.
func commandOptionAttributes(depth int) map[string]schema.Attribute {
	attrs := map[string]schema.Attribute{
		"type": schema.StringAttribute{
			MarkdownDescription: "Option type: " + commandOptionTypes.doc() + ". A `sub_command_group` holds " +
				"`sub_command` options, and a `sub_command` holds the other types.",
			Required:   true,
			Validators: []validator.String{commandOptionTypes.validator()},
		},
		"name": schema.StringAttribute{
			MarkdownDescription: "Option name (1-32 characters), unique among its siblings. Lowercase letters, " +
				"numbers, `-` and `_`.",
			Required: true,
		},
		"name_localizations": localizationsAttribute("name", 32),
		"description": schema.StringAttribute{
			MarkdownDescription: "Option description (1-100 characters).",
			Required:            true,
			Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 100)},
		},
		"description_localizations": localizationsAttribute("description", 100),
		"required": schema.BoolAttribute{
			MarkdownDescription: "Whether users must fill in the parameter. Required options must come before " +
				"optional ones. Not for subcommands or groups. Defaults to `false`.",
			Optional: true,
			Computed: true,
			Default:  booldefault.StaticBool(false),
		},
		"autocomplete": schema.BoolAttribute{
			MarkdownDescription: "Whether the application suggests values as the user types. For `string`, " +
				"`integer` and `number` options without `choices`. Defaults to `false`.",
			Optional: true,
			Computed: true,
			Default:  booldefault.StaticBool(false),
		},
		"choices": schema.ListNestedAttribute{
			MarkdownDescription: "The only values users can pick, in order (1 to 25). For `string`, `integer` and " +
				"`number` options.",
			Optional:   true,
			Validators: []validator.List{listvalidator.SizeBetween(1, 25)},
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"name": schema.StringAttribute{
					MarkdownDescription: "Name shown to users (1-100 characters).",
					Required:            true,
					Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 100)},
				},
				"name_localizations": localizationsAttribute("name", 100),
				"value": schema.StringAttribute{
					MarkdownDescription: "Value sent to the application: up to 100 characters for `string` options, " +
						"or the number written as a string, such as `\"10\"` or `\"2.5\"`, for `integer` and `number` " +
						"options.",
					Required: true,
				},
			}},
		},
		"channel_types": schema.SetAttribute{
			MarkdownDescription: "Channel types users can pick from, for `channel` options: " +
				commandChannelTypes.doc() + ". All types when omitted.",
			ElementType: types.StringType,
			Optional:    true,
			Validators: []validator.Set{
				setvalidator.SizeAtLeast(1),
				setvalidator.ValueStringsAre(commandChannelTypes.validator()),
			},
		},
		"min_value": schema.Float64Attribute{
			MarkdownDescription: "Smallest value allowed, for `integer` and `number` options.",
			Optional:            true,
		},
		"max_value": schema.Float64Attribute{
			MarkdownDescription: "Largest value allowed, for `integer` and `number` options.",
			Optional:            true,
		},
		"min_length": schema.Int64Attribute{
			MarkdownDescription: "Shortest length allowed (0-6000), for `string` options.",
			Optional:            true,
			Validators:          []validator.Int64{int64validator.Between(0, 6000)},
		},
		"max_length": schema.Int64Attribute{
			MarkdownDescription: "Longest length allowed (1-6000), for `string` options.",
			Optional:            true,
			Validators:          []validator.Int64{int64validator.Between(1, 6000)},
		},
		"file_types": schema.SetAttribute{
			MarkdownDescription: "File types users can attach, for `attachment` options (1 to 10): `image`, " +
				"`video`, `audio` or an extension such as `.pdf`. Discord only checks the file extension.",
			ElementType: types.StringType,
			Optional:    true,
			Validators: []validator.Set{
				setvalidator.SizeBetween(1, 10),
				setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1)),
			},
		},
	}
	if depth < maxCommandOptionDepth {
		attrs["options"] = schema.ListNestedAttribute{
			MarkdownDescription: "Options of a `sub_command_group` (its subcommands) or of a `sub_command` (its " +
				"parameters), in order (1 to 25).",
			Optional:     true,
			Validators:   []validator.List{listvalidator.SizeBetween(1, 25)},
			NestedObject: schema.NestedAttributeObject{Attributes: commandOptionAttributes(depth + 1)},
		}
	}
	return attrs
}

func (r *applicationCommandResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.Set{setplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an application command: a slash command (`chat_input`), or a `user` or `message` " +
			"command shown in the context menu. Commands are global, or scoped to one server with `server_id`; " +
			"server commands update immediately and suit testing.\n\n" +
			"Each command is managed on its own, so commands created elsewhere are left alone. Discord treats creating " +
			"a command as an upsert: creating one with the type and name of an existing command in the same scope " +
			"overwrites that command and brings it under Terraform.\n\n" +
			"Discord allows 200 command creations per day per server. Command permissions for users, roles and " +
			"channels need a user's OAuth2 token, so they are not managed here; use `default_member_permissions`.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("Command ID."),
			"application_id": schema.StringAttribute{
				MarkdownDescription: "ID of the application that owns the command. Defaults to the bot's application.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{snowflakeValidator()},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"server_id": schema.StringAttribute{
				MarkdownDescription: "ID of the server (guild) for a server command. Omit for a global command.",
				Optional:            true,
				Validators:          []validator.String{snowflakeValidator()},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "Command type: " + commandTypes.doc() + ". Defaults to `chat_input`. Changing " +
					"it replaces the command.",
				Optional:      true,
				Computed:      true,
				Default:       stringdefault.StaticString("chat_input"),
				Validators:    []validator.String{commandTypes.validator()},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Command name (1-32 characters), unique per type within its scope. `chat_input` " +
					"names use lowercase letters, numbers, `-` and `_`; `user` and `message` names may use any case " +
					"and spaces.",
				Required:   true,
				Validators: []validator.String{stringvalidator.UTF8LengthBetween(1, 32)},
			},
			"name_localizations": localizationsAttribute("name", 32),
			"description": schema.StringAttribute{
				MarkdownDescription: "Description (1-100 characters). Required for `chat_input` commands; not " +
					"allowed for `user` and `message` commands.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.UTF8LengthBetween(1, 100)},
			},
			"description_localizations": localizationsAttribute("description", 100),
			"default_member_permissions": schema.StringAttribute{
				MarkdownDescription: "Permissions a member needs to use the command by default, as a decimal bitfield " +
					"such as from `provider::discord::permissions()`. `\"0\"` limits it to administrators. Everyone can " +
					"use it when omitted. Server admins can override this in the server's integration settings.",
				Optional:   true,
				Validators: []validator.String{permissionsValidator()},
			},
			"contexts": schema.SetAttribute{
				MarkdownDescription: "Where the command can be used: " + commandContexts.doc() + ". " +
					"`private_channel` only applies to user-installed commands. Global commands only. Discord's " +
					"default applies when omitted, and removing the argument leaves the current value.",
				ElementType:   types.StringType,
				Optional:      true,
				Computed:      true,
				PlanModifiers: keep,
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(commandContexts.validator()),
				},
			},
			"integration_types": schema.SetAttribute{
				MarkdownDescription: "Installation contexts the command is available in: " +
					commandIntegrationTypes.doc() + ". Each must be enabled on the application. Global commands only. " +
					"Defaults to the application's installation contexts, and removing the argument leaves the " +
					"current value.",
				ElementType:   types.StringType,
				Optional:      true,
				Computed:      true,
				PlanModifiers: keep,
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(commandIntegrationTypes.validator()),
				},
			},
			"nsfw": schema.BoolAttribute{
				MarkdownDescription: "Whether the command is age-restricted. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"options": schema.ListNestedAttribute{
				MarkdownDescription: "Parameters, subcommands and subcommand groups of a `chat_input` command, in " +
					"order (1 to 25). Groups hold subcommands and subcommands hold parameters; no other nesting is " +
					"allowed.",
				Optional:     true,
				Validators:   []validator.List{listvalidator.SizeBetween(1, 25)},
				NestedObject: schema.NestedAttributeObject{Attributes: commandOptionAttributes(1)},
			},
		},
	}
}

func (r *applicationCommandResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

// IdentitySchema overrides resourceIdentity's to make server_id optional,
// since global commands have none.
func (r *applicationCommandResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	attrs := make(map[string]identityschema.Attribute, len(r.attrs))
	for _, a := range r.attrs {
		attrs[a.name] = identityschema.StringAttribute{
			Description:       a.description,
			RequiredForImport: a.name != "server_id",
			OptionalForImport: a.name == "server_id",
		}
	}
	resp.IdentitySchema = identityschema.Schema{Attributes: attrs}
}

// ImportState accepts "<application_id>/<command_id>" for a global command
// and "<application_id>/<server_id>/<command_id>" for a server command, or
// the identity with server_id null for a global command.
func (r *applicationCommandResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var appID, serverID, commandID types.String
	if req.ID != "" {
		parts := strings.Split(req.ID, "/")
		if (len(parts) != 2 && len(parts) != 3) || strings.Contains("/"+req.ID+"/", "//") {
			resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("expected import ID in the form "+
				"\"application_id/command_id\" or \"application_id/server_id/command_id\", got %q", req.ID))
			return
		}
		appID, commandID = types.StringValue(parts[0]), types.StringValue(parts[len(parts)-1])
		serverID = types.StringNull()
		if len(parts) == 3 {
			serverID = types.StringValue(parts[1])
		}
	} else {
		resp.Diagnostics.Append(req.Identity.GetAttribute(ctx, path.Root("application_id"), &appID)...)
		resp.Diagnostics.Append(req.Identity.GetAttribute(ctx, path.Root("server_id"), &serverID)...)
		resp.Diagnostics.Append(req.Identity.GetAttribute(ctx, path.Root("command_id"), &commandID)...)
		if resp.Diagnostics.HasError() {
			return
		}
		for name, v := range map[string]types.String{"application_id": appID, "command_id": commandID} {
			if v.ValueString() == "" {
				resp.Diagnostics.AddAttributeError(path.Root(name), "Invalid import identity",
					fmt.Sprintf("Identity attribute %q must not be empty.", name))
			}
		}
		if serverID.ValueString() == "" {
			serverID = types.StringNull()
		}
		if resp.Diagnostics.HasError() {
			return
		}
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("application_id"), appID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), serverID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), commandID)...)
}

// ValidateConfig checks the rules Discord documents across attributes:
// which arguments each command and option type takes, how options nest and
// how names are written.
func (r *applicationCommandResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m applicationCommandModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	diags := &resp.Diagnostics
	if !m.ServerID.IsNull() {
		for name, v := range map[string]types.Set{"contexts": m.Contexts, "integration_types": m.IntegrationTypes} {
			if !v.IsNull() {
				diags.AddAttributeError(path.Root(name), "Invalid attribute for a server command",
					name+" only applies to global commands; remove it or server_id.")
			}
		}
	}
	if m.Type.IsUnknown() {
		return
	}
	if m.Type.IsNull() || m.Type.ValueString() == "chat_input" {
		validateChatInputName(path.Root("name"), m.Name, m.NameLocalizations, diags)
		if m.Description.IsNull() {
			diags.AddAttributeError(path.Root("description"), "Missing description",
				"chat_input commands require a description.")
		}
		validateCommandOptions(ctx, path.Root("options"), m.Options, 1, "", diags)
		return
	}
	for name, null := range map[string]bool{
		"description":               m.Description.IsNull(),
		"description_localizations": m.DescriptionLocalizations.IsNull(),
		"options":                   m.Options.IsNull(),
	} {
		if !null {
			diags.AddAttributeError(path.Root(name), "Invalid attribute for the command type",
				name+" only applies to chat_input commands.")
		}
	}
}

// validateChatInputName checks a slash command or option name and its
// translations against Discord's naming rules.
func validateChatInputName(p path.Path, name types.String, localizations types.Map, diags *diag.Diagnostics) {
	check := func(p path.Path, v types.String) {
		if v.IsNull() || v.IsUnknown() {
			return
		}
		s := v.ValueString()
		if !chatInputNameRegexp.MatchString(s) || strings.ToLower(s) != s {
			diags.AddAttributeError(p, "Invalid command name",
				fmt.Sprintf("%q must be 1-32 lowercase letters, numbers, - or _.", s))
		}
	}
	check(p, name)
	if localizations.IsNull() || localizations.IsUnknown() {
		return
	}
	localizedPath := p.ParentPath().AtName("name_localizations")
	for locale, v := range localizations.Elements() {
		if s, ok := v.(types.String); ok {
			check(localizedPath.AtMapKey(locale), s)
		}
	}
}

// knownObjects returns the known elements of a list of objects, or nil when
// the list itself is not known.
func knownObjects(list types.List) []types.Object {
	if list.IsNull() || list.IsUnknown() {
		return nil
	}
	var out []types.Object
	for _, e := range list.Elements() {
		if o, ok := e.(types.Object); ok && !o.IsNull() && !o.IsUnknown() {
			out = append(out, o)
		}
	}
	return out
}

// decodeOption reads an option object at depth, whose nested options are
// null at the deepest level.
func decodeOption(ctx context.Context, o types.Object, depth int, diags *diag.Diagnostics) commandOptionModel {
	var m commandOptionModel
	if depth < maxCommandOptionDepth {
		diags.Append(o.As(ctx, &m, basetypes.ObjectAsOptions{})...)
		return m
	}
	diags.Append(o.As(ctx, &m.commandOptionFields, basetypes.ObjectAsOptions{})...)
	m.Options = types.ListNull(commandOptionType(maxCommandOptionDepth))
	return m
}

func isSubcommand(t string) bool {
	return t == "sub_command" || t == "sub_command_group"
}

// validateCommandOptions checks the options at depth whose parent has type
// parent ("" for the command itself).
func validateCommandOptions(ctx context.Context, p path.Path, list types.List, depth int, parent string, diags *diag.Diagnostics) {
	names := map[string]bool{}
	optional := false
	for i, obj := range knownObjects(list) {
		op := p.AtListIndex(i)
		o := decodeOption(ctx, obj, depth, diags)
		if diags.HasError() {
			return
		}
		validateChatInputName(op.AtName("name"), o.Name, o.NameLocalizations, diags)
		if !o.Name.IsUnknown() {
			if names[o.Name.ValueString()] {
				diags.AddAttributeError(op.AtName("name"), "Duplicate option name",
					fmt.Sprintf("Option names must be unique among siblings; %q is repeated.", o.Name.ValueString()))
			}
			names[o.Name.ValueString()] = true
		}
		if o.Type.IsUnknown() {
			continue
		}
		t := o.Type.ValueString()
		switch {
		case parent == "sub_command_group" && t != "sub_command":
			diags.AddAttributeError(op.AtName("type"), "Invalid option nesting",
				"A sub_command_group may only contain sub_command options.")
		case parent == "sub_command" && isSubcommand(t):
			diags.AddAttributeError(op.AtName("type"), "Invalid option nesting",
				"A sub_command may only contain parameters, not subcommands or groups.")
		}
		if !o.Required.IsUnknown() {
			if o.Required.ValueBool() && optional {
				diags.AddAttributeError(op.AtName("required"), "Required option after an optional one",
					"Required options must be listed before optional options.")
			}
			optional = optional || !o.Required.ValueBool()
		}
		validateOptionArguments(ctx, op, t, o, diags)
		if isSubcommand(t) {
			validateCommandOptions(ctx, op.AtName("options"), o.Options, depth+1, t, diags)
		}
	}
}

// validateOptionArguments checks that an option of type t only sets the
// arguments its type takes, and that its values are valid for the type.
func validateOptionArguments(ctx context.Context, op path.Path, t string, o commandOptionModel, diags *diag.Diagnostics) {
	numeric := t == "integer" || t == "number"
	allowed := map[string]bool{
		"options":       isSubcommand(t),
		"choices":       numeric || t == "string",
		"autocomplete":  numeric || t == "string",
		"channel_types": t == "channel",
		"min_value":     numeric,
		"max_value":     numeric,
		"min_length":    t == "string",
		"max_length":    t == "string",
		"file_types":    t == "attachment",
		"required":      !isSubcommand(t),
	}
	set := map[string]bool{
		"options":       !o.Options.IsNull(),
		"choices":       !o.Choices.IsNull(),
		"autocomplete":  o.Autocomplete.ValueBool(),
		"channel_types": !o.ChannelTypes.IsNull(),
		"min_value":     !o.MinValue.IsNull(),
		"max_value":     !o.MaxValue.IsNull(),
		"min_length":    !o.MinLength.IsNull(),
		"max_length":    !o.MaxLength.IsNull(),
		"file_types":    !o.FileTypes.IsNull(),
		"required":      o.Required.ValueBool(),
	}
	for name, isSet := range set {
		if isSet && !allowed[name] {
			diags.AddAttributeError(op.AtName(name), "Invalid argument for the option type",
				fmt.Sprintf("%s does not apply to %s options.", name, t))
		}
	}
	if set["choices"] && o.Autocomplete.ValueBool() {
		diags.AddAttributeError(op.AtName("autocomplete"), "Conflicting arguments",
			"autocomplete cannot be enabled on an option with choices.")
	}
	if t == "integer" {
		for name, v := range map[string]types.Float64{"min_value": o.MinValue, "max_value": o.MaxValue} {
			if f := v.ValueFloat64(); !v.IsNull() && !v.IsUnknown() && (f != math.Trunc(f) || math.Abs(f) > maxSafeInteger) {
				diags.AddAttributeError(op.AtName(name), "Invalid integer bound",
					name+" of an integer option must be a whole number between -(2^53-1) and 2^53-1.")
			}
		}
	}
	if lo, hi := o.MinValue, o.MaxValue; !lo.IsNull() && !lo.IsUnknown() && !hi.IsNull() && !hi.IsUnknown() &&
		lo.ValueFloat64() > hi.ValueFloat64() {
		diags.AddAttributeError(op.AtName("min_value"), "Invalid bounds", "min_value must not exceed max_value.")
	}
	if lo, hi := o.MinLength, o.MaxLength; !lo.IsNull() && !lo.IsUnknown() && !hi.IsNull() && !hi.IsUnknown() &&
		lo.ValueInt64() > hi.ValueInt64() {
		diags.AddAttributeError(op.AtName("min_length"), "Invalid bounds", "min_length must not exceed max_length.")
	}
	if !set["choices"] || !allowed["choices"] {
		return
	}
	for i, obj := range knownObjects(o.Choices) {
		var c commandChoiceModel
		diags.Append(obj.As(ctx, &c, basetypes.ObjectAsOptions{})...)
		if c.Value.IsNull() || c.Value.IsUnknown() {
			continue
		}
		if _, err := choiceValue(t, c.Value.ValueString()); err != nil {
			diags.AddAttributeError(op.AtName("choices").AtListIndex(i).AtName("value"), "Invalid choice value", err.Error())
		}
	}
}

// choiceValue encodes a choice value for an option of type t. Numbers must
// be written as Discord returns them, so that reading them back matches.
func choiceValue(t, s string) (json.RawMessage, error) {
	switch t {
	case "integer":
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != s || n > maxSafeInteger || n < -maxSafeInteger {
			return nil, fmt.Errorf("%q must be a whole number between -(2^53-1) and 2^53-1, written without "+
				"leading zeros or a plus sign", s)
		}
		return json.RawMessage(s), nil
	case "number":
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(f) || math.Abs(f) > maxSafeInteger+1 || formatNumber(f) != s {
			return nil, fmt.Errorf("%q must be a number between -2^53 and 2^53 in its shortest decimal form, "+
				"such as \"2.5\" or \"10\"", s)
		}
		return json.RawMessage(s), nil
	default:
		if utf8.RuneCountInString(s) > 100 {
			return nil, fmt.Errorf("%q is longer than 100 characters", s)
		}
		return json.Marshal(s)
	}
}

func formatNumber(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// choiceString converts a choice value from Discord back to its string form.
func choiceString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return formatNumber(f)
	}
	return string(raw)
}

func stringMap(ctx context.Context, m types.Map, diags *diag.Diagnostics) map[string]string {
	if m.IsNull() || m.IsUnknown() {
		return nil
	}
	out := map[string]string{}
	diags.Append(m.ElementsAs(ctx, &out, false)...)
	return out
}

func stringMapValue(ctx context.Context, m map[string]string, diags *diag.Diagnostics) types.Map {
	if len(m) == 0 {
		return types.MapNull(types.StringType)
	}
	v, d := types.MapValueFrom(ctx, types.StringType, m)
	diags.Append(d...)
	return v
}

// optionsPayload converts the options at depth to their API form.
func optionsPayload(ctx context.Context, list types.List, depth int, diags *diag.Diagnostics) []discord.ApplicationCommandOption {
	objs := knownObjects(list)
	out := make([]discord.ApplicationCommandOption, 0, len(objs))
	for _, obj := range objs {
		m := decodeOption(ctx, obj, depth, diags)
		t, _ := commandOptionTypes.value(m.Type.ValueString())
		o := discord.ApplicationCommandOption{
			Type:                     t,
			Name:                     m.Name.ValueString(),
			NameLocalizations:        stringMap(ctx, m.NameLocalizations, diags),
			Description:              m.Description.ValueString(),
			DescriptionLocalizations: stringMap(ctx, m.DescriptionLocalizations, diags),
			Required:                 m.Required.ValueBool(),
			Autocomplete:             m.Autocomplete.ValueBool(),
			MinValue:                 m.MinValue.ValueFloat64Pointer(),
			MaxValue:                 m.MaxValue.ValueFloat64Pointer(),
			MinLength:                m.MinLength.ValueInt64Pointer(),
			MaxLength:                m.MaxLength.ValueInt64Pointer(),
			Options:                  optionsPayload(ctx, m.Options, depth+1, diags),
		}
		if !m.ChannelTypes.IsNull() {
			o.ChannelTypes = enumValues(ctx, commandChannelTypes, m.ChannelTypes, diags)
		}
		if !m.FileTypes.IsNull() {
			o.FileTypes = setStrings(ctx, m.FileTypes, diags)
		}
		for _, cobj := range knownObjects(m.Choices) {
			var c commandChoiceModel
			diags.Append(cobj.As(ctx, &c, basetypes.ObjectAsOptions{})...)
			value, err := choiceValue(m.Type.ValueString(), c.Value.ValueString())
			if err != nil {
				diags.AddError("Invalid choice value", err.Error())
				continue
			}
			o.Choices = append(o.Choices, discord.ApplicationCommandOptionChoice{
				Name:              c.Name.ValueString(),
				NameLocalizations: stringMap(ctx, c.NameLocalizations, diags),
				Value:             value,
			})
		}
		out = append(out, o)
	}
	return out
}

// optionsValue converts options read from Discord at depth, null when there
// are none.
func optionsValue(ctx context.Context, opts []discord.ApplicationCommandOption, depth int, diags *diag.Diagnostics) types.List {
	objType := commandOptionType(depth)
	if len(opts) == 0 {
		return types.ListNull(objType)
	}
	values := make([]attr.Value, 0, len(opts))
	for _, o := range opts {
		choices := types.ListNull(types.ObjectType{AttrTypes: commandChoiceAttrTypes})
		if len(o.Choices) > 0 {
			models := make([]commandChoiceModel, len(o.Choices))
			for i, c := range o.Choices {
				models[i] = commandChoiceModel{
					Name:              types.StringValue(c.Name),
					NameLocalizations: stringMapValue(ctx, c.NameLocalizations, diags),
					Value:             types.StringValue(choiceString(c.Value)),
				}
			}
			v, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: commandChoiceAttrTypes}, models)
			diags.Append(d...)
			choices = v
		}
		fileTypes := types.SetNull(types.StringType)
		if len(o.FileTypes) > 0 {
			fileTypes = stringSetValue(ctx, o.FileTypes, diags)
		}
		fields := commandOptionFields{
			Type:                     commandOptionTypes.name(o.Type),
			Name:                     types.StringValue(o.Name),
			NameLocalizations:        stringMapValue(ctx, o.NameLocalizations, diags),
			Description:              types.StringValue(o.Description),
			DescriptionLocalizations: stringMapValue(ctx, o.DescriptionLocalizations, diags),
			Required:                 types.BoolValue(o.Required),
			Autocomplete:             types.BoolValue(o.Autocomplete),
			Choices:                  choices,
			ChannelTypes:             enumSetValue(ctx, commandChannelTypes, o.ChannelTypes, diags),
			MinValue:                 types.Float64PointerValue(o.MinValue),
			MaxValue:                 types.Float64PointerValue(o.MaxValue),
			MinLength:                types.Int64PointerValue(o.MinLength),
			MaxLength:                types.Int64PointerValue(o.MaxLength),
			FileTypes:                fileTypes,
		}
		var v types.Object
		var d diag.Diagnostics
		if depth < maxCommandOptionDepth {
			v, d = types.ObjectValueFrom(ctx, objType.AttrTypes, commandOptionModel{
				commandOptionFields: fields,
				Options:             optionsValue(ctx, o.Options, depth+1, diags),
			})
		} else {
			v, d = types.ObjectValueFrom(ctx, objType.AttrTypes, fields)
		}
		diags.Append(d...)
		values = append(values, v)
	}
	list, d := types.ListValue(objType, values)
	diags.Append(d...)
	return list
}

// payload is the full request body for the model. Options are sent as an
// empty list when unset so that an update removes them.
func (m *applicationCommandModel) payload(ctx context.Context, diags *diag.Diagnostics) discord.Payload {
	p := discord.Payload{
		"name":                       m.Name.ValueString(),
		"name_localizations":         stringMap(ctx, m.NameLocalizations, diags),
		"default_member_permissions": m.DefaultMemberPermissions.ValueStringPointer(),
	}
	putBool(p, "nsfw", m.NSFW)
	if m.Type.ValueString() == "chat_input" {
		p["description"] = m.Description.ValueString()
		p["description_localizations"] = stringMap(ctx, m.DescriptionLocalizations, diags)
		p["options"] = optionsPayload(ctx, m.Options, 1, diags)
	}
	if m.ServerID.IsNull() {
		if !m.Contexts.IsNull() && !m.Contexts.IsUnknown() {
			p["contexts"] = enumValues(ctx, commandContexts, m.Contexts, diags)
		}
		if !m.IntegrationTypes.IsNull() && !m.IntegrationTypes.IsUnknown() {
			p["integration_types"] = enumValues(ctx, commandIntegrationTypes, m.IntegrationTypes, diags)
		}
	}
	return p
}

func (m *applicationCommandModel) apply(ctx context.Context, c *discord.ApplicationCommand, diags *diag.Diagnostics) {
	m.ID = types.StringValue(c.ID)
	m.ApplicationID = types.StringValue(c.ApplicationID)
	m.ServerID = stringPtrValue(&c.GuildID)
	m.Type = commandTypes.name(c.Type)
	m.Name = types.StringValue(c.Name)
	m.NameLocalizations = stringMapValue(ctx, c.NameLocalizations, diags)
	m.Description = stringPtrValue(&c.Description)
	m.DescriptionLocalizations = stringMapValue(ctx, c.DescriptionLocalizations, diags)
	m.DefaultMemberPermissions = stringPtrValue(c.DefaultMemberPermissions)
	m.NSFW = types.BoolValue(c.NSFW)
	m.Options = optionsValue(ctx, c.Options, 1, diags)
	// Contexts and installation types only apply to global commands.
	m.Contexts = types.SetNull(types.StringType)
	m.IntegrationTypes = types.SetNull(types.StringType)
	if c.GuildID == "" {
		m.Contexts = enumSetValue(ctx, commandContexts, c.Contexts, diags)
		m.IntegrationTypes = enumSetValue(ctx, commandIntegrationTypes, c.IntegrationTypes, diags)
	}
}

func (r *applicationCommandResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan applicationCommandModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	appID := plan.ApplicationID.ValueString()
	if plan.ApplicationID.IsUnknown() || plan.ApplicationID.IsNull() {
		id, err := r.client.ApplicationID(ctx)
		if err != nil {
			apiError(&resp.Diagnostics, "read the bot's application", err)
			return
		}
		appID = id
	}
	p := plan.payload(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	commandTypes.put(p, "type", plan.Type)
	c, err := r.client.CreateApplicationCommand(ctx, appID, plan.ServerID.ValueString(), p)
	if err != nil {
		apiError(&resp.Diagnostics, "create application command", err)
		return
	}
	plan.apply(ctx, c, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *applicationCommandResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state applicationCommandModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.client.GetApplicationCommand(ctx, state.ApplicationID.ValueString(), state.ServerID.ValueString(), state.ID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read application command", err)
		return
	}
	state.apply(ctx, c, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *applicationCommandResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan, state applicationCommandModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := diffPayload(plan.payload(ctx, &resp.Diagnostics), state.payload(ctx, &resp.Diagnostics))
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.client.EditApplicationCommand(ctx, state.ApplicationID.ValueString(), state.ServerID.ValueString(), state.ID.ValueString(), p)
	if err != nil {
		apiError(&resp.Diagnostics, "update application command", err)
		return
	}
	plan.apply(ctx, c, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *applicationCommandResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state applicationCommandModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteApplicationCommand(ctx, state.ApplicationID.ValueString(), state.ServerID.ValueString(), state.ID.ValueString())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete application command", err)
	}
}

package apispec

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// modelSchemas maps each struct in internal/discord/models.go to the spec
// schemas it is decoded from or encoded as. Every JSON field of the struct
// must exist, with a compatible type, in every schema listed.
var modelSchemas = map[string]struct {
	typ     reflect.Type
	schemas []string
}{
	"Guild":           {reflect.TypeFor[discord.Guild](), []string{"GuildWithCountsResponse", "GuildResponse"}},
	"RoleColors":      {reflect.TypeFor[discord.RoleColors](), []string{"GuildRoleColorsResponse", "RoleColors"}},
	"Role":            {reflect.TypeFor[discord.Role](), []string{"GuildRoleResponse"}},
	"Overwrite":       {reflect.TypeFor[discord.Overwrite](), []string{"ChannelPermissionOverwriteResponse"}},
	"ForumTag":        {reflect.TypeFor[discord.ForumTag](), []string{"ForumTagResponse", "UpdateThreadTagRequest"}},
	"DefaultReaction": {reflect.TypeFor[discord.DefaultReaction](), []string{"DefaultReactionEmojiResponse", "UpdateDefaultReactionEmojiRequest"}},
	"Channel":         {reflect.TypeFor[discord.Channel](), []string{"GuildChannelResponse"}},
	"ThreadMetadata":  {reflect.TypeFor[discord.ThreadMetadata](), []string{"ThreadMetadataResponse"}},
	"Thread":          {reflect.TypeFor[discord.Thread](), []string{"ThreadResponse", "CreatedThreadResponse"}},
	"PositionUpdate":  {reflect.TypeFor[discord.PositionUpdate](), []string{"UpdateRolePositionsRequest"}},
	"User":            {reflect.TypeFor[discord.User](), []string{"UserResponse"}},
	"Member":          {reflect.TypeFor[discord.Member](), []string{"GuildMemberResponse"}},
	"Ban":             {reflect.TypeFor[discord.Ban](), []string{"GuildBanResponse"}},
	"Webhook":         {reflect.TypeFor[discord.Webhook](), []string{"GuildIncomingWebhookResponse"}},
	"InviteChannel":   {reflect.TypeFor[discord.InviteChannel](), []string{"InviteChannelResponse"}},
	"Invite":          {reflect.TypeFor[discord.Invite](), []string{"GuildInviteResponse"}},
	"EmbedFooter":     {reflect.TypeFor[discord.EmbedFooter](), []string{"MessageEmbedFooterResponse", "RichEmbedFooter"}},
	"EmbedMedia":      {reflect.TypeFor[discord.EmbedMedia](), []string{"MessageEmbedImageResponse", "RichEmbedImage", "RichEmbedThumbnail"}},
	"EmbedAuthor":     {reflect.TypeFor[discord.EmbedAuthor](), []string{"MessageEmbedAuthorResponse", "RichEmbedAuthor"}},
	"EmbedField":      {reflect.TypeFor[discord.EmbedField](), []string{"MessageEmbedFieldResponse", "RichEmbedField"}},
	"Embed":           {reflect.TypeFor[discord.Embed](), []string{"MessageEmbedResponse", "RichEmbed"}},
	"Message":         {reflect.TypeFor[discord.Message](), []string{"MessageResponse"}},
	"Emoji":           {reflect.TypeFor[discord.Emoji](), []string{"EmojiResponse"}},
	"EntityMetadata":  {reflect.TypeFor[discord.EntityMetadata](), []string{"EntityMetadataExternalResponse", "EntityMetadataExternal"}},
	"NWeekday":        {reflect.TypeFor[discord.NWeekday](), []string{"ByNWeekdayResponse", "ByNWeekday"}},
	"RecurrenceRule":  {reflect.TypeFor[discord.RecurrenceRule](), []string{"RecurrenceRuleResponse", "RecurrenceRule"}},
	"ScheduledEvent": {reflect.TypeFor[discord.ScheduledEvent](), []string{
		"ExternalScheduledEventResponse", "StageScheduledEventResponse", "VoiceScheduledEventResponse",
	}},
	"StageInstance":  {reflect.TypeFor[discord.StageInstance](), []string{"StageInstanceResponse"}},
	"WidgetSettings": {reflect.TypeFor[discord.WidgetSettings](), []string{"WidgetSettingsResponse"}},
	"WelcomeScreen":  {reflect.TypeFor[discord.WelcomeScreen](), []string{"GuildWelcomeScreenResponse"}},
	"WelcomeScreenChannel": {reflect.TypeFor[discord.WelcomeScreenChannel](), []string{
		"GuildWelcomeScreenChannelResponse", "GuildWelcomeChannel"}},
	"Onboarding":             {reflect.TypeFor[discord.Onboarding](), []string{"GuildOnboardingResponse", "UserGuildOnboardingResponse"}},
	"OnboardingPrompt":       {reflect.TypeFor[discord.OnboardingPrompt](), []string{"OnboardingPromptResponse"}},
	"OnboardingPromptOption": {reflect.TypeFor[discord.OnboardingPromptOption](), []string{"OnboardingPromptOptionResponse"}},
	"PromptEmoji":            {reflect.TypeFor[discord.PromptEmoji](), []string{"SettingsEmojiResponse"}},
	"Integration": {reflect.TypeFor[discord.Integration](), []string{
		"DiscordIntegrationResponse", "ExternalConnectionIntegrationResponse", "GuildSubscriptionIntegrationResponse",
	}},
	"IntegrationAccount": {reflect.TypeFor[discord.IntegrationAccount](), []string{"AccountResponse"}},
	"GuildTemplate":      {reflect.TypeFor[discord.GuildTemplate](), []string{"GuildTemplateResponse"}},
}

// TestModelsTableIsComplete fails when a struct is added to models.go
// without an entry in modelSchemas.
func TestModelsTableIsComplete(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "../discord/models.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var structs []string
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok {
				if _, ok := ts.Type.(*ast.StructType); ok {
					structs = append(structs, ts.Name.Name)
				}
			}
		}
	}
	slices.Sort(structs)
	if want := slices.Sorted(maps.Keys(modelSchemas)); !slices.Equal(structs, want) {
		t.Errorf("models.go structs %v do not match modelSchemas %v", structs, want)
	}
}

// TestModelsMatchSpec validates the client models against the spec schemas,
// so a field Discord renames or retypes fails here instead of decoding to a
// zero value.
func TestModelsMatchSpec(t *testing.T) {
	s := pinnedSpec(t)
	for name, m := range modelSchemas {
		for _, schemaName := range m.schemas {
			sc, ok := s.Schema(schemaName)
			if !ok {
				t.Errorf("discord.%s: schema %s is not in the spec", name, schemaName)
				continue
			}
			for _, err := range structMismatches(s, m.typ, sc) {
				t.Errorf("discord.%s against %s: %s", name, schemaName, err)
			}
		}
	}
}

func structMismatches(s *Spec, typ reflect.Type, sc *Schema) []string {
	props := s.Properties(sc)
	var errs []string
	for f := range typ.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !f.IsExported() || name == "-" || name == "" {
			continue
		}
		p, ok := props[name]
		if !ok {
			errs = append(errs, "field "+name+" is not in the schema")
			continue
		}
		if err := typeMismatch(s, f.Type, p); err != "" {
			errs = append(errs, "field "+name+": "+err)
		}
	}
	return errs
}

var rawMessage = reflect.TypeFor[json.RawMessage]()

// typeMismatch compares a Go type with the JSON types a schema accepts.
// Nullability is not compared: a nil pointer and a zero value both decode
// from null.
func typeMismatch(s *Spec, typ reflect.Type, sc *Schema) string {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == rawMessage {
		return ""
	}
	var want string
	switch typ.Kind() {
	case reflect.String:
		want = "string"
	case reflect.Bool:
		want = "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		want = "integer"
	case reflect.Float32, reflect.Float64:
		want = "number"
	case reflect.Slice, reflect.Array:
		want = "array"
	case reflect.Struct, reflect.Map:
		want = "object"
	default:
		return "unsupported Go type " + typ.String()
	}
	got := s.Types(sc)
	if !slices.Contains(got, want) && (want != "number" || !slices.Contains(got, "integer")) {
		return "Go type " + typ.String() + " is " + want + ", spec allows " + strings.Join(got, ", ")
	}
	if want == "array" {
		items := s.Items(sc)
		if items == nil {
			return "spec array has no items schema"
		}
		if err := typeMismatch(s, typ.Elem(), items); err != "" {
			return "items: " + err
		}
	}
	return ""
}

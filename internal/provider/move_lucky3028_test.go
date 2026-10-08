package provider

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// The fixtures in testdata/lucky3028 are the state Lucky3028/discord (commit
// 3710abd, its last) stores for each of its resources, generated with its own
// schemas through the SDKv2's state shims.
const (
	luckyServer  = "1180000000000000001"
	luckyChannel = "1180000000000000002"
	luckyRole    = "1180000000000000003"
	luckyRole2   = "1180000000000000004"
	luckyRole3   = "1180000000000000005"
	luckyUser    = "1180000000000000006"
	luckyApp     = "1180000000000000011"
	luckyCateg   = "1180000000000000012"
)

func luckyFixture(t *testing.T, sourceType string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "lucky3028", sourceType+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// moveState calls MoveResourceState on the provider server, as Terraform
// does for a moved block, and returns the target state's non-null attributes
// and identity as Go values.
func moveState(t *testing.T, req *tfprotov6.MoveResourceStateRequest) (state, identity map[string]any, diags []*tfprotov6.Diagnostic) {
	t.Helper()
	ctx := context.Background()
	server, err := providerserver.NewProtocol6WithError(New("test")())()
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	identities, err := server.GetResourceIdentitySchemas(ctx, &tfprotov6.GetResourceIdentitySchemasRequest{})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.MoveResourceState(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.TargetState != nil {
		v, err := resp.TargetState.Unmarshal(schemas.ResourceSchemas[req.TargetTypeName].ValueType())
		if err != nil {
			t.Fatal(err)
		}
		state = nonNullAttributes(t, v)
	}
	if resp.TargetIdentity != nil {
		v, err := resp.TargetIdentity.IdentityData.Unmarshal(identities.IdentitySchemas[req.TargetTypeName].ValueType())
		if err != nil {
			t.Fatal(err)
		}
		identity = nonNullAttributes(t, v)
	}
	return state, identity, resp.Diagnostics
}

func nonNullAttributes(t *testing.T, v tftypes.Value) map[string]any {
	t.Helper()
	all, _ := goValue(t, v).(map[string]any)
	for k, a := range all {
		if a == nil {
			delete(all, k)
		}
	}
	return all
}

// goValue converts a value to nil, string, int64, bool, []any (sets sorted)
// or map[string]any.
func goValue(t *testing.T, v tftypes.Value) any {
	t.Helper()
	if !v.IsKnown() {
		t.Fatalf("unknown value %s", v)
	}
	if v.IsNull() {
		return nil
	}
	typ := v.Type()
	switch {
	case typ.Is(tftypes.String):
		var s string
		_ = v.As(&s)
		return s
	case typ.Is(tftypes.Number):
		var f big.Float
		_ = v.As(&f)
		i, _ := f.Int64()
		return i
	case typ.Is(tftypes.Bool):
		var b bool
		_ = v.As(&b)
		return b
	case typ.Is(tftypes.List{}), typ.Is(tftypes.Set{}):
		var elems []tftypes.Value
		_ = v.As(&elems)
		out := make([]any, 0, len(elems))
		for _, e := range elems {
			out = append(out, goValue(t, e))
		}
		if typ.Is(tftypes.Set{}) {
			slices.SortFunc(out, func(a, b any) int { return strings.Compare(fmt.Sprint(a), fmt.Sprint(b)) })
		}
		return out
	default:
		var attrs map[string]tftypes.Value
		if err := v.As(&attrs); err != nil {
			t.Fatal(err)
		}
		out := make(map[string]any, len(attrs))
		for k, a := range attrs {
			out[k] = goValue(t, a)
		}
		return out
	}
}

func luckyMoveRequest(t *testing.T, source, target string) *tfprotov6.MoveResourceStateRequest {
	t.Helper()
	return &tfprotov6.MoveResourceStateRequest{
		SourceProviderAddress: lucky3028Address,
		SourceTypeName:        source,
		SourceState:           &tfprotov6.RawState{JSON: luckyFixture(t, source)},
		TargetTypeName:        target,
	}
}

func TestMoveStateFromLucky3028(t *testing.T) {
	channelState := func(extra map[string]any) map[string]any {
		m := map[string]any{
			"id": luckyChannel, "server_id": luckyServer, "name": "general", "position": int64(3), "category_id": luckyCateg,
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	channelIdentity := map[string]any{"channel_id": luckyChannel}
	serverIdentity := map[string]any{"server_id": luckyServer}
	cases := []struct {
		source, target  string
		state, identity map[string]any
	}{
		{
			source: "discord_text_channel", target: "discord_text_channel",
			state:    channelState(map[string]any{"topic": "Chat", "nsfw": true, "rate_limit_per_user": int64(10)}),
			identity: channelIdentity,
		},
		{
			source: "discord_news_channel", target: "discord_announcement_channel",
			state:    channelState(map[string]any{"topic": "Releases", "nsfw": false}),
			identity: channelIdentity,
		},
		{
			source: "discord_forum_channel", target: "discord_forum_channel",
			state:    channelState(map[string]any{"nsfw": false, "rate_limit_per_user": int64(30)}),
			identity: channelIdentity,
		},
		{
			source: "discord_voice_channel", target: "discord_voice_channel",
			state:    channelState(map[string]any{"bitrate": int64(96000), "user_limit": int64(12)}),
			identity: channelIdentity,
		},
		{
			source: "discord_category_channel", target: "discord_category_channel",
			state:    map[string]any{"id": luckyCateg, "server_id": luckyServer, "name": "Projects", "position": int64(2)},
			identity: map[string]any{"channel_id": luckyCateg},
		},
		{
			source: "discord_channel_permission", target: "discord_channel_permission",
			state: map[string]any{
				"id": luckyChannel + "/" + luckyUser, "channel_id": luckyChannel, "overwrite_id": luckyUser,
				"type": "member", "allow": "1024", "deny": "2251799813685248",
			},
			identity: map[string]any{"channel_id": luckyChannel, "overwrite_id": luckyUser},
		},
		{
			source: "discord_role", target: "discord_role",
			state: map[string]any{
				"id": luckyRole, "server_id": luckyServer, "name": "Moderators", "permissions": "1099511627775",
				"color": int64(3447003), "hoist": true, "mentionable": false, "position": int64(5), "managed": false,
			},
			identity: map[string]any{"server_id": luckyServer, "role_id": luckyRole},
		},
		{
			source: "discord_role_everyone", target: "discord_role_everyone",
			state:    map[string]any{"id": luckyServer, "server_id": luckyServer, "permissions": "104324673"},
			identity: serverIdentity,
		},
		{
			source: "discord_role_positions", target: "discord_role_positions",
			state: map[string]any{
				"id": luckyServer, "server_id": luckyServer, "role_ids": []any{luckyRole2, luckyRole3, luckyRole},
			},
		},
		{
			source: "discord_member_roles", target: "discord_member_roles",
			state: map[string]any{
				"id": luckyServer + "/" + luckyUser, "server_id": luckyServer, "user_id": luckyUser,
				"role_ids": []any{luckyRole, luckyRole3},
			},
			identity: map[string]any{"server_id": luckyServer, "user_id": luckyUser},
		},
		{
			source: "discord_message", target: "discord_message",
			state: map[string]any{
				"id": "1180000000000000007", "channel_id": luckyChannel, "content": "Welcome!", "pinned": true,
				"author_id": luckyUser,
				"attachments": []any{map[string]any{
					"filename": "rules.pdf", "source": "files/rules.pdf", "content_base64": nil, "source_hash": nil,
					"description": nil, "spoiler": nil, "id": "1180000000000000015", "size": int64(2048),
					"content_type": "application/pdf",
				}},
			},
			identity: map[string]any{"channel_id": luckyChannel, "message_id": "1180000000000000007"},
		},
		{
			source: "discord_webhook", target: "discord_webhook",
			state: map[string]any{
				"id": "1180000000000000008", "channel_id": luckyChannel, "name": "Deploys",
				"avatar": "data:image/png;base64,iVBORw0KGgo=", "avatar_hash": "a1b2c3", "store_secrets": true,
				"token": "secret-token", "url": "https://discord.com/api/webhooks/1180000000000000008/secret-token",
			},
			identity: map[string]any{"webhook_id": "1180000000000000008"},
		},
		{
			source: "discord_invite", target: "discord_invite",
			state: map[string]any{
				"id": "abcDEF12", "code": "abcDEF12", "channel_id": luckyChannel, "max_age": int64(0),
				"max_uses": int64(5), "temporary": false, "unique": false,
			},
			identity: map[string]any{"channel_id": luckyChannel, "code": "abcDEF12"},
		},
		{
			source: "discord_guild_sticker", target: "discord_sticker",
			state: map[string]any{
				"id": "1180000000000000009", "server_id": luckyServer, "name": "wave", "description": "Waving hand",
				"tags": "wave", "format_type": "png",
			},
			identity: map[string]any{"server_id": luckyServer, "sticker_id": "1180000000000000009"},
		},
		{
			source: "discord_server", target: "discord_server_settings",
			state: map[string]any{
				"id": luckyServer, "server_id": luckyServer, "name": "My Server", "verification_level": "medium",
				"default_message_notifications": "only_mentions", "explicit_content_filter": "members_without_roles",
				"afk_channel_id": luckyChannel, "afk_timeout": int64(900), "icon": "data:image/png;base64,iVBORw0KGgo=",
				"icon_hash": "i1c0n", "splash_hash": "5p1a5h", "owner_id": "1180000000000000016",
			},
			identity: serverIdentity,
		},
		{
			source: "discord_managed_server", target: "discord_server_settings",
			state: map[string]any{
				"id": luckyServer, "server_id": luckyServer, "name": "My Server", "description": "A place to chat",
				"verification_level": "low", "default_message_notifications": "all_messages",
				"explicit_content_filter": "all_members", "afk_timeout": int64(300), "owner_id": "1180000000000000016",
			},
			identity: serverIdentity,
		},
		{
			source: "discord_system_channel", target: "discord_server_settings",
			state: map[string]any{
				"id": luckyServer, "server_id": luckyServer, "system_channel_id": luckyChannel,
				"system_channel_flags": int64(5),
			},
			identity: serverIdentity,
		},
		{
			source: "discord_server_onboarding", target: "discord_onboarding",
			state: map[string]any{
				"id": luckyServer, "server_id": luckyServer, "enabled": true, "mode": "advanced",
				"default_channel_ids": []any{luckyChannel, luckyCateg},
			},
			identity: serverIdentity,
		},
		{
			source: "discord_server_widget", target: "discord_server_widget",
			state:    map[string]any{"id": luckyServer, "server_id": luckyServer, "enabled": true, "channel_id": luckyChannel},
			identity: serverIdentity,
		},
		{
			source: "discord_auto_moderation_rule", target: "discord_auto_moderation_rule",
			state: map[string]any{
				"id": "1180000000000000010", "server_id": luckyServer, "name": "Block invites", "event_type": "message_send",
				"trigger_type": "keyword", "enabled": true, "exempt_role_ids": []any{luckyRole}, "creator_id": luckyUser,
			},
			identity: map[string]any{"server_id": luckyServer, "rule_id": "1180000000000000010"},
		},
		{
			source: "discord_role_connection_metadata", target: "discord_application_role_connection_metadata",
			state: map[string]any{
				"id": luckyApp, "application_id": luckyApp, "records": []any{
					map[string]any{
						"type": "datetime_greater_than_or_equal", "key": "joined_at", "name": "Joined",
						"name_localizations": nil, "description": "Days since joining", "description_localizations": nil,
					},
					map[string]any{
						"type": "integer_greater_than_or_equal", "key": "level", "name": "Level",
						"name_localizations": nil, "description": "Minimum level", "description_localizations": nil,
					},
				},
			},
			identity: map[string]any{"application_id": luckyApp},
		},
	}

	// Every Lucky3028/discord resource has a fixture and a case.
	fixtures, err := filepath.Glob(filepath.Join("testdata", "lucky3028", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != len(cases) {
		t.Errorf("%d fixtures but %d cases", len(fixtures), len(cases))
	}

	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			state, identity, diags := moveState(t, luckyMoveRequest(t, tc.source, tc.target))
			if len(diags) > 0 {
				t.Fatalf("diagnostics: %s: %s", diags[0].Summary, diags[0].Detail)
			}
			if !reflect.DeepEqual(state, tc.state) {
				t.Errorf("state:\n got  %v\n want %v", state, tc.state)
			}
			if !reflect.DeepEqual(identity, tc.identity) {
				t.Errorf("identity:\n got  %v\n want %v", identity, tc.identity)
			}
		})
	}
}

func TestMoveStateFromLucky3028Errors(t *testing.T) {
	cases := []struct {
		name   string
		req    *tfprotov6.MoveResourceStateRequest
		detail string
	}{
		{
			name: "other provider",
			req: &tfprotov6.MoveResourceStateRequest{
				SourceProviderAddress: "registry.terraform.io/aequasi/discord",
				SourceTypeName:        "discord_role",
				SourceState:           &tfprotov6.RawState{JSON: luckyFixture(t, "discord_role")},
				TargetTypeName:        "discord_role",
			},
			detail: "does not include support for the given source resource",
		},
		{
			name:   "other resource type",
			req:    luckyMoveRequest(t, "discord_text_channel", "discord_announcement_channel"),
			detail: "does not include support for the given source resource",
		},
		{
			name:   "no move support",
			req:    luckyMoveRequest(t, "discord_text_channel", "discord_media_channel"),
			detail: "does not include support for the given source resource",
		},
		{
			name: "missing ID",
			req: &tfprotov6.MoveResourceStateRequest{
				SourceProviderAddress: lucky3028Address,
				SourceTypeName:        "discord_role",
				SourceState:           &tfprotov6.RawState{JSON: []byte(`{"id":"","server_id":"1","name":"x"}`)},
				TargetTypeName:        "discord_role",
			},
			detail: `The state has no "id" attribute`,
		},
		{
			name: "invalid JSON",
			req: &tfprotov6.MoveResourceStateRequest{
				SourceProviderAddress: lucky3028Address,
				SourceTypeName:        "discord_role",
				SourceState:           &tfprotov6.RawState{JSON: []byte(`{"id":`)},
				TargetTypeName:        "discord_role",
			},
			detail: "could not be read",
		},
		{
			name: "flatmap state",
			req: &tfprotov6.MoveResourceStateRequest{
				SourceProviderAddress: lucky3028Address,
				SourceTypeName:        "discord_role",
				SourceState:           &tfprotov6.RawState{Flatmap: map[string]string{"id": "1"}},
				TargetTypeName:        "discord_role",
			},
			detail: "Terraform 0.11 or earlier",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, _, diags := moveState(t, tc.req)
			if state != nil {
				t.Errorf("state = %v, want none", state)
			}
			if len(diags) != 1 || diags[0].Severity != tfprotov6.DiagnosticSeverityError || !strings.Contains(diags[0].Detail, tc.detail) {
				t.Fatalf("diagnostics = %+v, want one error containing %q", diags, tc.detail)
			}
		})
	}
}

// The source provider address is compared case-insensitively, as Terraform
// normalizes it.
func TestMoveStateFromLucky3028AddressCase(t *testing.T) {
	req := luckyMoveRequest(t, "discord_role_everyone", "discord_role_everyone")
	req.SourceProviderAddress = "registry.terraform.io/Lucky3028/discord"
	state, _, diags := moveState(t, req)
	if len(diags) > 0 || state["server_id"] != luckyServer {
		t.Fatalf("state = %v, diagnostics = %+v", state, diags)
	}
}

package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

func TestComponentsValue(t *testing.T) {
	returned := []json.RawMessage{json.RawMessage(
		`{"type":17,"id":1,"components":[{"type":13,"id":2,"file":{"url":"https://cdn.discordapp.com/a/guide.pdf?ex=1","proxy_url":"x"}}]}`)}
	tests := []struct {
		name, prior string
		keep        bool
	}{
		{"fields Discord adds", `[{"type":17,"components":[{"type":13,"file":{"url":"attachment://guide.pdf"}}]}]`, true},
		{"changed value", `[{"type":17,"components":[{"type":13,"file":{"url":"https://example.com/guide.pdf"}}]}]`, false},
		{"missing component", `[{"type":17,"components":[]}]`, false},
		{"type changed", `[{"type":1,"components":[{"type":13,"file":{"url":"attachment://guide.pdf"}}]}]`, false},
		{"object replaced by value", `[{"type":17,"components":[{"type":13,"file":"guide.pdf"}]}]`, false},
		{"attachment reference to an object", `[{"type":17,"components":[{"type":13,"file":"attachment://guide.pdf"}]}]`, false},
		{"invalid prior", `not json`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := componentsValue(types.StringValue(tt.prior), returned)
			if kept := got.ValueString() == tt.prior; kept != tt.keep {
				t.Errorf("kept prior = %v, want %v (got %s)", kept, tt.keep, got.ValueString())
			}
		})
	}
	if got := componentsValue(types.StringValue(`[]`), nil); !got.IsNull() {
		t.Errorf("no components returned: got %s, want null", got)
	}
	if got := componentsValue(types.StringNull(), returned); !strings.Contains(got.ValueString(), `"id":1`) {
		t.Errorf("imported components = %s, want Discord's copy", got)
	}
}

func TestValidateComponents(t *testing.T) {
	tests := []struct {
		name, components string
		v2               bool
		err              string
	}{
		{"action rows", `[{"type":1,"components":[{"type":2}]}]`, false, ""},
		{"empty", `[]`, false, "at least one component"},
		{"not an array", `{"type":1}`, false, "JSON array"},
		{"type not an integer", `[{"type":"1"}]`, false, `no integer "type"`},
		{"type fraction", `[{"type":1.5}]`, false, `no integer "type"`},
		{"V2 at the limit", `[{"type":17,"components":[` + strings.Repeat(`{"type":10},`, 37) + `{"type":9,"accessory":{"type":11}}]}]`, true, ""},
		{"V2 over the limit", `[{"type":17,"components":[` + strings.Repeat(`{"type":10},`, 38) + `{"type":9,"accessory":{"type":11}}]}]`, true, "got 41"},
		{"V2 layout without the flag", `[{"type":10}]`, false, "only action rows"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateComponents(tt.components, tt.v2)
			switch {
			case tt.err == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)):
				t.Errorf("error = %v, want %q", err, tt.err)
			}
		})
	}
}

func TestPollDuration(t *testing.T) {
	expiry := "2025-01-03T00:30:00Z"
	bad := "soon"
	tests := []struct {
		name, posted string
		expiry       *string
		want         types.Int64
	}{
		{"rounded hours", "2025-01-01T00:00:00Z", &expiry, types.Int64Value(49)},
		{"no expiry", "2025-01-01T00:00:00Z", nil, types.Int64Null()},
		{"invalid expiry", "2025-01-01T00:00:00Z", &bad, types.Int64Null()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &discord.Message{Timestamp: tt.posted, Poll: &discord.Poll{Expiry: tt.expiry}}
			if got := pollDuration(msg); !got.Equal(tt.want) {
				t.Errorf("pollDuration = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestReferencedAttachments(t *testing.T) {
	const cdn = "https://cdn.discordapp.com/attachments/"
	msg := &discord.Message{
		ChannelID: "10",
		Embeds: []discord.Embed{
			{Image: &discord.EmbedMedia{URL: cdn + "10/21/banner.png?ex=1"}},
			{Thumbnail: &discord.EmbedMedia{URL: cdn + "10/21/banner.png"}},
			{Footer: &discord.EmbedFooter{Text: "x", IconURL: cdn + "99/22/other-channel.png"}},
			{Author: &discord.EmbedAuthor{Name: "x", IconURL: "https://example.com/attachments/10/23/a.png/extra"}},
			{Image: &discord.EmbedMedia{URL: "%zz"}},
		},
		Components: []json.RawMessage{
			json.RawMessage(`{"type":17,"components":[{"type":13,"name":"guide.pdf","size":42,"file":{"url":"` + cdn +
				`10/24/guide.pdf","attachment_id":"24","content_type":"application/pdf"}}]}`),
			json.RawMessage(`{"type":12,"items":[{"media":{"url":"` + cdn + `10/25/shot.png","attachment_id":"25"}},` +
				`{"media":{"url":"https://example.com/linked.png"}}]}`),
			json.RawMessage(`not json`),
		},
	}
	want := []discord.Attachment{
		{ID: "21", Filename: "banner.png"},
		{ID: "24", Filename: "guide.pdf", Size: 42, ContentType: "application/pdf"},
		{ID: "25", Filename: "shot.png"},
	}
	got := referencedAttachments(msg)
	if len(got) != len(want) {
		t.Fatalf("referencedAttachments = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("attachment %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

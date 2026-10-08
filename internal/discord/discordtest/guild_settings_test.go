package discordtest

import (
	"errors"
	"testing"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

func onboardingPrompt(id string, options ...discord.Payload) discord.Payload {
	return discord.Payload{"id": id, "title": "Prompt " + id, "options": options}
}

func apiErrorCode(err error) int {
	var apiErr *discord.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return 0
}

func TestModifyOnboardingRequirements(t *testing.T) {
	s := NewServer()
	defer s.Close()
	c := discord.NewClient(s.URL, Token, "test")
	ctx := t.Context()
	channels := []string{"1", "2", "3", "4", "5", "6", "7"}

	_, err := c.ModifyOnboarding(ctx, GuildID, discord.Payload{"enabled": true, "default_channel_ids": channels[:6]})
	if code := apiErrorCode(err); code != 350000 {
		t.Fatalf("enabling with 6 channels: code %d (%v), want 350000", code, err)
	}
	if _, err := c.ModifyOnboarding(ctx, GuildID, discord.Payload{"enabled": true, "default_channel_ids": channels}); err != nil {
		t.Fatal(err)
	}
	_, err = c.ModifyOnboarding(ctx, GuildID, discord.Payload{"default_channel_ids": channels[:6]})
	if code := apiErrorCode(err); code != 350001 {
		t.Fatalf("dropping below 7 channels while enabled: code %d (%v), want 350001", code, err)
	}
	o, err := c.ModifyOnboarding(ctx, GuildID, discord.Payload{"enabled": false, "default_channel_ids": channels[:6]})
	if err != nil {
		t.Fatal(err)
	}
	if o.Enabled || len(o.DefaultChannelIDs) != 6 {
		t.Errorf("onboarding = %+v, want disabled with 6 channels", o)
	}
}

func TestModifyOnboardingPromptIDs(t *testing.T) {
	s := NewServer()
	defer s.Close()
	c := discord.NewClient(s.URL, Token, "test")
	ctx := t.Context()
	option := discord.Payload{"title": "Option"}

	for _, id := range []string{"", "0", "abc"} {
		_, err := c.ModifyOnboarding(ctx, GuildID, discord.Payload{"prompts": []discord.Payload{onboardingPrompt(id, option)}})
		if code := apiErrorCode(err); code != 50035 {
			t.Errorf("prompt ID %q: code %d (%v), want 50035", id, code, err)
		}
	}
	_, err := c.ModifyOnboarding(ctx, GuildID, discord.Payload{"prompts": []discord.Payload{onboardingPrompt("123")}})
	if code := apiErrorCode(err); code != 50035 {
		t.Errorf("prompt without options: code %d (%v), want 50035", code, err)
	}

	o, err := c.ModifyOnboarding(ctx, GuildID, discord.Payload{"prompts": []discord.Payload{onboardingPrompt("123", option)}})
	if err != nil {
		t.Fatal(err)
	}
	prompt := o.Prompts[0]
	if prompt.ID == "123" || prompt.Options[0].ID == "" {
		t.Fatalf("new prompt kept its placeholder ID or has no option ID: %+v", prompt)
	}
	kept := onboardingPrompt(prompt.ID, discord.Payload{"id": prompt.Options[0].ID, "title": "Renamed"})
	o, err = c.ModifyOnboarding(ctx, GuildID, discord.Payload{"prompts": []discord.Payload{kept}})
	if err != nil {
		t.Fatal(err)
	}
	if got := o.Prompts[0]; got.ID != prompt.ID || got.Options[0].ID != prompt.Options[0].ID || got.Options[0].Title != "Renamed" {
		t.Errorf("existing IDs were not kept: %+v", got)
	}
}

func TestWelcomeScreenEnabledFeature(t *testing.T) {
	s := NewServer()
	defer s.Close()
	c := discord.NewClient(s.URL, Token, "test")
	ctx := t.Context()

	if _, err := c.GetWelcomeScreen(ctx, GuildID); !discord.IsNotFound(err) {
		t.Fatalf("unset welcome screen: %v, want 404", err)
	}
	for _, enabled := range []bool{true, false} {
		if _, err := c.ModifyWelcomeScreen(ctx, GuildID, discord.Payload{"enabled": enabled}); err != nil {
			t.Fatal(err)
		}
		g, err := c.GetGuild(ctx, GuildID)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, f := range g.Features {
			if f == welcomeScreenEnabled {
				n++
			}
		}
		if (n == 1) != enabled || n > 1 {
			t.Errorf("enabled=%v: features %v", enabled, g.Features)
		}
	}
}

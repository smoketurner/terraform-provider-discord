package discordtest

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// guildSettings holds a guild's singleton configuration objects. A nil
// welcome screen has never been configured.
type guildSettings struct {
	widget     discord.WidgetSettings
	welcome    *discord.WelcomeScreen
	onboarding discord.Onboarding
}

const welcomeScreenEnabled = "WELCOME_SCREEN_ENABLED"

// Onboarding requires this many default channels when enabled.
const minOnboardingChannels = 7

var promptIDRegexp = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

func (s *Server) handleGuildSettings(mux *http.ServeMux) {
	mux.HandleFunc("GET /guilds/{guild}/widget", s.getWidgetSettings)
	mux.HandleFunc("PATCH /guilds/{guild}/widget", s.modifyWidgetSettings)
	mux.HandleFunc("GET /guilds/{guild}/welcome-screen", s.getWelcomeScreen)
	mux.HandleFunc("PATCH /guilds/{guild}/welcome-screen", s.modifyWelcomeScreen)
	mux.HandleFunc("GET /guilds/{guild}/onboarding", s.getOnboarding)
	mux.HandleFunc("PUT /guilds/{guild}/onboarding", s.modifyOnboarding)
}

// guildSettings returns the settings of the request's guild, creating the
// defaults on first use, or writes 404 when the guild does not exist.
func (s *Server) guildSettings(w http.ResponseWriter, r *http.Request) (*discord.Guild, *guildSettings, bool) {
	g, ok := s.guild(w, r)
	if !ok {
		return nil, nil, false
	}
	if s.settings == nil {
		s.settings = map[string]*guildSettings{}
	}
	gs, ok := s.settings[g.ID]
	if !ok {
		gs = &guildSettings{onboarding: discord.Onboarding{
			GuildID: g.ID, Prompts: []discord.OnboardingPrompt{}, DefaultChannelIDs: []string{},
		}}
		s.settings[g.ID] = gs
	}
	return g, gs, true
}

func (s *Server) getWidgetSettings(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, gs, ok := s.guildSettings(w, r); ok {
		writeJSON(w, http.StatusOK, gs.widget)
	}
}

func (s *Server) modifyWidgetSettings(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, gs, ok := s.guildSettings(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	set(body, "enabled", &gs.widget.Enabled)
	set(body, "channel_id", &gs.widget.ChannelID)
	writeJSON(w, http.StatusOK, gs.widget)
}

func (s *Server) getWelcomeScreen(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, gs, ok := s.guildSettings(w, r)
	if !ok {
		return
	}
	if gs.welcome == nil {
		notFound(w, "Guild Welcome Screen", 10069)
		return
	}
	writeJSON(w, http.StatusOK, gs.welcome)
}

// modifyWelcomeScreen stores enabled as the guild's WELCOME_SCREEN_ENABLED
// feature, as Discord does; the welcome screen object has no enabled field.
func (s *Server) modifyWelcomeScreen(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, gs, ok := s.guildSettings(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	if gs.welcome == nil {
		gs.welcome = &discord.WelcomeScreen{WelcomeChannels: []discord.WelcomeScreenChannel{}}
	}
	set(body, "description", &gs.welcome.Description)
	set(body, "welcome_channels", &gs.welcome.WelcomeChannels)
	if gs.welcome.WelcomeChannels == nil {
		gs.welcome.WelcomeChannels = []discord.WelcomeScreenChannel{}
	}
	for i := range gs.welcome.WelcomeChannels {
		c := &gs.welcome.WelcomeChannels[i]
		if c.EmojiID == nil || c.EmojiName != nil {
			continue
		}
		if custom, ok := s.emojis[g.ID][*c.EmojiID]; ok {
			c.EmojiName = &custom.Name
		}
	}
	if _, ok := body["enabled"]; ok {
		var enabled bool
		set(body, "enabled", &enabled)
		g.Features = slices.DeleteFunc(g.Features, func(f string) bool { return f == welcomeScreenEnabled })
		if enabled {
			g.Features = append(g.Features, welcomeScreenEnabled)
		}
	}
	writeJSON(w, http.StatusOK, gs.welcome)
}

func (s *Server) getOnboarding(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, gs, ok := s.guildSettings(w, r); ok {
		writeJSON(w, http.StatusOK, gs.onboarding)
	}
}

type promptRequest struct {
	ID           string          `json:"id"`
	Type         int64           `json:"type"`
	Options      []optionRequest `json:"options"`
	Title        string          `json:"title"`
	SingleSelect bool            `json:"single_select"`
	Required     bool            `json:"required"`
	InOnboarding bool            `json:"in_onboarding"`
}

type optionRequest struct {
	ID            string   `json:"id"`
	ChannelIDs    []string `json:"channel_ids"`
	RoleIDs       []string `json:"role_ids"`
	EmojiID       *string  `json:"emoji_id"`
	EmojiName     *string  `json:"emoji_name"`
	EmojiAnimated *bool    `json:"emoji_animated"`
	Title         string   `json:"title"`
	Description   *string  `json:"description"`
}

// modifyOnboarding replaces the fields present in the body. Every prompt
// needs an ID; IDs of existing prompts and options are kept and any other ID
// is replaced with a new one. The minimum number of default channels is
// enforced while onboarding is enabled, counting the channels of prompt
// options too in advanced mode.
func (s *Server) modifyOnboarding(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, gs, ok := s.guildSettings(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	next := gs.onboarding
	set(body, "enabled", &next.Enabled)
	set(body, "mode", &next.Mode)
	set(body, "default_channel_ids", &next.DefaultChannelIDs)
	if next.DefaultChannelIDs == nil {
		next.DefaultChannelIDs = []string{}
	}
	if raw, ok := body["prompts"]; ok {
		var prompts []promptRequest
		if err := json.Unmarshal(raw, &prompts); err != nil {
			writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
			return
		}
		if next.Prompts, ok = s.onboardingPrompts(g.ID, prompts, gs.onboarding.Prompts); !ok {
			writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
			return
		}
	}
	if next.Enabled && onboardingChannels(&next) < minOnboardingChannels {
		if gs.onboarding.Enabled {
			writeError(w, http.StatusBadRequest, 350001, "Cannot update onboarding while below requirements")
		} else {
			writeError(w, http.StatusBadRequest, 350000, "Cannot enable onboarding, requirements are not met")
		}
		return
	}
	gs.onboarding = next
	writeJSON(w, http.StatusOK, gs.onboarding)
}

func (s *Server) onboardingPrompts(guildID string, req []promptRequest, current []discord.OnboardingPrompt) ([]discord.OnboardingPrompt, bool) {
	promptIDs := map[string]bool{}
	optionIDs := map[string]bool{}
	for _, p := range current {
		promptIDs[p.ID] = true
		for _, o := range p.Options {
			optionIDs[o.ID] = true
		}
	}
	out := make([]discord.OnboardingPrompt, 0, len(req))
	for _, p := range req {
		if !promptIDRegexp.MatchString(p.ID) || p.Title == "" || len(p.Options) == 0 {
			return nil, false
		}
		prompt := discord.OnboardingPrompt{
			ID: p.ID, Type: p.Type, Title: p.Title,
			SingleSelect: p.SingleSelect, Required: p.Required, InOnboarding: p.InOnboarding,
		}
		if !promptIDs[p.ID] {
			prompt.ID = s.newID()
		}
		for _, o := range p.Options {
			opt := discord.OnboardingPromptOption{
				ID: o.ID, Title: o.Title, ChannelIDs: o.ChannelIDs, RoleIDs: o.RoleIDs,
				Emoji: s.promptEmoji(guildID, o),
			}
			if !optionIDs[o.ID] {
				opt.ID = s.newID()
			}
			if opt.ChannelIDs == nil {
				opt.ChannelIDs = []string{}
			}
			if opt.RoleIDs == nil {
				opt.RoleIDs = []string{}
			}
			desc := ""
			if o.Description != nil {
				desc = *o.Description
			}
			opt.Description = &desc
			prompt.Options = append(prompt.Options, opt)
		}
		out = append(out, prompt)
	}
	return out, true
}

// promptEmoji returns the emoji object Discord reads back, which names a
// custom emoji even when only its ID was sent.
func (s *Server) promptEmoji(guildID string, o optionRequest) *discord.PromptEmoji {
	e := &discord.PromptEmoji{ID: o.EmojiID, Name: o.EmojiName}
	if o.EmojiID != nil {
		if custom, ok := s.emojis[guildID][*o.EmojiID]; ok {
			if e.Name == nil {
				e.Name = &custom.Name
			}
			e.Animated = custom.Animated
		}
	}
	if o.EmojiAnimated != nil {
		e.Animated = *o.EmojiAnimated
	}
	return e
}

func onboardingChannels(o *discord.Onboarding) int {
	channels := map[string]bool{}
	for _, id := range o.DefaultChannelIDs {
		channels[id] = true
	}
	if o.Mode == 1 {
		for _, p := range o.Prompts {
			for _, opt := range p.Options {
				for _, id := range opt.ChannelIDs {
					channels[id] = true
				}
			}
		}
	}
	return len(channels)
}

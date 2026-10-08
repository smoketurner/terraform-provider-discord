package discordtest

import (
	"net/http"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

func (s *Server) handleFollowers(mux *http.ServeMux) {
	mux.HandleFunc("POST /channels/{channel}/followers", s.followChannel)
}

// webhookResponse adds the followed channel to Channel Follower webhooks.
type webhookResponse struct {
	*discord.Webhook
	SourceChannel *discord.WebhookSourceChannel `json:"source_channel,omitempty"`
}

func (s *Server) webhookResponse(wh *discord.Webhook) webhookResponse {
	resp := webhookResponse{Webhook: wh}
	if src, ok := s.channels[s.follows[wh.ID]]; ok {
		resp.SourceChannel = &discord.WebhookSourceChannel{ID: src.ID, Name: src.Name}
	}
	return resp
}

func (s *Server) followChannel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.channel(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	var targetID string
	set(body, "webhook_channel_id", &targetID)
	target, ok := s.channels[targetID]
	if !ok {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
		return
	}
	if src.Type != discord.ChannelTypeAnnouncement ||
		(target.Type != discord.ChannelTypeText && target.Type != discord.ChannelTypeAnnouncement) {
		writeError(w, http.StatusBadRequest, 50024, "Cannot execute action on this channel type")
		return
	}
	name := s.guilds[src.GuildID].Name
	wh := &discord.Webhook{ID: s.newID(), Type: 2, GuildID: target.GuildID, ChannelID: target.ID, Name: &name}
	s.webhooks[wh.ID] = wh
	s.follows[wh.ID] = src.ID
	writeJSON(w, http.StatusOK, discord.FollowedChannel{ChannelID: src.ID, WebhookID: wh.ID})
}

// LoseFollowSource makes a Channel Follower webhook omit its source channel,
// as Discord does once the bot loses access to the followed server.
func (s *Server) LoseFollowSource(webhookID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.follows, webhookID)
}

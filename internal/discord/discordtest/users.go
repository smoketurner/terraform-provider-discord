package discordtest

import (
	"net/http"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// ApplicationID is the ID of the application the fake bot token belongs to.
const ApplicationID = "100000000000000004"

func (s *Server) handleUsers(mux *http.ServeMux) {
	mux.HandleFunc("GET /users/@me", s.getCurrentUser)
	mux.HandleFunc("GET /users/{user}", s.getUser)
	mux.HandleFunc("GET /applications/@me", s.getCurrentApplication)
}

// application returns the bot's application, creating it on first use.
func (s *Server) application() *discord.Application {
	if s.app == nil {
		s.app = &discord.Application{
			ID:        ApplicationID,
			Name:      "Test App",
			Bot:       &discord.User{ID: s.botUserID, Username: "bot", Discriminator: "0", Bot: true},
			Owner:     &discord.User{ID: UserID, Username: "tester", Discriminator: "0"},
			VerifyKey: "0123456789abcdef",
			Tags:      []string{},
		}
	}
	return s.app
}

// user finds a user by ID: the bot user or a member of any guild.
func (s *Server) user(id string) *discord.User {
	if bot := s.application().Bot; bot.ID == id {
		return bot
	}
	for _, members := range s.members {
		if m, ok := members[id]; ok {
			return m.User
		}
	}
	return nil
}

func (s *Server) getCurrentUser(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.application().Bot)
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.user(r.PathValue("user"))
	if u == nil {
		notFound(w, "User", 10013)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) getCurrentApplication(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.application())
}

// UpdateUser changes a user outside Terraform, as when they edit their
// profile. It panics if the user does not exist.
func (s *Server) UpdateUser(id string, f func(*discord.User)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s.user(id))
}

// UpdateApplication changes the bot's application outside Terraform, as in
// the Developer Portal.
func (s *Server) UpdateApplication(f func(*discord.Application)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s.application())
}

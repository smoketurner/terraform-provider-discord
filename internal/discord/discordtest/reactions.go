package discordtest

import (
	"cmp"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

func (s *Server) handleReactions(mux *http.ServeMux) {
	mux.HandleFunc("PUT /channels/{channel}/messages/{message}/reactions/{emoji}/@me", s.addOwnReaction)
	mux.HandleFunc("DELETE /channels/{channel}/messages/{message}/reactions/{emoji}/@me", s.deleteOwnReaction)
	mux.HandleFunc("GET /channels/{channel}/messages/{message}/reactions/{emoji}", s.listReactions)
}

var customEmojiRegexp = regexp.MustCompile(`^[A-Za-z0-9_]{2,32}:([0-9]+)$`)

// knownEmoji mimics Discord's check of the emoji path segment: a unicode
// emoji, or "name:id" of an existing custom emoji.
func (s *Server) knownEmoji(emoji string) bool {
	if m := customEmojiRegexp.FindStringSubmatch(emoji); m != nil {
		name, _, _ := strings.Cut(emoji, ":")
		for _, emojis := range s.emojis {
			if e, ok := emojis[m[1]]; ok && e.Name == name {
				return true
			}
		}
		return false
	}
	return emoji != "" && utf8.RuneCountInString(emoji) != len(emoji) && !strings.ContainsAny(emoji, ": ")
}

// reactionTarget resolves the message and emoji of a reaction request, or
// writes the error Discord returns.
func (s *Server) reactionTarget(w http.ResponseWriter, r *http.Request) (*discord.Message, string, bool) {
	m, ok := s.message(w, r)
	if !ok {
		return nil, "", false
	}
	emoji := r.PathValue("emoji")
	if !s.knownEmoji(emoji) {
		writeError(w, http.StatusBadRequest, 10014, "Unknown Emoji")
		return nil, "", false
	}
	return m, emoji, true
}

func (s *Server) addOwnReaction(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, emoji, ok := s.reactionTarget(w, r)
	if !ok {
		return
	}
	s.addReaction(m.ID, emoji, s.botUserID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteOwnReaction(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, emoji, ok := s.reactionTarget(w, r)
	if !ok {
		return
	}
	s.removeReaction(m.ID, emoji, s.botUserID)
	w.WriteHeader(http.StatusNoContent)
}

// compareIDs orders snowflakes numerically.
func compareIDs(a, b string) int {
	return cmp.Or(cmp.Compare(len(a), len(b)), strings.Compare(a, b))
}

func (s *Server) listReactions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, emoji, ok := s.reactionTarget(w, r)
	if !ok {
		return
	}
	limit := 25
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
			return
		}
		limit = n
	}
	after := r.URL.Query().Get("after")
	users := []discord.User{}
	for _, id := range s.reactions[m.ID][emoji] {
		if len(users) == limit {
			break
		}
		if after == "" || compareIDs(id, after) > 0 {
			users = append(users, discord.User{ID: id, Username: "user" + id, Discriminator: "0", Bot: id == s.botUserID})
		}
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) addReaction(messageID, emoji, userID string) {
	if s.reactions[messageID] == nil {
		s.reactions[messageID] = map[string][]string{}
	}
	users := s.reactions[messageID][emoji]
	if i, found := slices.BinarySearchFunc(users, userID, compareIDs); !found {
		s.reactions[messageID][emoji] = slices.Insert(users, i, userID)
	}
}

func (s *Server) removeReaction(messageID, emoji, userID string) {
	if s.reactions[messageID] == nil {
		return
	}
	s.reactions[messageID][emoji] = slices.DeleteFunc(s.reactions[messageID][emoji], func(id string) bool { return id == userID })
}

// AddReaction adds a user's reaction to a message, simulating other members
// reacting.
func (s *Server) AddReaction(messageID, emoji, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addReaction(messageID, emoji, userID)
}

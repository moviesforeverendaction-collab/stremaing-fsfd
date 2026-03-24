package bot

import (
	"EverythingSuckz/fsb/config"
	"fmt"
	"sync"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

// FsubState holds the current force-subscribe channel configuration.
// It is set via /setfsub and /removefsub commands handled by the Python bot.
// The Go bot reads this state to gate file uploads.
// In a production setup, persist this to a database.
type FsubState struct {
	mu          sync.RWMutex
	ChannelID   int64
	ChannelLink string
	Enabled     bool
}

var Fsub = &FsubState{}

func (f *FsubState) Set(channelID int64, link string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ChannelID = channelID
	f.ChannelLink = link
	f.Enabled = true
}

func (f *FsubState) Clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ChannelID = 0
	f.ChannelLink = ""
	f.Enabled = false
}

func (f *FsubState) IsEnabled() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.Enabled
}

func (f *FsubState) Get() (int64, string) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.ChannelID, f.ChannelLink
}

// CheckFsub returns true if the user is subscribed (or fsub is disabled).
// It calls Telegram's GetParticipant API on the log channel.
func CheckFsub(ctx *ext.Context, userID int64, log *zap.Logger) bool {
	if !Fsub.IsEnabled() {
		return true
	}
	channelID, _ := Fsub.Get()

	// Use the main bot client to check membership
	if Bot == nil {
		return true // fail open if bot not ready
	}

	result, err := Bot.API().ChannelsGetParticipant(
		ctx,
		&tg.ChannelsGetParticipantRequest{
			Channel: &tg.InputChannel{ChannelID: channelID},
			Participant: &tg.InputPeerUser{
				UserID: userID,
			},
		},
	)
	if err != nil {
		log.Sugar().Debugf("fsub check error for user %d: %v", userID, err)
		// If error contains "USER_NOT_PARTICIPANT" → not subscribed
		if isNotParticipant(err) {
			return false
		}
		return true // fail open on other errors
	}

	switch result.Participant.(type) {
	case *tg.ChannelParticipantBanned, *tg.ChannelParticipantLeft:
		return false
	default:
		return true
	}
}

func isNotParticipant(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return contains(errStr, "USER_NOT_PARTICIPANT") ||
		contains(errStr, "not a member") ||
		contains(errStr, "CHANNEL_PRIVATE")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub ||
		len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// FsubMiddlewareHandler wraps a gotgproto handler with fsub gate.
// If user is not subscribed, it sends them a join prompt and stops the chain.
func FsubGate(log *zap.Logger) func(*ext.Context, *ext.Update) error {
	return func(ctx *ext.Context, u *ext.Update) error {
		if !Fsub.IsEnabled() {
			return nil // pass through
		}

		chatID := u.EffectiveChat().GetID()
		if !CheckFsub(ctx, chatID, log) {
			_, channelLink := Fsub.Get()
			msg := "🔒 You need to join our channel to use this bot."
			if channelLink != "" {
				msg = fmt.Sprintf(
					"🔒 **Join Required**\n\nPlease join our channel to use this bot:\n%s\n\nOnce joined, send your file again.",
					channelLink,
				)
			}
			ctx.Reply(u, ext.ReplyTextString(msg), nil)
			return dispatcher.EndGroups
		}
		return nil
	}
}

// LoadFsubCommands registers /setfsub, /removefsub, /fsub commands in Go.
// These mirror what the Python bot does but work for the Go-only fallback.
func LoadFsubCommands(log *zap.Logger, d dispatcher.Dispatcher) {
	log = log.Named("fsub")
	defer log.Info("Loaded fsub commands")

	// /setfsub — admin only, sets the fsub channel
	d.AddHandlerToGroup(
		newFsubCommandHandler(log),
		-1,
	)
}

// newFsubCommandHandler is a placeholder — fsub commands are handled by
// the Python bot (colored buttons). The Go side only reads Fsub state.
// If you want Go-only fsub commands (no colors), implement them here.
func newFsubCommandHandler(log *zap.Logger) dispatcher.Handler {
	// Returns a no-op handler — actual commands handled by Python bot
	return nil
}

// AllowedUsersCheck checks if user is in the allowed list (existing feature).
func AllowedUsersCheck(userID int64) bool {
	if len(config.ValueOf.AllowedUsers) == 0 {
		return true
	}
	for _, id := range config.ValueOf.AllowedUsers {
		if id == userID {
			return true
		}
	}
	return false
}

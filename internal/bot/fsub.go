package bot

import (
	"EverythingSuckz/fsb/config"
	"fmt"
	"strings"
	"sync"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

// FsubState holds the current force-subscribe channel configuration.
// Managed by the Python bot via /setfsub and /removefsub commands.
// Go reads this state to gate file uploads in the stream command.
type FsubState struct {
	mu          sync.RWMutex
	ChannelID   int64
	AccessHash  int64
	ChannelLink string
	Enabled     bool
}

var Fsub = &FsubState{}

func (f *FsubState) Set(channelID, accessHash int64, link string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ChannelID = channelID
	f.AccessHash = accessHash
	f.ChannelLink = link
	f.Enabled = true
}

func (f *FsubState) Clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ChannelID = 0
	f.AccessHash = 0
	f.ChannelLink = ""
	f.Enabled = false
}

func (f *FsubState) IsEnabled() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.Enabled
}

func (f *FsubState) Get() (channelID, accessHash int64, link string) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.ChannelID, f.AccessHash, f.ChannelLink
}

// CheckFsub returns true if the user is subscribed (or fsub is disabled).
func CheckFsub(ctx *ext.Context, userID int64, log *zap.Logger) bool {
	if !Fsub.IsEnabled() {
		return true
	}
	if Bot == nil {
		return true // fail open if bot not ready yet
	}

	channelID, accessHash, _ := Fsub.Get()

	result, err := Bot.API().ChannelsGetParticipant(
		ctx,
		&tg.ChannelsGetParticipantRequest{
			Channel: &tg.InputChannel{
				ChannelID:  channelID,
				AccessHash: accessHash,
			},
			Participant: &tg.InputPeerUser{UserID: userID},
		},
	)
	if err != nil {
		errStr := err.Error()
		log.Sugar().Debugf("fsub check for user %d: %v", userID, errStr)
		if strings.Contains(errStr, "USER_NOT_PARTICIPANT") ||
			strings.Contains(errStr, "CHANNEL_PRIVATE") {
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

// FsubGate is a gotgproto middleware that blocks non-subscribers.
func FsubGate(log *zap.Logger) func(*ext.Context, *ext.Update) error {
	return func(ctx *ext.Context, u *ext.Update) error {
		if !Fsub.IsEnabled() {
			return nil
		}
		chatID := u.EffectiveChat().GetID()
		if !CheckFsub(ctx, chatID, log) {
			_, _, channelLink := Fsub.Get()
			msg := "🔒 You need to join our channel to use this bot."
			if channelLink != "" {
				msg = fmt.Sprintf(
					"🔒 **Join Required**\n\nPlease join our channel first:\n%s\n\nOnce joined, send your file again.",
					channelLink,
				)
			}
			ctx.Reply(u, ext.ReplyTextString(msg), nil)
			return dispatcher.EndGroups
		}
		return nil
	}
}

// AllowedUsersCheck checks if user is in the ALLOWED_USERS list.
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

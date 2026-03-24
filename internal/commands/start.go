package commands

import (
	"github.com/celestix/gotgproto/dispatcher"
)

// LoadStart is intentionally a no-op.
// /start is handled entirely by the Python UI bot (internal/pybot/bot.py)
// which sends the rich UI with colored inline buttons and fsub gate.
// Having Go also reply would cause a double-response conflict.
func (m *command) LoadStart(d dispatcher.Dispatcher) {
	log := m.log.Named("start")
	log.Sugar().Info("Loaded (delegated to Python UI bot)")
}

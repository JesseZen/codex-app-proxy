package manager

import (
	"strings"

	"github.com/jesse/codex-app-proxy/internal/config"
)

// CAP-owned tmux namespace. All hosted-terminal commands use `tmux -L cap` to
// isolate CAP-managed sessions from user tmux sessions.
const (
	tmuxSocketName   = "cap"
	tmuxHostSession  = "cap-host"
	tmuxWindowPrefix = "codex"
)

func defaultTmuxSettings() config.Settings {
	var cfg config.Config
	cfg.ApplyDefaults()
	return cfg.Settings
}

func tmuxPrefixForSettings(settings config.Settings) []string {
	settingsConfig := config.Config{Settings: settings}
	settingsConfig.ApplyDefaults()
	return []string{"tmux", "-L", settingsConfig.Settings.Terminal.Tmux.SocketName}
}

func tmuxHostSessionForSettings(settings config.Settings) string {
	settingsConfig := config.Config{Settings: settings}
	settingsConfig.ApplyDefaults()
	return settingsConfig.Settings.Terminal.Tmux.HostSession
}

func tmuxPrefix() []string {
	return tmuxPrefixForSettings(defaultTmuxSettings())
}

// TmuxDetectCommand returns the argv used to verify tmux is installed.
func TmuxDetectCommand() []string {
	return []string{"tmux", "-V"}
}

// TmuxHasSessionCommand returns the argv that checks whether the CAP host session exists.
func TmuxHasSessionCommand() []string {
	return TmuxHasSessionCommandForSettings(defaultTmuxSettings())
}

func TmuxHasSessionCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "has-session", "-t", tmuxHostSessionForSettings(settings))
}

// TmuxStartHostCommand returns the argv that starts the detached CAP host session.
func TmuxStartHostCommand() []string {
	return TmuxStartHostCommandForSettings(defaultTmuxSettings())
}

func TmuxStartHostCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "new-session", "-d", "-s", tmuxHostSessionForSettings(settings))
}

// TmuxCreateWindowCommand returns the argv that creates a new window in the CAP host
// running the given command.
func TmuxCreateWindowCommand(windowName string, command []string) []string {
	return TmuxCreateWindowCommandForSettings(defaultTmuxSettings(), windowName, command)
}

func TmuxCreateWindowCommandForSettings(settings config.Settings, windowName string, command []string) []string {
	args := append(tmuxPrefixForSettings(settings), "new-window", "-t", tmuxHostSessionForSettings(settings), "-n", windowName, "-P", "-F", "#{window_id}")
	return append(args, command...)
}

// TmuxSelectWindowCommand returns the argv that switches to a window in the CAP host.
func TmuxSelectWindowCommand(windowID string) []string {
	return TmuxSelectWindowCommandForSettings(defaultTmuxSettings(), windowID)
}

func TmuxSelectWindowCommandForSettings(settings config.Settings, windowID string) []string {
	target := tmuxHostSessionForSettings(settings) + ":" + windowID
	return append(tmuxPrefixForSettings(settings), "select-window", "-t", target)
}

// TmuxAttachCommand returns the argv that attaches to the CAP host session.
func TmuxAttachCommand() []string {
	return TmuxAttachCommandForSettings(defaultTmuxSettings())
}

func TmuxAttachCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "attach-session", "-t", tmuxHostSessionForSettings(settings))
}

// TmuxShowMouseCommand returns the argv that reads the CAP host mouse setting.
func TmuxShowMouseCommand() []string {
	return TmuxShowMouseCommandForSettings(defaultTmuxSettings())
}

func TmuxShowMouseCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "show", "-gv", "mouse")
}

// TmuxEnableMouseCommand returns the argv that enables mouse support in the CAP host.
func TmuxEnableMouseCommand() []string {
	return TmuxEnableMouseCommandForSettings(defaultTmuxSettings())
}

func TmuxEnableMouseCommandForSettings(settings config.Settings) []string {
	return append(tmuxPrefixForSettings(settings), "set-option", "-g", "mouse", "on")
}

// SafeWindowName generates a tmux-safe window name from a session identifier.
// Non-alphanumeric characters (except `-` and `_`) are replaced with `-` so the
// name can be used unambiguously in tmux targets like `cap-host:<window>`.
func SafeWindowName(sessionID string) string {
	var b strings.Builder
	b.WriteString(tmuxWindowPrefix)
	b.WriteByte(':')
	for _, r := range sessionID {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
}

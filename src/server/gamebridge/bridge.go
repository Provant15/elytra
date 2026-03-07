package gamebridge

import (
	"regexp"
	"time"
)

// Player represents a connected game player.
type Player struct {
	Name string `json:"name"`
	UUID string `json:"uuid,omitempty"`
}

// PlayerList holds the current connected player state.
type PlayerList struct {
	Players []Player `json:"players"`
	Count   int      `json:"count"`
	Max     int      `json:"max"`
}

// ActionResult holds the outcome of a player action.
type ActionResult struct {
	Success  bool   `json:"success"`
	Message  string `json:"message,omitempty"`
	Response string `json:"response,omitempty"`
}

// BridgeStatus reports the current bridge connection state.
type BridgeStatus struct {
	Connected bool       `json:"connected"`
	Mode      string     `json:"mode"` // "rcon" or "fallback"
	Since     *time.Time `json:"since,omitempty"`
}

// ValidPlayerName matches Minecraft's player name rules (letters, digits, underscores, 1-16 chars).
var ValidPlayerName = regexp.MustCompile(`^[a-zA-Z0-9_]{1,16}$`)

// ValidActions is the strict allowlist of supported player actions.
var ValidActions = map[string]bool{
	"kick": true, "ban": true, "message": true, "smite": true,
	"teleport": true, "gamemode": true, "op": true, "deop": true,
}

// validCoordinate matches Minecraft coordinate values: integers, decimals, or relative (~, ~10, ~-5.5).
// Uses alternation to ensure at least one meaningful character (prevents empty match).
var validCoordinate = regexp.MustCompile(`^(~(-?\d{1,10}(\.\d{1,5})?)?|-?\d{1,10}(\.\d{1,5})?)$`)

// SanitizeTextParam strips control characters and limits length for text params
// (reason, message) to prevent command injection via embedded newlines or
// excessive payloads. Returns the sanitized string, safe for command interpolation.
func SanitizeTextParam(s string, maxLen int) string {
	// Strip any control characters (newlines, tabs, carriage returns, etc.).
	var clean []rune
	for _, r := range s {
		if r >= 32 && r != 127 {
			clean = append(clean, r)
		}
	}
	result := string(clean)
	if len(result) > maxLen {
		result = result[:maxLen]
	}
	return result
}

// ValidateCoordinate checks that a coordinate string is a valid Minecraft coordinate.
func ValidateCoordinate(s string) bool {
	return validCoordinate.MatchString(s)
}

// Bridge is the game-agnostic interface for communicating with game servers.
// Each game type (Minecraft RCON, console fallback, future Valheim, etc.)
// provides its own implementation.
type Bridge interface {
	// GetPlayers returns the current connected player list.
	GetPlayers() (PlayerList, error)

	// ExecuteAction performs a named action on a player.
	// Player name must match ValidPlayerName. Action must be in ValidActions.
	ExecuteAction(player string, action string, params map[string]string) (ActionResult, error)

	// ExecuteCommand sends a raw command and returns the response.
	ExecuteCommand(command string) (ActionResult, error)

	// Status returns the bridge connection status.
	Status() BridgeStatus

	// Close cleans up any connections held by the bridge.
	Close() error
}

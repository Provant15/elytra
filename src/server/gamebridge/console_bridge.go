package gamebridge

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/apex/log"
	"github.com/pyrohost/elytra/src/environment"
)

// ConsoleBridge implements Bridge by sending commands through the Docker
// container's stdin. This is the fallback when RCON is unavailable.
type ConsoleBridge struct {
	env environment.ProcessEnvironment
	log *log.Entry
}

// NewConsoleBridge creates a console-based bridge.
func NewConsoleBridge(env environment.ProcessEnvironment, logger *log.Entry) *ConsoleBridge {
	return &ConsoleBridge{
		env: env,
		log: logger,
	}
}

// GetPlayers sends the `list` command via console.
// Console bridge cannot capture command output directly, so this returns
// an empty list. RCON is the recommended mode for full functionality.
func (b *ConsoleBridge) GetPlayers() (PlayerList, error) {
	if err := b.env.SendCommand("list"); err != nil {
		return PlayerList{}, fmt.Errorf("failed to send list command: %w", err)
	}
	return PlayerList{Players: []Player{}, Count: 0, Max: 0}, nil
}

// ExecuteAction sends the corresponding Minecraft command via console.
// Validates player name and action before composing the command.
func (b *ConsoleBridge) ExecuteAction(player string, action string, params map[string]string) (ActionResult, error) {
	if !ValidPlayerName.MatchString(player) {
		return ActionResult{Success: false, Message: "invalid player name"}, nil
	}
	if !ValidActions[action] {
		return ActionResult{Success: false, Message: fmt.Sprintf("unknown action: %s", action)}, nil
	}

	var cmd string

	switch action {
	case "kick":
		reason := SanitizeTextParam(params["reason"], 200)
		if reason != "" {
			cmd = fmt.Sprintf("kick %s %s", player, reason)
		} else {
			cmd = fmt.Sprintf("kick %s", player)
		}
	case "ban":
		reason := SanitizeTextParam(params["reason"], 200)
		if reason != "" {
			cmd = fmt.Sprintf("ban %s %s", player, reason)
		} else {
			cmd = fmt.Sprintf("ban %s", player)
		}
	case "message":
		msg := SanitizeTextParam(params["message"], 500)
		if msg == "" {
			return ActionResult{Success: false, Message: "message cannot be empty"}, nil
		}
		cmd = fmt.Sprintf("tell %s %s", player, msg)
	case "smite":
		cmd = fmt.Sprintf("execute at %s run summon lightning_bolt", player)
	case "teleport":
		if target := params["target"]; target != "" {
			if !ValidPlayerName.MatchString(target) {
				return ActionResult{Success: false, Message: "invalid target player name"}, nil
			}
			cmd = fmt.Sprintf("tp %s %s", player, target)
		} else {
			x, y, z := params["x"], params["y"], params["z"]
			if !ValidateCoordinate(x) || !ValidateCoordinate(y) || !ValidateCoordinate(z) {
				return ActionResult{Success: false, Message: "invalid coordinates"}, nil
			}
			cmd = fmt.Sprintf("tp %s %s %s %s", player, x, y, z)
		}
	case "gamemode":
		mode := params["mode"]
		validModes := map[string]bool{
			"survival": true, "creative": true,
			"adventure": true, "spectator": true,
		}
		if !validModes[mode] {
			return ActionResult{Success: false, Message: "invalid gamemode"}, nil
		}
		cmd = fmt.Sprintf("gamemode %s %s", mode, player)
	case "op":
		cmd = fmt.Sprintf("op %s", player)
	case "deop":
		cmd = fmt.Sprintf("deop %s", player)
	default:
		return ActionResult{Success: false, Message: fmt.Sprintf("unknown action: %s", action)}, nil
	}

	if err := b.env.SendCommand(cmd); err != nil {
		return ActionResult{Success: false, Message: err.Error()}, err
	}

	return ActionResult{
		Success: true,
		Message: "Command sent via console (no response available in fallback mode)",
	}, nil
}

// ExecuteCommand sends a raw command via console.
func (b *ConsoleBridge) ExecuteCommand(command string) (ActionResult, error) {
	if err := b.env.SendCommand(command); err != nil {
		return ActionResult{Success: false, Message: err.Error()}, err
	}
	return ActionResult{
		Success: true,
		Message: "Command sent via console",
	}, nil
}

// Status returns the console bridge status (always "connected" if server is running).
func (b *ConsoleBridge) Status() BridgeStatus {
	return BridgeStatus{
		Connected: true,
		Mode:      "fallback",
	}
}

// Close is a no-op for the console bridge.
func (b *ConsoleBridge) Close() error {
	return nil
}

// consoleListRegex matches the Minecraft `list` output from console.
var consoleListRegex = regexp.MustCompile(
	`There are (\d+) of a max of (\d+) players online:\s*(.*)`,
)

// ParseListOutput attempts to parse a Minecraft `/list` console output line.
// Used by the subscriber when operating in fallback mode.
func ParseListOutput(line string) (PlayerList, bool) {
	matches := consoleListRegex.FindStringSubmatch(line)
	if matches == nil {
		return PlayerList{}, false
	}

	count, _ := strconv.Atoi(matches[1])
	max, _ := strconv.Atoi(matches[2])

	var players []Player
	if count > 0 && strings.TrimSpace(matches[3]) != "" {
		for _, name := range strings.Split(matches[3], ", ") {
			name = strings.TrimSpace(name)
			if name != "" {
				players = append(players, Player{Name: name})
			}
		}
	}
	if players == nil {
		players = []Player{}
	}

	return PlayerList{Players: players, Count: count, Max: max}, true
}

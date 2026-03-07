package gamebridge

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apex/log"
	"github.com/cenkalti/backoff/v4"
	"github.com/gorcon/rcon"
	"github.com/magiconair/properties"
)

// RCONBridge implements Bridge using Minecraft's Source RCON protocol.
type RCONBridge struct {
	mu   sync.Mutex
	conn *rcon.Conn
	log  *log.Entry

	// Connection details read from server.properties.
	host     string
	port     int
	password string

	// Path to the server.properties file within the server filesystem.
	propsPath string

	connected bool
	connSince *time.Time
}

// NewRCONBridge creates a new RCON bridge. It does NOT connect immediately -
// connections are established lazily on first use.
func NewRCONBridge(propsPath string, logger *log.Entry) *RCONBridge {
	return &RCONBridge{
		propsPath: propsPath,
		log:       logger,
		host:      "127.0.0.1",
	}
}

// loadCredentials reads RCON connection details from server.properties.
func (b *RCONBridge) loadCredentials() error {
	props, err := properties.LoadFile(b.propsPath, properties.UTF8)
	if err != nil {
		return fmt.Errorf("failed to read server.properties: %w", err)
	}

	b.port = props.GetInt("rcon.port", 25575)
	b.password = props.GetString("rcon.password", "")
	if b.password == "" {
		return fmt.Errorf("rcon.password is empty in server.properties")
	}

	return nil
}

// ensureConnected lazily establishes the RCON connection with retry/backoff.
func (b *RCONBridge) ensureConnected() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.conn != nil && b.connected {
		return nil
	}

	if err := b.loadCredentials(); err != nil {
		return err
	}

	addr := fmt.Sprintf("%s:%d", b.host, b.port)
	// Intentionally do NOT log the password.
	b.log.WithField("addr", addr).Info("connecting to RCON")

	var conn *rcon.Conn
	op := func() error {
		var err error
		conn, err = rcon.Dial(addr, b.password)
		return err
	}

	eb := backoff.NewExponentialBackOff()
	eb.MaxElapsedTime = 30 * time.Second
	eb.InitialInterval = 1 * time.Second

	if err := backoff.Retry(op, eb); err != nil {
		return fmt.Errorf("failed to connect to RCON at %s: %w", addr, err)
	}

	b.conn = conn
	b.connected = true
	now := time.Now()
	b.connSince = &now
	b.log.Info("RCON connection established")
	return nil
}

// execute sends a command via RCON with automatic reconnection.
func (b *RCONBridge) execute(command string) (string, error) {
	if err := b.ensureConnected(); err != nil {
		return "", err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	resp, err := b.conn.Execute(command)
	if err != nil {
		// Connection lost - mark as disconnected so next call reconnects.
		b.connected = false
		if b.conn != nil {
			b.conn.Close()
		}
		b.conn = nil
		return "", fmt.Errorf("RCON command failed: %w", err)
	}

	return resp, nil
}

// listRegex matches Minecraft's /list response.
// Format: "There are N of a max of M players online: Player1, Player2"
var listRegex = regexp.MustCompile(
	`There are (\d+) of a max of (\d+) players online:\s*(.*)`,
)

// GetPlayers queries the connected player list via RCON `list`.
func (b *RCONBridge) GetPlayers() (PlayerList, error) {
	resp, err := b.execute("list")
	if err != nil {
		return PlayerList{}, err
	}

	matches := listRegex.FindStringSubmatch(resp)
	if matches == nil {
		return PlayerList{Count: 0, Max: 0, Players: []Player{}}, nil
	}

	count, _ := strconv.Atoi(matches[1])
	max, _ := strconv.Atoi(matches[2])

	var players []Player
	if count > 0 && strings.TrimSpace(matches[3]) != "" {
		names := strings.Split(matches[3], ", ")
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name != "" {
				players = append(players, Player{Name: name})
			}
		}
	}
	if players == nil {
		players = []Player{}
	}

	return PlayerList{
		Players: players,
		Count:   count,
		Max:     max,
	}, nil
}

// ExecuteAction maps a named action to a Minecraft RCON command.
// Validates player name and action before composing the command.
func (b *RCONBridge) ExecuteAction(player string, action string, params map[string]string) (ActionResult, error) {
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
		target := params["target"]
		if target != "" {
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

	resp, err := b.execute(cmd)
	if err != nil {
		return ActionResult{Success: false, Message: err.Error()}, err
	}

	return ActionResult{
		Success:  true,
		Response: resp,
	}, nil
}

// ExecuteCommand sends a raw RCON command.
func (b *RCONBridge) ExecuteCommand(command string) (ActionResult, error) {
	resp, err := b.execute(command)
	if err != nil {
		return ActionResult{Success: false, Message: err.Error()}, err
	}

	return ActionResult{
		Success:  true,
		Response: resp,
	}, nil
}

// Status returns the current RCON connection status.
func (b *RCONBridge) Status() BridgeStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	return BridgeStatus{
		Connected: b.connected,
		Mode:      "rcon",
		Since:     b.connSince,
	}
}

// Close shuts down the RCON connection.
func (b *RCONBridge) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn != nil {
		b.conn.Close()
		b.conn = nil
		b.connected = false
	}
	return nil
}

package router

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/pyrohost/elytra/src/server"
	"github.com/pyrohost/elytra/src/server/gamebridge"
)

// requireBridge extracts the server and verifies it has an active game bridge
// and is in a running state (same guard as postServerCommands).
func requireBridge(c *gin.Context) (*server.Server, gamebridge.Bridge, bool) {
	s := ExtractServer(c)

	if running, err := s.Environment.IsRunning(c.Request.Context()); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to check server state"})
		return nil, nil, false
	} else if !running {
		c.JSON(http.StatusBadGateway, gin.H{"error": "server must be online"})
		return nil, nil, false
	}

	bridge := s.Bridge()
	if bridge == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no game bridge available for this server"})
		return nil, nil, false
	}

	return s, bridge, true
}

// getServerPlayers returns the current player list from the game bridge.
func getServerPlayers(c *gin.Context) {
	_, bridge, ok := requireBridge(c)
	if !ok {
		return
	}

	players, err := bridge.GetPlayers()
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, players)
}

// postServerPlayerAction executes an action on a specific player.
// Player name and action are in the request body (not URL) to avoid
// encoding issues with special characters.
func postServerPlayerAction(c *gin.Context) {
	_, bridge, ok := requireBridge(c)
	if !ok {
		return
	}

	var body struct {
		Player string            `json:"player" binding:"required"`
		Action string            `json:"action" binding:"required"`
		Params map[string]string `json:"params"`
	}

	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate player name against Minecraft's rules.
	if !gamebridge.ValidPlayerName.MatchString(body.Player) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid player name"})
		return
	}

	// Validate action against strict allowlist.
	if !gamebridge.ValidActions[body.Action] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown action"})
		return
	}

	result, err := bridge.ExecuteAction(body.Player, body.Action, body.Params)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// postServerPlayerCommand sends a raw command through the game bridge.
func postServerPlayerCommand(c *gin.Context) {
	_, bridge, ok := requireBridge(c)
	if !ok {
		return
	}

	var body struct {
		Command string `json:"command" binding:"required"`
	}

	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := bridge.ExecuteCommand(body.Command)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// getServerPlayersStatus returns the game bridge connection status.
func getServerPlayersStatus(c *gin.Context) {
	_, bridge, ok := requireBridge(c)
	if !ok {
		return
	}

	c.JSON(http.StatusOK, bridge.Status())
}

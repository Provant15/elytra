package gamebridge

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/magiconair/properties"
	"github.com/pyrohost/elytra/src/environment"
)

const (
	// FeatureMinecraftRCON is the egg feature flag for Minecraft RCON support.
	FeatureMinecraftRCON = "minecraft_rcon"

	// markerFile is written when Elytra auto-enables RCON.
	// If the user later sets enable-rcon=false, Elytra respects the override.
	markerFile = ".elytra-rcon-auto"
)

// ConfigureAndCreate inspects the server's egg features and filesystem to
// determine which bridge to create. For Minecraft RCON, it ensures RCON is
// enabled in server.properties. Returns nil if no bridge applies.
func ConfigureAndCreate(
	features []string,
	serverRoot string,
	env environment.ProcessEnvironment,
	logger *log.Entry,
) (Bridge, error) {
	hasRCON := false
	for _, f := range features {
		if f == FeatureMinecraftRCON {
			hasRCON = true
			break
		}
	}

	if !hasRCON {
		return nil, nil
	}

	propsPath := filepath.Join(serverRoot, "server.properties")
	markerPath := filepath.Join(serverRoot, markerFile)

	// Check if server.properties exists yet (first start may not have it).
	if _, err := os.Stat(propsPath); os.IsNotExist(err) {
		logger.Debug("server.properties not found, skipping RCON auto-config")
		return NewConsoleBridge(env, logger), nil
	}

	props, err := properties.LoadFile(propsPath, properties.UTF8)
	if err != nil {
		logger.WithError(err).Warn("failed to read server.properties for RCON config")
		return NewConsoleBridge(env, logger), nil
	}

	rconEnabled := props.GetBool("enable-rcon", false)

	if !rconEnabled {
		// Check if we previously auto-enabled and the user disabled it.
		if _, err := os.Stat(markerPath); err == nil {
			// Marker exists but RCON is disabled - user explicitly turned it off.
			logger.Info("RCON was auto-enabled but user disabled it, using console fallback")
			return NewConsoleBridge(env, logger), nil
		}

		// Auto-enable RCON.
		logger.Info("auto-enabling RCON in server.properties")
		password, err := generatePassword(16)
		if err != nil {
			logger.WithError(err).Warn("failed to generate RCON password")
			return NewConsoleBridge(env, logger), nil
		}

		props.Set("enable-rcon", "true")
		props.Set("rcon.port", "25575")
		props.Set("rcon.password", password)

		f, err := os.OpenFile(propsPath, os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			logger.WithError(err).Warn("failed to open server.properties for writing")
			return NewConsoleBridge(env, logger), nil
		}
		if _, err := props.Write(f, properties.UTF8); err != nil {
			f.Close()
			logger.WithError(err).Warn("failed to write server.properties")
			return NewConsoleBridge(env, logger), nil
		}
		f.Close()
		// Ensure restrictive permissions (RCON password is in this file).
		os.Chmod(propsPath, 0600)

		// Write marker file.
		if err := os.WriteFile(markerPath, []byte("auto-configured by elytra"), 0644); err != nil {
			logger.WithError(err).Warn("failed to write RCON marker file")
		}

		logger.Info("RCON auto-configured successfully")
	}

	return NewRCONBridge(propsPath, logger), nil
}

// generatePassword creates a random hex password of the given byte length.
func generatePassword(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random password: %w", err)
	}
	return hex.EncodeToString(b), nil
}

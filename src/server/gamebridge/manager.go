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

// EnsureRCONConfig ensures RCON is enabled in server.properties for servers
// with the minecraft_rcon egg feature. Call this before the game container
// starts so the server process reads the RCON configuration on boot.
//
// Bridge creation is handled separately by ConfigureAndCreate, which should
// be called after the container starts (once the container's bridge network
// IP is available).
func EnsureRCONConfig(features []string, serverRoot string, logger *log.Entry) {
	if !hasFeature(features, FeatureMinecraftRCON) {
		return
	}

	propsPath := filepath.Join(serverRoot, "server.properties")
	markerPath := filepath.Join(serverRoot, markerFile)

	if _, err := os.Stat(propsPath); os.IsNotExist(err) {
		logger.Debug("server.properties not found, skipping RCON pre-config")
		return
	}

	props, err := properties.LoadFile(propsPath, properties.UTF8)
	if err != nil {
		logger.WithError(err).Warn("failed to read server.properties for RCON pre-config")
		return
	}

	if props.GetBool("enable-rcon", false) {
		return // RCON already enabled.
	}

	// If we previously auto-enabled and the user disabled it, respect that.
	if _, err := os.Stat(markerPath); err == nil {
		return
	}

	// Auto-enable RCON.
	logger.Info("auto-enabling RCON in server.properties")
	password, err := generatePassword(16)
	if err != nil {
		logger.WithError(err).Warn("failed to generate RCON password")
		return
	}

	props.Set("enable-rcon", "true")
	props.Set("rcon.port", "25575")
	props.Set("rcon.password", password)

	f, err := os.OpenFile(propsPath, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		logger.WithError(err).Warn("failed to open server.properties for writing")
		return
	}
	if _, err := props.Write(f, properties.UTF8); err != nil {
		f.Close()
		logger.WithError(err).Warn("failed to write server.properties")
		return
	}
	f.Close()
	os.Chmod(propsPath, 0600)

	if err := os.WriteFile(markerPath, []byte("auto-configured by elytra"), 0644); err != nil {
		logger.WithError(err).Warn("failed to write RCON marker file")
	}

	logger.Info("RCON auto-configured successfully")
}

// ConfigureAndCreate inspects the server's egg features and filesystem to
// determine which bridge to create. Returns nil if no bridge applies.
//
// This should be called after the game container is running, so that
// containerIP (the container's IP on the Docker bridge network) is available
// for RCON connections. RCON auto-configuration in server.properties is
// handled by EnsureRCONConfig, which runs before the container starts.
func ConfigureAndCreate(
	features []string,
	serverRoot string,
	env environment.ProcessEnvironment,
	containerIP string,
	logger *log.Entry,
) (Bridge, error) {
	if !hasFeature(features, FeatureMinecraftRCON) {
		return nil, nil
	}

	propsPath := filepath.Join(serverRoot, "server.properties")

	if _, err := os.Stat(propsPath); os.IsNotExist(err) {
		logger.Debug("server.properties not found, using console fallback")
		return NewConsoleBridge(env, logger), nil
	}

	props, err := properties.LoadFile(propsPath, properties.UTF8)
	if err != nil {
		logger.WithError(err).Warn("failed to read server.properties")
		return NewConsoleBridge(env, logger), nil
	}

	if !props.GetBool("enable-rcon", false) {
		return NewConsoleBridge(env, logger), nil
	}

	return NewRCONBridge(propsPath, containerIP, logger), nil
}

// hasFeature checks whether a feature flag is present in the list.
func hasFeature(features []string, target string) bool {
	for _, f := range features {
		if f == target {
			return true
		}
	}
	return false
}

// generatePassword creates a random hex password of the given byte length.
func generatePassword(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random password: %w", err)
	}
	return hex.EncodeToString(b), nil
}

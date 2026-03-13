package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pyrohost/elytra/src/router/middleware"
	"github.com/pyrohost/elytra/src/server/backup"
)

// getArchiveSnapshots lists all archive snapshots for a server.
// Spec: GET /api/archives?server={uuid} -> synchronous list
func getArchiveSnapshots(c *gin.Context) {
	serverUUID := c.Query("server")
	if serverUUID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "server query parameter is required"})
		return
	}

	manager := middleware.ExtractManager(c)
	s, exists := manager.Get(serverUUID)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
		return
	}

	client := middleware.ExtractApiClient(c)
	ctx := c.Request.Context()

	rusticConfig, err := client.GetServerRusticConfig(ctx, s.ID(), "local")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to get rustic config: " + err.Error(),
		})
		return
	}

	adapter := backup.NewRusticWithServerPath(
		client, s.ID(), "", "", "local", nil,
		rusticConfig.RepositoryPassword, rusticConfig.RepositoryPath,
	)

	repo := adapter.GetRepository()
	snapshots, err := repo.ListSnapshots(ctx, map[string]string{
		"server": serverUUID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to list snapshots: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": snapshots})
}

// deleteArchiveSnapshot deletes a specific archive snapshot.
// Spec: DELETE /api/archives/{snapshot_id} -> 202 { job_id }
func deleteArchiveSnapshot(c *gin.Context) {
	snapshotID := c.Param("snapshot_id")
	serverUUID := c.Query("server")

	if snapshotID == "" || serverUUID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "snapshot_id path param and server query param are required",
		})
		return
	}

	manager := middleware.ExtractJobManager(c)

	jobData := map[string]interface{}{
		"server_id":   serverUUID,
		"snapshot_id": snapshotID,
	}

	jobID, err := manager.CreateJob("archive_delete", jobData)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to submit delete job: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"job_id":  jobID,
		"status":  "accepted",
		"message": "Snapshot deletion queued",
	})
}

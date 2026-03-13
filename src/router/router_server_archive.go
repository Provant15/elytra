package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pyrohost/elytra/src/router/middleware"
)

// postServerArchive initiates an archive operation for a server.
// Spec: POST /api/servers/{uuid}/archive -> 202 { job_id }
func postServerArchive(c *gin.Context) {
	s := middleware.ExtractServer(c)
	manager := middleware.ExtractJobManager(c)

	var data struct {
		Excludes string `json:"excludes"`
	}
	if err := c.BindJSON(&data); err != nil {
		return
	}

	jobData := map[string]interface{}{
		"server_id": s.ID(),
		"excludes":  data.Excludes,
	}

	jobID, err := manager.CreateJob("archive_create", jobData)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to submit archive job: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"job_id":  jobID,
		"status":  "accepted",
		"message": "Archive job has been queued for processing",
	})
}

// postServerRestore initiates the overlay phase of a two-phase restore.
// Spec: POST /api/servers/{uuid}/restore -> 202 { job_id }
func postServerRestore(c *gin.Context) {
	s := middleware.ExtractServer(c)
	manager := middleware.ExtractJobManager(c)

	var data struct {
		SnapshotID string `json:"snapshot_id" binding:"required"`
	}
	if err := c.BindJSON(&data); err != nil {
		return
	}

	jobData := map[string]interface{}{
		"server_id":   s.ID(),
		"snapshot_id": data.SnapshotID,
	}

	jobID, err := manager.CreateJob("archive_restore", jobData)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to submit restore job: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"job_id":  jobID,
		"status":  "accepted",
		"message": "Archive restore job has been queued for processing",
	})
}

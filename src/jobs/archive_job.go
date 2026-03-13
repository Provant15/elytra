package jobs

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/google/uuid"
	"github.com/pyrohost/elytra/src/remote"
	"github.com/pyrohost/elytra/src/server"
	"github.com/pyrohost/elytra/src/server/backup"
)

// ArchiveCreateJob creates a Rustic snapshot of a server's user data,
// excluding files provided by the egg install script. The exclude patterns
// come from the egg's archive_excludes configuration, passed as a
// newline-separated string.
type ArchiveCreateJob struct {
	id            string
	serverID      string
	excludes      string
	progress      int
	message       string
	serverManager *server.Manager
	client        remote.Client
}

// NewArchiveCreateJob creates a new archive creation job.
func NewArchiveCreateJob(data map[string]interface{}, serverManager *server.Manager, client remote.Client) (Job, error) {
	serverID, ok := data["server_id"].(string)
	if !ok || serverID == "" {
		return nil, fmt.Errorf("server_id is required")
	}

	excludes, _ := data["excludes"].(string)

	return &ArchiveCreateJob{
		id:            uuid.New().String(),
		serverID:      serverID,
		excludes:      excludes,
		progress:      0,
		message:       "Archive creation queued",
		serverManager: serverManager,
		client:        client,
	}, nil
}

func (j *ArchiveCreateJob) GetID() string      { return j.id }
func (j *ArchiveCreateJob) GetType() string     { return "archive_create" }
func (j *ArchiveCreateJob) GetProgress() int    { return j.progress }
func (j *ArchiveCreateJob) GetMessage() string  { return j.message }
func (j *ArchiveCreateJob) GetServerID() string { return j.serverID }

// GetWebSocketEventType returns the WebSocket event type for archive operations.
func (j *ArchiveCreateJob) GetWebSocketEventType() string { return "archive.status" }

// GetWebSocketEventData returns job-specific data for WebSocket events.
func (j *ArchiveCreateJob) GetWebSocketEventData() map[string]interface{} {
	return map[string]interface{}{"operation": "create"}
}

// Validate ensures the job data is valid before execution.
func (j *ArchiveCreateJob) Validate() error {
	if j.serverID == "" {
		return fmt.Errorf("server_id is required")
	}
	return nil
}

// Execute stops the server, creates a Rustic snapshot using the existing
// backup infrastructure, and reports the snapshot details.
func (j *ArchiveCreateJob) Execute(ctx context.Context, reporter ProgressReporter) (interface{}, error) {
	logger := log.WithFields(log.Fields{
		"job_id":    j.id,
		"server_id": j.serverID,
	})

	logger.Info("starting archive creation")

	s, exists := j.serverManager.Get(j.serverID)
	if !exists {
		return nil, fmt.Errorf("server %s not found", j.serverID)
	}

	// Acquire backup lock to prevent concurrent backup/archive operations
	j.serverManager.AcquireBackupLock(j.serverID)
	defer j.serverManager.ReleaseBackupLock(j.serverID)

	// Step 1: Stop the server
	reporter.ReportProgress(10, "Stopping server...")
	if err := s.HandlePowerAction(server.PowerActionStop); err != nil {
		logger.WithField("error", err).Warn("failed to stop server, attempting kill")
		if err := s.HandlePowerAction(server.PowerActionTerminate); err != nil {
			return nil, errors.Wrap(err, "failed to stop server for archival")
		}
	}

	reporter.ReportProgress(20, "Waiting for server to stop...")
	if err := s.Environment.WaitForStop(ctx, time.Minute*5, false); err != nil {
		return nil, errors.Wrap(err, "timed out waiting for server to stop")
	}

	// Step 2: Get rustic config from Panel (password, repo path)
	reporter.ReportProgress(30, "Initializing archive...")
	rusticConfig, err := j.client.GetServerRusticConfig(ctx, s.ID(), "local")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get server rustic config")
	}

	// Step 3: Create rustic adapter using the correct factory pattern
	archiveUUID := uuid.New().String()
	adapter := backup.NewRusticWithServerPath(
		j.client,
		s.ID(),
		archiveUUID,
		j.excludes,
		"local",
		nil, // s3Creds (nil for local)
		rusticConfig.RepositoryPassword,
		rusticConfig.RepositoryPath,
	)

	reporter.ReportProgress(50, "Running rustic backup...")

	// Generate() takes the server's filesystem and ignore patterns
	details, err := adapter.Generate(ctx, s.Filesystem(), j.excludes)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create archive snapshot")
	}

	reporter.ReportProgress(90, "Archive snapshot created")

	result := map[string]interface{}{
		"successful":  true,
		"snapshot_id": details.SnapshotId,
		"size":        details.Size,
		"checksum":    details.Checksum,
	}

	logger.WithFields(log.Fields{
		"snapshot_id": details.SnapshotId,
		"size":        details.Size,
	}).Info("archive creation completed")

	return result, nil
}

// ArchiveRestoreJob restores user data from a Rustic snapshot onto a
// server's filesystem. This is Phase 2 (overlay) of a two-phase restore.
// It assumes the egg install script has already run (Phase 1).
type ArchiveRestoreJob struct {
	id            string
	serverID      string
	snapshotID    string
	progress      int
	message       string
	serverManager *server.Manager
	client        remote.Client
}

// NewArchiveRestoreJob creates a new archive restore (overlay) job.
func NewArchiveRestoreJob(data map[string]interface{}, serverManager *server.Manager, client remote.Client) (Job, error) {
	serverID, ok := data["server_id"].(string)
	if !ok || serverID == "" {
		return nil, fmt.Errorf("server_id is required")
	}

	snapshotID, ok := data["snapshot_id"].(string)
	if !ok || snapshotID == "" {
		return nil, fmt.Errorf("snapshot_id is required")
	}

	return &ArchiveRestoreJob{
		id:            uuid.New().String(),
		serverID:      serverID,
		snapshotID:    snapshotID,
		progress:      0,
		message:       "Archive restore queued",
		serverManager: serverManager,
		client:        client,
	}, nil
}

func (j *ArchiveRestoreJob) GetID() string      { return j.id }
func (j *ArchiveRestoreJob) GetType() string     { return "archive_restore" }
func (j *ArchiveRestoreJob) GetProgress() int    { return j.progress }
func (j *ArchiveRestoreJob) GetMessage() string  { return j.message }
func (j *ArchiveRestoreJob) GetServerID() string { return j.serverID }

// GetWebSocketEventType returns the WebSocket event type for archive operations.
func (j *ArchiveRestoreJob) GetWebSocketEventType() string { return "archive.status" }

// GetWebSocketEventData returns job-specific data for WebSocket events.
func (j *ArchiveRestoreJob) GetWebSocketEventData() map[string]interface{} {
	return map[string]interface{}{"operation": "restore", "snapshot_id": j.snapshotID}
}

// Validate ensures the job data is valid before execution.
func (j *ArchiveRestoreJob) Validate() error {
	if j.serverID == "" || j.snapshotID == "" {
		return fmt.Errorf("server_id and snapshot_id are required")
	}
	return nil
}

// Execute restores archived user data from a Rustic snapshot onto the
// server's filesystem, then starts the server.
func (j *ArchiveRestoreJob) Execute(ctx context.Context, reporter ProgressReporter) (interface{}, error) {
	logger := log.WithFields(log.Fields{
		"job_id":      j.id,
		"server_id":   j.serverID,
		"snapshot_id": j.snapshotID,
	})

	logger.Info("starting archive restore (overlay phase)")

	s, exists := j.serverManager.Get(j.serverID)
	if !exists {
		return nil, fmt.Errorf("server %s not found", j.serverID)
	}

	// Acquire backup lock to prevent concurrent operations
	j.serverManager.AcquireBackupLock(j.serverID)
	defer j.serverManager.ReleaseBackupLock(j.serverID)

	// Get rustic config from Panel
	reporter.ReportProgress(10, "Initializing restore...")
	rusticConfig, err := j.client.GetServerRusticConfig(ctx, s.ID(), "local")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get server rustic config")
	}

	// Locate the snapshot using the correct factory function
	reporter.ReportProgress(30, "Locating archive snapshot...")
	adapter, err := backup.LocateRusticBySnapshotID(
		j.client,
		s.ID(),
		j.snapshotID,
		"local",
		nil, // s3Creds
		rusticConfig.RepositoryPassword,
		rusticConfig.RepositoryPath,
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to locate archive snapshot")
	}

	// RestoreSnapshot(ctx, snapshotID, targetPath, sourcePath)
	reporter.ReportProgress(50, "Restoring archived data...")
	repo := adapter.GetRepository()
	serverPath := s.Filesystem().Path()
	if err := repo.RestoreSnapshot(ctx, j.snapshotID, serverPath, "/"); err != nil {
		return nil, errors.Wrap(err, "failed to restore archive snapshot")
	}

	reporter.ReportProgress(80, "Starting server...")
	if err := s.HandlePowerAction(server.PowerActionStart); err != nil {
		logger.WithField("error", err).Warn("failed to start server after restore")
	}

	reporter.ReportProgress(100, "Archive restore completed")

	logger.Info("archive restore completed successfully")
	return map[string]interface{}{
		"successful":  true,
		"snapshot_id": j.snapshotID,
	}, nil
}

// ArchiveDeleteJob deletes a Rustic snapshot from the repository using
// Repository.DeleteSnapshot().
type ArchiveDeleteJob struct {
	id            string
	serverID      string
	snapshotID    string
	progress      int
	message       string
	serverManager *server.Manager
	client        remote.Client
}

// NewArchiveDeleteJob creates a new archive deletion job.
func NewArchiveDeleteJob(data map[string]interface{}, serverManager *server.Manager, client remote.Client) (Job, error) {
	serverID, ok := data["server_id"].(string)
	if !ok || serverID == "" {
		return nil, fmt.Errorf("server_id is required")
	}

	snapshotID, ok := data["snapshot_id"].(string)
	if !ok || snapshotID == "" {
		return nil, fmt.Errorf("snapshot_id is required")
	}

	return &ArchiveDeleteJob{
		id:            uuid.New().String(),
		serverID:      serverID,
		snapshotID:    snapshotID,
		progress:      0,
		message:       "Archive deletion queued",
		serverManager: serverManager,
		client:        client,
	}, nil
}

func (j *ArchiveDeleteJob) GetID() string      { return j.id }
func (j *ArchiveDeleteJob) GetType() string     { return "archive_delete" }
func (j *ArchiveDeleteJob) GetProgress() int    { return j.progress }
func (j *ArchiveDeleteJob) GetMessage() string  { return j.message }
func (j *ArchiveDeleteJob) GetServerID() string { return j.serverID }

// GetWebSocketEventType returns the WebSocket event type for archive operations.
func (j *ArchiveDeleteJob) GetWebSocketEventType() string { return "archive.status" }

// GetWebSocketEventData returns job-specific data for WebSocket events.
func (j *ArchiveDeleteJob) GetWebSocketEventData() map[string]interface{} {
	return map[string]interface{}{"operation": "delete", "snapshot_id": j.snapshotID}
}

// Validate ensures the job data is valid before execution.
func (j *ArchiveDeleteJob) Validate() error {
	if j.snapshotID == "" {
		return fmt.Errorf("snapshot_id is required")
	}
	return nil
}

// Execute deletes the specified snapshot using Repository.DeleteSnapshot().
func (j *ArchiveDeleteJob) Execute(ctx context.Context, reporter ProgressReporter) (interface{}, error) {
	logger := log.WithFields(log.Fields{
		"job_id":      j.id,
		"server_id":   j.serverID,
		"snapshot_id": j.snapshotID,
	})

	logger.Info("starting archive snapshot deletion")

	s, exists := j.serverManager.Get(j.serverID)
	if !exists {
		return nil, fmt.Errorf("server %s not found", j.serverID)
	}

	// Get rustic config from Panel
	reporter.ReportProgress(20, "Initializing...")
	rusticConfig, err := j.client.GetServerRusticConfig(ctx, s.ID(), "local")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get server rustic config")
	}

	// Locate the snapshot
	adapter, err := backup.LocateRusticBySnapshotID(
		j.client,
		s.ID(),
		j.snapshotID,
		"local",
		nil,
		rusticConfig.RepositoryPassword,
		rusticConfig.RepositoryPath,
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to locate snapshot for deletion")
	}

	reporter.ReportProgress(50, "Deleting snapshot...")
	repo := adapter.GetRepository()
	if err := repo.DeleteSnapshot(ctx, j.snapshotID); err != nil {
		return nil, errors.Wrap(err, "failed to delete snapshot")
	}

	reporter.ReportProgress(100, "Snapshot deleted")
	logger.Info("archive snapshot deletion completed")

	return map[string]interface{}{"successful": true}, nil
}

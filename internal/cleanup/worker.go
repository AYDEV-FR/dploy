package cleanup

import (
	"context"
	"time"

	"github.com/AYDEV-FR/dploy/internal/kube"
	"github.com/AYDEV-FR/dploy/internal/logger"
)

// Worker handles TTL-based cleanup of expired environments.
type Worker struct {
	kubeClient *kube.Client
	interval   time.Duration
}

// NewWorker creates a new cleanup worker.
func NewWorker(kubeClient *kube.Client, intervalSeconds int) *Worker {
	return &Worker{
		kubeClient: kubeClient,
		interval:   time.Duration(intervalSeconds) * time.Second,
	}
}

// Start begins the cleanup worker loop.
func (w *Worker) Start(ctx context.Context) {
	logger.Info("Starting TTL cleanup worker", "interval", w.interval)

	// Run immediately on start
	w.cleanupExpired(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Cleanup worker stopped")
			return
		case <-ticker.C:
			w.cleanupExpired(ctx)
		}
	}
}

// cleanupExpired finds and deletes all expired environments.
func (w *Worker) cleanupExpired(ctx context.Context) {
	apps, err := w.kubeClient.ListAllDployApplications(ctx)
	if err != nil {
		logger.Error("Cleanup worker: failed to list applications", "error", err)
		return
	}

	now := time.Now()
	expiredCount := 0

	for _, app := range apps.Items {
		annotations := app.GetAnnotations()
		expiresAtStr, ok := annotations["dploy.dev/expires-at"]
		if !ok {
			continue
		}

		expiresAt, err := time.Parse(time.RFC3339, expiresAtStr)
		if err != nil {
			logger.Error("Cleanup worker: failed to parse expires-at", "app", app.GetName(), "error", err)
			continue
		}

		if now.After(expiresAt) {
			appName := app.GetName()
			logger.Info("Cleanup worker: deleting expired environment", "app", appName, "expiredAt", expiresAtStr)

			if err := w.kubeClient.DeleteApplication(ctx, appName); err != nil {
				logger.Error("Cleanup worker: failed to delete application", "app", appName, "error", err)
				continue
			}

			expiredCount++
			logger.Info("Cleanup worker: successfully deleted application", "app", appName)
		}
	}

	if expiredCount > 0 {
		logger.Info("Cleanup worker: cleanup cycle completed", "deletedCount", expiredCount)
	}
}

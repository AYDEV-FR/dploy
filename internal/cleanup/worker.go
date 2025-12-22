package cleanup

import (
	"context"
	"log"
	"time"

	"github.com/AYDEV-FR/dploy/internal/kube"
)

// Worker handles TTL-based cleanup of expired environments
type Worker struct {
	kubeClient *kube.Client
	interval   time.Duration
}

// NewWorker creates a new cleanup worker
func NewWorker(kubeClient *kube.Client, intervalSeconds int) *Worker {
	return &Worker{
		kubeClient: kubeClient,
		interval:   time.Duration(intervalSeconds) * time.Second,
	}
}

// Start begins the cleanup worker loop
func (w *Worker) Start(ctx context.Context) {
	log.Printf("Starting TTL cleanup worker with interval: %v", w.interval)

	// Run immediately on start
	w.cleanupExpired(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Cleanup worker stopped")
			return
		case <-ticker.C:
			w.cleanupExpired(ctx)
		}
	}
}

// cleanupExpired finds and deletes all expired environments
func (w *Worker) cleanupExpired(ctx context.Context) {
	apps, err := w.kubeClient.ListAllDployApplications(ctx)
	if err != nil {
		log.Printf("Cleanup worker: failed to list applications: %v", err)
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
			log.Printf("Cleanup worker: failed to parse expires-at for %s: %v", app.GetName(), err)
			continue
		}

		if now.After(expiresAt) {
			appName := app.GetName()
			log.Printf("Cleanup worker: deleting expired environment %s (expired at %s)", appName, expiresAtStr)

			if err := w.kubeClient.DeleteApplication(ctx, appName); err != nil {
				log.Printf("Cleanup worker: failed to delete %s: %v", appName, err)
				continue
			}

			expiredCount++
			log.Printf("Cleanup worker: successfully deleted %s", appName)
		}
	}

	if expiredCount > 0 {
		log.Printf("Cleanup worker: deleted %d expired environment(s)", expiredCount)
	}
}

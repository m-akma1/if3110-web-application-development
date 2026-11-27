package migration

import (
	"context"
	"testing"
	"time"
)

// RunJobs processes due jobs with a worker until the queue has none left, the way production
// runs them. Tests in package migration_test call it after an Enqueue* call.
func RunJobs(t testing.TB, repo *Repository, engine *Engine) {
	t.Helper()
	w := NewWorker(repo, engine, WorkerConfig{
		Concurrency: 1, PollInterval: 20 * time.Millisecond, Lease: 5 * time.Second,
		BaseBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond, ReapInterval: time.Hour,
	})
	for i := 0; i < 20; i++ {
		worked, err := w.RunOnce(context.Background(), "test-worker")
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if !worked {
			return
		}
	}
	t.Fatal("RunJobs: the queue did not drain")
}

// ExecuteNow is the queued path of a migration run: EnqueueExecute, then RunJobs. It returns
// the migration as stored afterwards (SUCCESS, FAILED, ...).
func ExecuteNow(t testing.TB, svc *Service, userID, id string) (*Migration, error) {
	t.Helper()
	if _, _, err := svc.EnqueueExecute(context.Background(), userID, id); err != nil {
		return nil, err
	}
	RunJobs(t, svc.repo, svc.engine)
	return svc.repo.Get(context.Background(), id)
}

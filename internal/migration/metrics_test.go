package migration

import (
	"context"
	"testing"

	"dbplatform/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// histogramCount is the number of observations of one series of a histogram vector.
func histogramCount(t *testing.T, h *prometheus.HistogramVec, labels ...string) uint64 {
	t.Helper()
	var m dto.Metric
	if err := h.WithLabelValues(labels...).(prometheus.Metric).Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram().GetSampleCount()
}

func TestWorkerRecordsJobMetrics(t *testing.T) {
	f := newFixture(t, Limits{})
	enqueued := testutil.ToFloat64(metrics.JobsEnqueued.WithLabelValues("EXECUTE"))
	finished := testutil.ToFloat64(metrics.JobsFinished.WithLabelValues("EXECUTE", "SUCCESS"))
	waits := histogramCount(t, metrics.QueueWait, "EXECUTE")
	runs := histogramCount(t, metrics.RunDuration, "EXECUTE", "success")
	locks := histogramCount(t, metrics.LockWait, "acquired")

	f.enqueue(t, f.draft(t, "orders", "CREATE TABLE orders (id int)"))
	if _, err := fastWorker(f).RunOnce(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}

	if d := testutil.ToFloat64(metrics.JobsEnqueued.WithLabelValues("EXECUTE")) - enqueued; d != 1 {
		t.Errorf("jobs_enqueued_total grew by %v, want 1", d)
	}
	if d := testutil.ToFloat64(metrics.JobsFinished.WithLabelValues("EXECUTE", "SUCCESS")) - finished; d != 1 {
		t.Errorf("jobs_finished_total{SUCCESS} grew by %v, want 1", d)
	}
	if histogramCount(t, metrics.QueueWait, "EXECUTE") != waits+1 ||
		histogramCount(t, metrics.RunDuration, "EXECUTE", "success") != runs+1 ||
		histogramCount(t, metrics.LockWait, "acquired") != locks+1 {
		t.Error("queue wait, run duration and lock wait must each get one observation")
	}
}

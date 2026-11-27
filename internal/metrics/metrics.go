// Package metrics holds the platform's Prometheus metrics. They are served on a separate,
// internal address (METRICS_ADDR), never through Caddy.
//
// Labels are low-cardinality on purpose: job type, status, direction, route and pool name.
// User IDs, job IDs, migration IDs and SQL never appear in labels; they belong in logs and traces.
package metrics

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "dbplatform"

// Registry has every platform metric plus the Go runtime and process collectors.
var Registry = prometheus.NewRegistry()

var (
	// JobsEnqueued counts jobs added to the queue.
	JobsEnqueued = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "jobs_enqueued_total",
		Help: "Migration jobs added to the queue, by job type.",
	}, []string{"job_type"})

	// JobsFinished counts jobs that reached a final state.
	JobsFinished = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "jobs_finished_total",
		Help: "Migration jobs that ended, by job type and final status (SUCCESS, FAILED, PARTIAL, UNCERTAIN).",
	}, []string{"job_type", "status"})

	// JobRetries counts jobs put back in the queue.
	JobRetries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "job_retries_total",
		Help: "Jobs put back in the queue, by job type and reason (transient, busy, interrupted, recovered).",
	}, []string{"job_type", "reason"})

	// QueueWait is how long a job waited between becoming due and being claimed.
	QueueWait = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "job_queue_wait_seconds",
		Help:    "Time from a job becoming due (queued or retry time) to a worker claiming it.",
		Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	}, []string{"job_type"})

	// LockWait is how long a run waited for its database's advisory lock.
	LockWait = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "migration_lock_wait_seconds",
		Help:    "Time spent waiting for the per-database advisory lock, by outcome (acquired, busy, error).",
		Buckets: []float64{.001, .005, .01, .05, .1, .25, .5, 1, 2.5, 5, 10, 15, 30},
	}, []string{"outcome"})

	// RunDuration is how long the engine worked on one job attempt.
	RunDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "migration_run_seconds",
		Help:    "Duration of one job attempt in the engine (lock wait included), by job type and outcome.",
		Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600},
	}, []string{"job_type", "outcome"})

	// Batches counts committed batches; BatchRows the rows they changed.
	Batches = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "migration_batches_total",
		Help: "Committed batches of batch migrations, by direction (UP, DOWN).",
	}, []string{"direction"})
	BatchRows = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "migration_batch_rows_total",
		Help: "Rows changed by committed batches, by direction.",
	}, []string{"direction"})
	BatchDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "migration_batch_seconds",
		Help:    "Duration of one committed batch transaction, by direction.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"direction"})

	// HTTPRequests and HTTPDuration describe the API by route pattern, never by raw path.
	HTTPRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "http_requests_total",
		Help: "HTTP requests by route pattern, method and status code class (2xx, 4xx, 5xx).",
	}, []string{"route", "method", "code"})
	HTTPDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "http_request_duration_seconds",
		Help:    "HTTP request duration by route pattern and method.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"route", "method"})
)

func init() {
	Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		JobsEnqueued, JobsFinished, JobRetries, QueueWait, LockWait, RunDuration,
		Batches, BatchRows, BatchDuration, HTTPRequests, HTTPDuration,
	)
}

// Handler serves the registry in the Prometheus text format.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Registry: Registry})
}

// Serve listens on addr and serves /metrics until ctx is cancelled. It returns at once when
// addr is empty.
func Serve(ctx context.Context, addr string) error {
	if addr == "" {
		return nil
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", Handler())
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// CodeClass turns 404 into "4xx".
func CodeClass(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	default:
		return "2xx"
	}
}

// RegisterJobGauge reports the number of jobs per status, read from migration_jobs at scrape
// time (one small GROUP BY), so the gauge is right even after a restart.
func RegisterJobGauge(db *sql.DB) {
	Registry.MustRegister(&jobCollector{db: db, desc: prometheus.NewDesc(
		namespace+"_jobs", "Migration jobs in the queue table, by status.", []string{"status"}, nil)})
}

type jobCollector struct {
	db   *sql.DB
	desc *prometheus.Desc
}

func (c *jobCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *jobCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	counts := map[string]float64{"QUEUED": 0, "RUNNING": 0, "SUCCESS": 0, "FAILED": 0, "PARTIAL": 0, "UNCERTAIN": 0}
	rows, err := c.db.QueryContext(ctx, `SELECT status, count(*) FROM migration_jobs GROUP BY status`)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(c.desc, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n float64
		if err := rows.Scan(&status, &n); err != nil {
			ch <- prometheus.NewInvalidMetric(c.desc, err)
			return
		}
		counts[status] = n
	}
	for status, n := range counts {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, n, status)
	}
}

// Pool is one connection pool to report: the platform database or a target, by name.
type Pool struct {
	Name  string
	Stats sql.DBStats
}

// RegisterPools reports sql.DB.Stats() of every pool list returns, labelled by pool name.
func RegisterPools(list func() []Pool) {
	label := []string{"pool"}
	d := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(namespace+"_db_pool_"+name, help, label, nil)
	}
	Registry.MustRegister(&poolCollector{list: list, descs: poolDescs{
		open:     d("open_connections", "Open connections (in use + idle)."),
		inUse:    d("in_use_connections", "Connections in use."),
		idle:     d("idle_connections", "Idle connections."),
		maxOpen:  d("max_open_connections", "Configured maximum of open connections (0 = unlimited)."),
		waits:    d("wait_count_total", "Times a caller had to wait for a free connection."),
		waitSecs: d("wait_seconds_total", "Total time callers waited for a free connection."),
	}})
}

type poolDescs struct{ open, inUse, idle, maxOpen, waits, waitSecs *prometheus.Desc }

type poolCollector struct {
	list  func() []Pool
	descs poolDescs
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.descs.open, c.descs.inUse, c.descs.idle, c.descs.maxOpen, c.descs.waits, c.descs.waitSecs} {
		ch <- d
	}
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	for _, p := range c.list() {
		s := p.Stats
		ch <- prometheus.MustNewConstMetric(c.descs.open, prometheus.GaugeValue, float64(s.OpenConnections), p.Name)
		ch <- prometheus.MustNewConstMetric(c.descs.inUse, prometheus.GaugeValue, float64(s.InUse), p.Name)
		ch <- prometheus.MustNewConstMetric(c.descs.idle, prometheus.GaugeValue, float64(s.Idle), p.Name)
		ch <- prometheus.MustNewConstMetric(c.descs.maxOpen, prometheus.GaugeValue, float64(s.MaxOpenConnections), p.Name)
		ch <- prometheus.MustNewConstMetric(c.descs.waits, prometheus.CounterValue, float64(s.WaitCount), p.Name)
		ch <- prometheus.MustNewConstMetric(c.descs.waitSecs, prometheus.CounterValue, s.WaitDuration.Seconds(), p.Name)
	}
}

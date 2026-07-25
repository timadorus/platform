package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTPRequestDuration covers both command-api and query-api (label "service"
	// distinguishes them); "route" is the matched mux path *template*
	// (e.g. "/universes/{universeId}"), never the raw path, to keep cardinality bounded.
	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "timadorus_http_request_duration_seconds",
		Help:    "HTTP request duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"service", "method", "route", "status"})

	// EventAppendDuration times internal/eventstore/postgres.Store.Append calls.
	EventAppendDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "timadorus_event_append_duration_seconds",
		Help:    "Duration of EventStore.Append calls in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"aggregate_type"})

	// OutboxPublishLagSeconds observes, per published row, the time between the event's
	// creation (events.created_at) and the outbox relay successfully publishing it to NATS.
	OutboxPublishLagSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "timadorus_outbox_publish_lag_seconds",
		Help:    "Time between an event being appended and its outbox row being published to NATS.",
		Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	})

	// ProjectionLagSeconds observes, per successfully-applied event, the time between the
	// event's creation and this projection catching up to it — the same
	// eventual-consistency window plan §7 documents, now measurable.
	ProjectionLagSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "timadorus_projection_lag_seconds",
		Help:    "Time between an event being appended and a projection successfully applying it.",
		Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{"projection"})

	// ProjectionEventsTotal counts processing outcomes per projection: "ok" (applied),
	// "retry" (nacked, will be redelivered), "dead_letter" (gave up after max attempts —
	// see internal/projection.Router).
	ProjectionEventsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "timadorus_projection_events_total",
		Help: "Count of projection event processing outcomes.",
	}, []string{"projection", "result"})
)

// HTTPMetrics is HTTP middleware that records HTTPRequestDuration for every request. Mounted
// alongside RequestLogging in both command-api and query-api.
func HTTPMetrics(service string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(sw, r)
			HTTPRequestDuration.WithLabelValues(service, r.Method, routeTemplate(r), strconv.Itoa(sw.status)).
				Observe(time.Since(start).Seconds())
		})
	}
}

func routeTemplate(r *http.Request) string {
	if route := mux.CurrentRoute(r); route != nil {
		if tpl, err := route.GetPathTemplate(); err == nil {
			return tpl
		}
	}
	return "unmatched"
}

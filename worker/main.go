package main

import (
	"context"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

const jobQueue = "obs_jobs"

var (
	jobs = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lab_worker_jobs_total",
		Help: "Jobs completed",
	})
	jobErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lab_worker_job_errors_total",
		Help: "Job processing failures",
	})
	jobLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "lab_worker_job_duration_seconds",
		Help:    "Simulated work duration",
		Buckets: []float64{0.01, 0.03, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
	})
)

func main() {
	prometheus.MustRegister(jobs, jobErrors, jobLatency)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	server := &http.Server{Addr: ":9101", Handler: mux}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server: %v", err)
		}
	}()

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://127.0.0.1:6379/0"
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("invalid REDIS_URL: %v", err)
	}
	client := redis.NewClient(options)
	defer client.Close()

	for ctx.Err() == nil {
		item, err := client.BRPop(ctx, 3*time.Second, jobQueue).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			jobErrors.Inc()
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if len(item) != 2 {
			jobErrors.Inc()
			continue
		}
		started := time.Now()
		select {
		case <-ctx.Done():
			break
		case <-time.After(15*time.Millisecond + time.Duration(rand.Float64()*120)*time.Millisecond):
		}
		jobLatency.Observe(time.Since(started).Seconds())
		jobs.Inc()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}

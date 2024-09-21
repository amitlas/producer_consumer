package utils

import (
    "fmt"
    "sync"
    "time"
    "context"
    "net/http"
    _ "net/http/pprof"

    "github.com/prometheus/client_golang/prometheus/promhttp"

    log "github.com/sirupsen/logrus"
)

// Register and run prometheus(blocking)
//
// Pamars:
// - config - monitoring configurations
// - registerCallbacks - callbacks func to register orinetheus data
func RunPrometheusServer(config *MontioringConfig, registerCallbacks func(), wg *sync.WaitGroup, terminate chan struct{}) {
    defer wg.Done()

    registerCallbacks()

    log.Info("Starting prometheus server")
    server := &http.Server{Addr: fmt.Sprintf(":%d", config.PrometheusPort)}

    http.Handle(config.PrometheusEndPoint, promhttp.Handler())
    go func() {
        err := http.ListenAndServe(fmt.Sprintf(":%d", config.PrometheusPort), nil)
        if (nil != err) {
            log.Fatalf("Failed to start Prometheus server: %v", err)
        }
    }()

    <-terminate
    log.Info("Terminating prometheus server")
    ctx, cancel := context.WithTimeout(context.Background(), 5 * time.Second)
    defer cancel()

    if err := server.Shutdown(ctx); err != nil {
        log.Errorf("Error shutting down Prometheus server: %v\n", err)
    }
}

// Register and run pprof server(blocking)
//
// Pamars:
// - config - monitoring configurations
func RunPprofServer(config *MontioringConfig, wg *sync.WaitGroup, terminate chan struct{}) {
    defer wg.Done()

    log.Infof("Starting pprof on :%d", config.ProfilingPort)
    server := &http.Server{Addr: fmt.Sprintf("0.0.0.0:%d", config.ProfilingPort)}

    go func() {
        if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("Error starting pprof server: %v\n", err)
        }
    }()

    <-terminate
    log.Info("Shutting down pprof server")
    ctx, cancel := context.WithTimeout(context.Background(), 5 * time.Second)
    defer cancel()

    if err := server.Shutdown(ctx); err != nil {
        log.Errorf("Error shutting down pprof server: %v\n", err)
    }
}

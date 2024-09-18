package utils

import (
    "fmt"
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
func RunPrometheusServer(config *MontioringConfig, registerCallbacks func()) {
    registerCallbacks()

    log.Info("Starting prometheus server")
    http.Handle(config.PrometheusEndPoint, promhttp.Handler())
    err := http.ListenAndServe(fmt.Sprintf(":%d", config.PrometheusPort), nil)
    if (nil != err) {
        log.Fatalf("Failed to start Prometheus server: %v", err)
    }
}

// Register and run pprof server(blocking)
//
// Pamars:
// - config - monitoring configurations
func RunPprofServer(config *MontioringConfig) {
    log.Infof("Starting pprof on :%d", config.ProfilingPort)
    log.Warning(http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", config.ProfilingPort), nil))
}

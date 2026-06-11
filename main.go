package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

// maxRequestBodyBytes caps the size of an incoming AdmissionReview body to
// guard against memory exhaustion from oversized or malicious requests. An
// AdmissionReview carries both the old and new object, and Grafana dashboards
// can be large, so the default is generous; it is configurable via the
// --max-request-body-bytes flag.
var maxRequestBodyBytes int64 = 16 << 20 // 16 MiB

// Possible values for the "outcome" metric label, covering every request path.
const (
	outcomeChanged   = "changed"   // GrafanaDashboard update with a real diff (allowed)
	outcomeUnchanged = "unchanged" // GrafanaDashboard update with no diff (denied)
	outcomeSkipped   = "skipped"   // request the webhook does not act on (passed through)
	outcomeError     = "error"     // request rejected before a decision could be made
)

var (
	// Histogram tracking the duration of every request, labeled by outcome.
	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "grafana_operator_webhook_request_duration_seconds",
			Help:    "Duration of requests to the webhook server in seconds, labeled by outcome.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"outcome"},
	)

	// Counter tracking every request handled by the webhook, labeled by outcome
	// (changed, unchanged, skipped, error).
	processedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "grafana_operator_webhook_processed_total",
			Help: "Total number of requests handled by the webhook, labeled by outcome.",
		},
		[]string{"outcome"},
	)
)

func init() {
	// Register the histogram and counter metrics
	prometheus.MustRegister(requestDuration)
	prometheus.MustRegister(processedTotal)

	log.SetFormatter(&log.JSONFormatter{})
	log.SetOutput(os.Stdout)
}

func handleAdmissionReview(w http.ResponseWriter, r *http.Request) {
	// Start measuring the request duration.
	start := time.Now()

	// Default to "error"; it is overwritten once a non-error outcome is
	// determined, so every early return is recorded without per-path
	// bookkeeping. The deferred call guarantees both metrics are emitted on
	// every code path.
	outcome := outcomeError
	defer func() {
		requestDuration.WithLabelValues(outcome).Observe(time.Since(start).Seconds())
		processedTotal.WithLabelValues(outcome).Inc()
	}()

	// The apiserver always sends admission requests as POST; reject anything else.
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var admissionReviewReq admissionv1.AdmissionReview
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusRequestEntityTooLarge)
		return
	}

	err = json.Unmarshal(body, &admissionReviewReq)
	if err != nil {
		http.Error(w, "failed to unmarshal request", http.StatusBadRequest)
		return
	}

	if admissionReviewReq.Request == nil {
		http.Error(w, "admission review request is empty", http.StatusBadRequest)
		return
	}

	// Default AdmissionReview response
	admissionReviewResp := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Response: &admissionv1.AdmissionResponse{
			UID:     admissionReviewReq.Request.UID,
			Allowed: true,
		},
	}

	// Only process UPDATE requests for GrafanaDashboard; pass anything else through.
	if admissionReviewReq.Request.Operation != admissionv1.Update || admissionReviewReq.Request.Kind.Kind != "GrafanaDashboard" {
		outcome = outcomeSkipped
		sendResponse(w, admissionReviewResp)
		return
	}

	// Parse old and new objects
	var oldObj, newObj map[string]interface{}
	err = json.Unmarshal(admissionReviewReq.Request.OldObject.Raw, &oldObj)
	if err != nil {
		http.Error(w, "failed to parse old object", http.StatusInternalServerError)
		return
	}

	err = json.Unmarshal(admissionReviewReq.Request.Object.Raw, &newObj)
	if err != nil {
		http.Error(w, "failed to parse new object", http.StatusInternalServerError)
		return
	}

	// Remove metadata.managedFields and metadata.generation
	cleanupMetadata(oldObj)
	cleanupMetadata(newObj)

	// Remove reconciledAt from both old and new objects
	removeLastResync(oldObj)
	removeLastResync(newObj)

	metadataChanged := !reflect.DeepEqual(oldObj["metadata"], newObj["metadata"])
	specChanged := !reflect.DeepEqual(oldObj["spec"], newObj["spec"])
	statusChanged := !reflect.DeepEqual(oldObj["status"], newObj["status"])

	if !metadataChanged && !specChanged && !statusChanged {
		log.Debug("No significant differences found.")

		outcome = outcomeUnchanged
		admissionReviewResp.Response.Allowed = false
		admissionReviewResp.Response.Result = &metav1.Status{
			Status:  "Success",
			Message: "Update successful.",
			Code:    http.StatusOK,
		}
	} else {
		if metadataChanged {
			printMetadataDifferences(oldObj, newObj)
		}
		if specChanged {
			printSpecDifferences(oldObj, newObj)
		}
		if statusChanged {
			printStatusDifferences(oldObj, newObj)
		}
		outcome = outcomeChanged
		admissionReviewResp.Response.Allowed = true
	}

	sendResponse(w, admissionReviewResp)
}

// Function to remove metadata.managedFields and metadata.generation
func cleanupMetadata(obj map[string]interface{}) {
	if metadata, exists := obj["metadata"].(map[string]interface{}); exists {
		delete(metadata, "managedFields")
		delete(metadata, "generation")
	}
}

// Helper function to remove lastResync from an object
func removeLastResync(obj map[string]interface{}) {
	if status, exists := obj["status"].(map[string]interface{}); exists {
		delete(status, "lastResync")
	}
}

func sendResponse(w http.ResponseWriter, admissionReviewResp admissionv1.AdmissionReview) {
	responseBytes, err := json.Marshal(admissionReviewResp)
	if err != nil {
		log.Errorf("Failed to marshal admission response: %v", err)
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(responseBytes); err != nil {
		log.Errorf("Failed to write admission response: %v", err)
	}
}

// Function to log metadata differences
func printMetadataDifferences(oldObj, newObj map[string]interface{}) {
	oldMetadata, _ := oldObj["metadata"].(map[string]interface{})
	newMetadata, _ := newObj["metadata"].(map[string]interface{})
	printDifferences("Metadata", oldMetadata, newMetadata)
}

// Function to log spec differences
func printSpecDifferences(oldObj, newObj map[string]interface{}) {
	oldSpec, _ := oldObj["spec"].(map[string]interface{})
	newSpec, _ := newObj["spec"].(map[string]interface{})
	printDifferences("Spec", oldSpec, newSpec)
}

// Function to log status differences
func printStatusDifferences(oldObj, newObj map[string]interface{}) {
	oldStatus, _ := oldObj["status"].(map[string]interface{})
	newStatus, _ := newObj["status"].(map[string]interface{})
	printDifferences("Status", oldStatus, newStatus)
}

// Function to print differences between two objects
func printDifferences(owner string, oldMap, newMap map[string]interface{}) {
	if oldMap == nil && newMap == nil {
		return
	}

	log.Debug("----- ", owner, " Differences -----")

	for key, oldValue := range oldMap {
		if newValue, exists := newMap[key]; exists {
			if !reflect.DeepEqual(oldValue, newValue) {
				log.Debugf("Key: %s\n  Old Value: %v\n  New Value: %v\n", key, oldValue, newValue)
			}
		} else {
			log.Debugf("Key removed: %s (Old Value: %v)", key, oldValue)
		}
	}

	for key, newValue := range newMap {
		if _, exists := oldMap[key]; !exists {
			log.Debugf("Key added: %s (New Value: %v)", key, newValue)
		}
	}
}

func main() {
	port := flag.String("port", "8443", "Webhook server port")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error, fatal, panic)")
	flag.Int64Var(&maxRequestBodyBytes, "max-request-body-bytes", maxRequestBodyBytes, "Maximum accepted request body size in bytes")
	flag.Parse()

	addr := fmt.Sprintf(":%s", *port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           http.DefaultServeMux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	level, err := log.ParseLevel(*logLevel)
	if err != nil {
		log.Fatalf("Invalid log level: %s", *logLevel)
	}
	log.SetLevel(level)

	// Metrics endpoint
	http.Handle("/metrics", promhttp.Handler())

	// Webhook handler
	http.HandleFunc("/validate", handleAdmissionReview)
	log.Infof("Starting webhook server on %s...", addr)

	go func() {
		if err := srv.ListenAndServeTLS("/certs/tls.crt", "/certs/tls.key"); err != nil && err != http.ErrServerClosed {
			log.Fatal("Failed to start webhook server:", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Info("Server exiting")
}

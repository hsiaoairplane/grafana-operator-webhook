package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestWebhookOperationHandler(t *testing.T) {
	tests := []struct {
		name            string
		operation       admissionv1.Operation
		expectedStatus  int
		expectedAllowed bool
	}{
		{"CREATE", admissionv1.Create, http.StatusOK, true},
		{"DELETE", admissionv1.Delete, http.StatusOK, true},
		{"CONNECT", admissionv1.Connect, http.StatusOK, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqBody := admissionv1.AdmissionReview{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "admission.k8s.io/v1",
					Kind:       "AdmissionReview",
				},
				Request: &admissionv1.AdmissionRequest{
					UID:       "uuid",
					Kind:      metav1.GroupVersionKind{Kind: "Application"},
					Operation: tt.operation,
					OldObject: runtime.RawExtension{Raw: []byte(`{"metadata": {}, "spec": {}, "status": {}}`)},
					Object:    runtime.RawExtension{Raw: []byte(`{"metadata": {}, "spec": {}, "status": {}}`)},
				},
			}

			reqBytes, err := json.Marshal(reqBody)
			if err != nil {
				t.Fatalf("Failed to marshal request: %v", err)
			}

			req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader(reqBytes))
			w := httptest.NewRecorder()

			handleAdmissionReview(w, req)

			resp := w.Result()
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("Expected status code 200, got %d", resp.StatusCode)
			}

			var admissionResp admissionv1.AdmissionReview
			if err := json.NewDecoder(resp.Body).Decode(&admissionResp); err != nil {
				t.Fatalf("Failed to decode response: %v", err)
			}

			if admissionResp.Response == nil {
				t.Fatalf("Expected a response, got nil")
			}

			if admissionResp.Response.UID != reqBody.Request.UID {
				t.Errorf("Expected UID %s, got %s", reqBody.Request.UID, admissionResp.Response.UID)
			}

			if !admissionResp.Response.Allowed {
				t.Errorf("Expected response to be allowed, but it was denied")
			}
		})
	}
}

func TestHandleAdmissionReview_NilRequest(t *testing.T) {
	// An AdmissionReview body without a "request" field must not panic the server.
	req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()

	handleAdmissionReview(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status code 400, got %d", resp.StatusCode)
	}
}

func TestHandleAdmissionReview_MethodNotAllowed(t *testing.T) {
	// Only POST is accepted; any other method must be rejected.
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/validate", nil)
			w := httptest.NewRecorder()

			handleAdmissionReview(w, req)

			resp := w.Result()
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("Expected status code 405, got %d", resp.StatusCode)
			}
		})
	}
}

func TestHandleAdmissionReview_BodyTooLarge(t *testing.T) {
	// A body exceeding maxRequestBodyBytes must be rejected rather than
	// read fully into memory.
	oversized := bytes.Repeat([]byte("a"), int(maxRequestBodyBytes)+1)
	req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader(oversized))
	w := httptest.NewRecorder()

	handleAdmissionReview(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("Expected status code 413, got %d", resp.StatusCode)
	}
}

func TestHandleAdmissionReview_StatusSyncRevisionChange(t *testing.T) {
	reqBody := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Request: &admissionv1.AdmissionRequest{
			UID:       "test-uid-status-sync-revision-change",
			Kind:      metav1.GroupVersionKind{Kind: "Application"},
			Operation: admissionv1.Update,
			OldObject: runtime.RawExtension{Raw: []byte(`{"metadata": {}, "spec": {}, "status": {"uid": "abc123"}}`)},
			Object:    runtime.RawExtension{Raw: []byte(`{"metadata": {}, "spec": {}, "status": {"uid": "def456"}}`)},
		},
	}

	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader(reqBytes))
	w := httptest.NewRecorder()

	handleAdmissionReview(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code 200, got %d", resp.StatusCode)
	}

	var admissionResp admissionv1.AdmissionReview
	if err := json.NewDecoder(resp.Body).Decode(&admissionResp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if admissionResp.Response == nil {
		t.Fatalf("Expected a response, got nil")
	}

	if admissionResp.Response.UID != reqBody.Request.UID {
		t.Errorf("Expected UID %s, got %s", reqBody.Request.UID, admissionResp.Response.UID)
	}

	if !admissionResp.Response.Allowed {
		t.Errorf("Expected response to be allowed, but it was denied")
	}
}

func TestHandleAdmissionReview_StatusReconciledAtChange(t *testing.T) {
	reqBody := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Request: &admissionv1.AdmissionRequest{
			UID:       "test-uid-status-change",
			Kind:      metav1.GroupVersionKind{Kind: "GrafanaDashboard"},
			Operation: admissionv1.Update,
			OldObject: runtime.RawExtension{Raw: []byte(`{"metadata": {}, "spec": {}, "status": {"lastResync": "2024-03-20T12:00:00Z"}}`)},
			Object:    runtime.RawExtension{Raw: []byte(`{"metadata": {}, "spec": {}, "status": {"lastResync": "2024-03-21T12:00:00Z"}}`)},
		},
	}

	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader(reqBytes))
	w := httptest.NewRecorder()

	handleAdmissionReview(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code 200, got %d", resp.StatusCode)
	}

	var admissionResp admissionv1.AdmissionReview
	if err := json.NewDecoder(resp.Body).Decode(&admissionResp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if admissionResp.Response == nil {
		t.Fatalf("Expected a response, got nil")
	}

	if admissionResp.Response.UID != reqBody.Request.UID {
		t.Errorf("Expected UID %s, got %s", reqBody.Request.UID, admissionResp.Response.UID)
	}

	if admissionResp.Response.Allowed {
		t.Errorf("Expected response to be denied, but it was allowed")
	}
}

func TestHandleAdmissionReview_MetricsByOutcome(t *testing.T) {
	makeReq := func(op admissionv1.Operation, kind, oldRaw, newRaw string) *http.Request {
		body := admissionv1.AdmissionReview{
			TypeMeta: metav1.TypeMeta{APIVersion: "admission.k8s.io/v1", Kind: "AdmissionReview"},
			Request: &admissionv1.AdmissionRequest{
				UID:       "uid",
				Kind:      metav1.GroupVersionKind{Kind: kind},
				Operation: op,
				OldObject: runtime.RawExtension{Raw: []byte(oldRaw)},
				Object:    runtime.RawExtension{Raw: []byte(newRaw)},
			},
		}
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("Failed to marshal request: %v", err)
		}
		return httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader(b))
	}

	const (
		identical   = `{"metadata": {}, "spec": {}, "status": {}}`
		changedSpec = `{"metadata": {}, "spec": {"json": "x"}, "status": {}}`
	)

	tests := []struct {
		name    string
		req     func() *http.Request
		outcome string
	}{
		{"error_method", func() *http.Request { return httptest.NewRequest(http.MethodGet, "/validate", nil) }, outcomeError},
		{"error_badjson", func() *http.Request {
			return httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader([]byte("not-json")))
		}, outcomeError},
		{"skipped_operation", func() *http.Request { return makeReq(admissionv1.Create, "GrafanaDashboard", identical, identical) }, outcomeSkipped},
		{"skipped_kind", func() *http.Request { return makeReq(admissionv1.Update, "Application", identical, identical) }, outcomeSkipped},
		{"unchanged", func() *http.Request { return makeReq(admissionv1.Update, "GrafanaDashboard", identical, identical) }, outcomeUnchanged},
		{"changed", func() *http.Request { return makeReq(admissionv1.Update, "GrafanaDashboard", identical, changedSpec) }, outcomeChanged},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := testutil.ToFloat64(processedTotal.WithLabelValues(tt.outcome))
			durationBefore := testutil.CollectAndCount(requestDuration)

			handleAdmissionReview(httptest.NewRecorder(), tt.req())

			if got := testutil.ToFloat64(processedTotal.WithLabelValues(tt.outcome)) - before; got != 1 {
				t.Errorf("processedTotal{outcome=%q} increased by %v, want 1", tt.outcome, got)
			}
			// The duration histogram is recorded on every path via the same
			// deferred call, so a series for this outcome must exist afterwards.
			if testutil.CollectAndCount(requestDuration) < durationBefore {
				t.Errorf("requestDuration series count decreased, want it recorded for outcome %q", tt.outcome)
			}
		})
	}
}

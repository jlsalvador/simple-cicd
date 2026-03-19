package webhook

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jlsalvador/simple-cicd/internal/k8s"
	"github.com/jlsalvador/simple-cicd/internal/types"
)

// --------------------------------------------------------------------------
// Minimal fakeClient for handler tests
// --------------------------------------------------------------------------

// handlerFakeClient only needs CreateWWR (the handler does nothing else with
// the client).
type handlerFakeClient struct {
	mu              sync.Mutex
	createdWWRs     []*types.WorkflowWebhookRequest
	failCreate      bool
	webhookNotFound bool // when true, GetWorkflowWebhook returns an error
}

var _ k8s.ClientIface = (*handlerFakeClient)(nil)

func (f *handlerFakeClient) CreateWWR(wwr *types.WorkflowWebhookRequest) (*types.WorkflowWebhookRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failCreate {
		return nil, fmt.Errorf("injected CreateWWR failure")
	}
	wwr.Metadata.UID = "test-uid"
	if wwr.Metadata.Name == "" {
		wwr.Metadata.Name = wwr.Metadata.GenerateName + "abc"
	}
	f.createdWWRs = append(f.createdWWRs, wwr)
	return wwr, nil
}

// Stub implementations for unused methods:
func (f *handlerFakeClient) GetWorkflow(_, _ string) (*types.Workflow, error) { return nil, nil }
func (f *handlerFakeClient) GetWorkflowWebhook(_, _ string) (*types.WorkflowWebhook, error) {
	if f.webhookNotFound {
		return nil, fmt.Errorf("not found")
	}
	return &types.WorkflowWebhook{}, nil
}
func (f *handlerFakeClient) GetWWR(_, _ string) (*types.WorkflowWebhookRequest, error) {
	return nil, nil
}
func (f *handlerFakeClient) ListWWRs(_ string) ([]types.WorkflowWebhookRequest, error) {
	return nil, nil
}
func (f *handlerFakeClient) ListAllWWRs() ([]types.WorkflowWebhookRequest, error)     { return nil, nil }
func (f *handlerFakeClient) UpdateWWRStatus(_ *types.WorkflowWebhookRequest) error    { return nil }
func (f *handlerFakeClient) PatchWWRFinalizers(_ *types.WorkflowWebhookRequest) error { return nil }
func (f *handlerFakeClient) DeleteWWR(_, _ string) error                              { return nil }
func (f *handlerFakeClient) CreateSecretForJob(_, _ string, _ types.WebhookRequestData, _, _ string) error {
	return nil
}
func (f *handlerFakeClient) GetJob(_, _ string) (*types.Job, error)        { return nil, nil }
func (f *handlerFakeClient) GetJobRaw(_, _ string) (map[string]any, error) { return nil, nil }
func (f *handlerFakeClient) CreateJobRaw(_ string, _ map[string]any) (k8s.CreatedResource, error) {
	return k8s.CreatedResource{}, nil
}
func (f *handlerFakeClient) DeleteJob(_, _ string) error { return nil }
func (f *handlerFakeClient) GetLease(_, _ string) (*types.Lease, error) {
	return &types.Lease{}, nil
}
func (f *handlerFakeClient) CreateLease(_ string, _ *types.Lease) (*types.Lease, error) {
	return &types.Lease{}, nil
}
func (f *handlerFakeClient) UpdateLease(_ *types.Lease) (*types.Lease, error) {
	return &types.Lease{}, nil
}

// --------------------------------------------------------------------------
// parsePath
// --------------------------------------------------------------------------

func TestParsePath(t *testing.T) {
	cases := []struct {
		path        string
		wantNS      string
		wantWebhook string
		wantErr     bool
	}{
		{"/default/my-hook", "default", "my-hook", false},
		{"/ns-a/hook-b", "ns-a", "hook-b", false},
		{"/only-one-segment", "", "", true},
		{"/", "", "", true},
		// SplitN with n=2 on "ns//extra" yields ["ns", "/extra"] — non-empty,
		// so parsePath accepts it. Kubernetes rejects invalid names at admission.
		{"/ns//extra", "ns", "/extra", false},
	}
	for _, c := range cases {
		ns, wh, err := parsePath(c.path)
		if c.wantErr {
			if err == nil {
				t.Errorf("parsePath(%q): expected error, got ns=%q wh=%q", c.path, ns, wh)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePath(%q): unexpected error: %v", c.path, err)
			continue
		}
		if ns != c.wantNS || wh != c.wantWebhook {
			t.Errorf("parsePath(%q): got (%q, %q), want (%q, %q)",
				c.path, ns, wh, c.wantNS, c.wantWebhook)
		}
	}
}

// --------------------------------------------------------------------------
// ServeHTTP: valid request
// --------------------------------------------------------------------------

func TestHandler_ValidRequest(t *testing.T) {
	fc := &handlerFakeClient{}
	triggered := false
	h := NewHandler(fc, func() { triggered = true })

	body := `{"event":"push"}`
	req := httptest.NewRequest(http.MethodPost, "/default/my-hook", strings.NewReader(body))
	req.Header.Set("X-Custom-Header", "hello")
	req.Header.Set("User-Agent", "test-agent/1.0")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("expected 202 Accepted, got %d: %s", rr.Code, rr.Body.String())
	}
	if !triggered {
		t.Errorf("expected triggerFn to be called")
	}
	if len(fc.createdWWRs) != 1 {
		t.Fatalf("expected 1 WWR created, got %d", len(fc.createdWWRs))
	}

	wwr := fc.createdWWRs[0]
	req2 := wwr.Spec.Request

	// Method should be base64("POST")
	assertBase64(t, "method", req2.Method, "POST")
	// Body should be base64(`{"event":"push"}`)
	assertBase64(t, "body", req2.Body, body)

	// Timestamp must decode to a valid Unix epoch
	tsDecoded := mustDecodeBase64(t, "timestamp", req2.Timestamp)
	epoch, err := strconv.ParseInt(tsDecoded, 10, 64)
	if err != nil {
		t.Errorf("timestamp is not a valid integer: %q, err: %v", tsDecoded, err)
	}
	now := time.Now().Unix()
	if epoch < now-5 || epoch > now+5 {
		t.Errorf("timestamp %d is not close to now (%d)", epoch, now)
	}

	// Labels
	if wwr.Metadata.Labels[types.LabelWebhookName] != "my-hook" {
		t.Errorf("wrong webhook-name label: %q", wwr.Metadata.Labels[types.LabelWebhookName])
	}
	if wwr.Metadata.Labels[types.LabelWebhookNamespace] != "default" {
		t.Errorf("wrong webhook-namespace label: %q", wwr.Metadata.Labels[types.LabelWebhookNamespace])
	}
}

// --------------------------------------------------------------------------
// ServeHTTP: invalid path
// --------------------------------------------------------------------------

func TestHandler_InvalidPath(t *testing.T) {
	cases := []string{"/", "/only-one"}
	fc := &handlerFakeClient{}
	h := NewHandler(fc, func() {})

	for _, path := range cases {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("path %q: expected 400, got %d", path, rr.Code)
		}
	}
}

// --------------------------------------------------------------------------
// ServeHTTP: /healthz is rejected by the handler itself
// --------------------------------------------------------------------------

func TestHandler_HealthzRejected(t *testing.T) {
	fc := &handlerFakeClient{}
	h := NewHandler(fc, func() {})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for /healthz on bare handler, got %d", rr.Code)
	}
}

// --------------------------------------------------------------------------
// ServeHTTP: CreateWWR failure → 500
// --------------------------------------------------------------------------

func TestHandler_CreateWWRFailure(t *testing.T) {
	fc := &handlerFakeClient{failCreate: true}
	h := NewHandler(fc, func() {})

	req := httptest.NewRequest(http.MethodPost, "/default/my-hook", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on CreateWWR failure, got %d", rr.Code)
	}
}

// --------------------------------------------------------------------------
// ServeHTTP: empty body is valid
// --------------------------------------------------------------------------

func TestHandler_EmptyBody(t *testing.T) {
	fc := &handlerFakeClient{}
	h := NewHandler(fc, func() {})

	req := httptest.NewRequest(http.MethodPost, "/default/my-hook", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("expected 202 for empty body, got %d", rr.Code)
	}
	assertBase64(t, "body", fc.createdWWRs[0].Spec.Request.Body, "")
}

// --------------------------------------------------------------------------
// Response body contains WWR name
// --------------------------------------------------------------------------

func TestHandler_ResponseContainsWWRName(t *testing.T) {
	fc := &handlerFakeClient{}
	h := NewHandler(fc, func() {})

	req := httptest.NewRequest(http.MethodPost, "/default/my-hook", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	respBody, _ := io.ReadAll(rr.Body)
	if !strings.Contains(string(respBody), "my-hook") {
		t.Errorf("response body should mention the WWR name, got: %q", string(respBody))
	}
}

// --------------------------------------------------------------------------
// ServeHTTP: WorkflowWebhook does not exist → 404, no WWR created
// --------------------------------------------------------------------------

func TestHandler_WebhookNotFound(t *testing.T) {
	fc := &handlerFakeClient{webhookNotFound: true}
	triggered := false
	h := NewHandler(fc, func() { triggered = true })

	req := httptest.NewRequest(http.MethodPost, "/default/nonexistent-hook", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 when WorkflowWebhook does not exist, got %d", rr.Code)
	}
	if len(fc.createdWWRs) != 0 {
		t.Errorf("expected no WWR to be created, got %d", len(fc.createdWWRs))
	}
	if triggered {
		t.Errorf("expected triggerFn not to be called")
	}
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func assertBase64(t *testing.T, field, encoded, wantDecoded string) {
	t.Helper()
	got := mustDecodeBase64(t, field, encoded)
	if got != wantDecoded {
		t.Errorf("%s: decoded %q, want %q", field, got, wantDecoded)
	}
}

func mustDecodeBase64(t *testing.T, field, encoded string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Errorf("%s: invalid base64 %q: %v", field, encoded, err)
		return ""
	}
	return string(b)
}

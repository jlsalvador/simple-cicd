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

// handlerCreatedSecret records a CreateRequestSecret call.
type handlerCreatedSecret struct {
	namespace string
	name      string
	data      types.WebhookRequestData
}

// handlerFakeClient implements the small subset of ClientIface that the
// handler touches: GetWorkflowWebhook, CreateRequestSecret, CreateWWR,
// and DeleteSecret (called on CreateWWR failure rollback).
type handlerFakeClient struct {
	mu sync.Mutex

	// recorded calls
	createdWWRs       []*types.WorkflowWebhookRequest
	createdSecrets    []handlerCreatedSecret
	deleteSecretCalls []string // "ns/name"

	// injection flags
	failCreateWWR    bool // make CreateWWR return an error
	failCreateSecret bool // make CreateRequestSecret return an error
	webhookNotFound  bool // make GetWorkflowWebhook return an error
	webhookTTL       *int32
	webhookDeadline  *int32
}

var _ k8s.ClientIface = (*handlerFakeClient)(nil)

func (f *handlerFakeClient) GetWorkflowWebhook(_, _ string) (*types.WorkflowWebhook, error) {
	if f.webhookNotFound {
		return nil, fmt.Errorf("not found")
	}
	wh := &types.WorkflowWebhook{}
	wh.Spec.TTLSecondsAfterFinished = f.webhookTTL
	wh.Spec.ActiveDeadlineSeconds = f.webhookDeadline
	return wh, nil
}

func (f *handlerFakeClient) CreateRequestSecret(namespace, webhookName string, req types.WebhookRequestData) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failCreateSecret {
		return "", fmt.Errorf("injected CreateRequestSecret failure")
	}
	name := webhookName + "-request-abc"
	f.createdSecrets = append(f.createdSecrets, handlerCreatedSecret{
		namespace: namespace,
		name:      name,
		data:      req,
	})
	return name, nil
}

func (f *handlerFakeClient) DeleteSecret(namespace, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteSecretCalls = append(f.deleteSecretCalls, namespace+"/"+name)
	return nil
}

func (f *handlerFakeClient) CreateWWR(wwr *types.WorkflowWebhookRequest) (*types.WorkflowWebhookRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failCreateWWR {
		return nil, fmt.Errorf("injected CreateWWR failure")
	}
	wwr.Metadata.UID = "test-uid"
	if wwr.Metadata.Name == "" {
		wwr.Metadata.Name = wwr.Metadata.GenerateName + "abc"
	}
	f.createdWWRs = append(f.createdWWRs, wwr)
	return wwr, nil
}

// --- Stub implementations for unused methods ---

func (f *handlerFakeClient) GetWorkflow(_, _ string) (*types.Workflow, error) { return nil, nil }
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
func (f *handlerFakeClient) GetSecretData(_, _ string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (f *handlerFakeClient) MirrorSecret(_, _, _, _, _, _ string) error { return nil }
func (f *handlerFakeClient) GetJob(_, _ string) (*types.Job, error)     { return nil, nil }
func (f *handlerFakeClient) GetJobRaw(_, _ string) (map[string]any, error) {
	return nil, nil
}
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

	// Verify the request Secret was created before the WWR.
	if len(fc.createdSecrets) != 1 {
		t.Fatalf("expected 1 request secret created, got %d", len(fc.createdSecrets))
	}
	secretData := fc.createdSecrets[0].data

	// Method should be base64("POST").
	assertBase64(t, "method", secretData.Method, "POST")
	// Body should be base64(`{"event":"push"}`).
	assertBase64(t, "body", secretData.Body, body)

	// Timestamp must decode to a valid Unix epoch.
	tsDecoded := mustDecodeBase64(t, "timestamp", secretData.Timestamp)
	epoch, err := strconv.ParseInt(tsDecoded, 10, 64)
	if err != nil {
		t.Errorf("timestamp is not a valid integer: %q, err: %v", tsDecoded, err)
	}
	now := time.Now().Unix()
	if epoch < now-5 || epoch > now+5 {
		t.Errorf("timestamp %d is not close to now (%d)", epoch, now)
	}

	// Verify that exactly one WWR was created.
	if len(fc.createdWWRs) != 1 {
		t.Fatalf("expected 1 WWR created, got %d", len(fc.createdWWRs))
	}
	wwr := fc.createdWWRs[0]

	// The WWR spec must reference the secret, not embed the data.
	if wwr.Spec.RequestSecret.Name == "" {
		t.Errorf("expected wwr.Spec.RequestSecret.Name to be set")
	}
	if wwr.Spec.RequestSecret.Name != fc.createdSecrets[0].name {
		t.Errorf("WWR requestSecret.name %q does not match created secret name %q",
			wwr.Spec.RequestSecret.Name, fc.createdSecrets[0].name)
	}

	// Labels.
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
// ServeHTTP: CreateWWR failure -> secret rolled back, 500 returned
// --------------------------------------------------------------------------

func TestHandler_CreateWWRFailure(t *testing.T) {
	fc := &handlerFakeClient{failCreateWWR: true}
	h := NewHandler(fc, func() {})

	req := httptest.NewRequest(http.MethodPost, "/default/my-hook", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on CreateWWR failure, got %d", rr.Code)
	}
	// The handler must have created the secret first, then rolled it back.
	if len(fc.createdSecrets) != 1 {
		t.Errorf("expected request secret to have been created before rollback, got %d", len(fc.createdSecrets))
	}
	if len(fc.deleteSecretCalls) != 1 {
		t.Errorf("expected orphaned secret to be deleted on CreateWWR failure, deleteSecretCalls=%v",
			fc.deleteSecretCalls)
	}
	if fc.deleteSecretCalls[0] != "default/"+fc.createdSecrets[0].name {
		t.Errorf("deleted wrong secret: got %q, want %q",
			fc.deleteSecretCalls[0], "default/"+fc.createdSecrets[0].name)
	}
}

// --------------------------------------------------------------------------
// ServeHTTP: CreateRequestSecret failure -> 500, no WWR created
// --------------------------------------------------------------------------

func TestHandler_CreateSecretFailure(t *testing.T) {
	fc := &handlerFakeClient{failCreateSecret: true}
	triggered := false
	h := NewHandler(fc, func() { triggered = true })

	req := httptest.NewRequest(http.MethodPost, "/default/my-hook", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on CreateRequestSecret failure, got %d", rr.Code)
	}
	if len(fc.createdWWRs) != 0 {
		t.Errorf("expected no WWR to be created when secret creation fails, got %d", len(fc.createdWWRs))
	}
	if triggered {
		t.Errorf("expected triggerFn not to be called")
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
	if len(fc.createdSecrets) != 1 {
		t.Fatalf("expected 1 request secret, got %d", len(fc.createdSecrets))
	}
	assertBase64(t, "body", fc.createdSecrets[0].data.Body, "")
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
// ServeHTTP: WorkflowWebhook does not exist → 404, no WWR or secret created
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
	if len(fc.createdSecrets) != 0 {
		t.Errorf("expected no request secret to be created, got %d", len(fc.createdSecrets))
	}
	if triggered {
		t.Errorf("expected triggerFn not to be called")
	}
}

// --------------------------------------------------------------------------
// TTL propagation: value from WorkflowWebhook is copied to WWR spec
// --------------------------------------------------------------------------

func TestHandler_TTLPropagatedToWWR(t *testing.T) {
	ttl := int32(300)
	fc := &handlerFakeClient{webhookTTL: &ttl}
	h := NewHandler(fc, func() {})

	req := httptest.NewRequest(http.MethodPost, "/default/my-hook", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}
	if len(fc.createdWWRs) != 1 {
		t.Fatalf("expected 1 WWR, got %d", len(fc.createdWWRs))
	}
	wwr := fc.createdWWRs[0]
	if wwr.Spec.TTLSecondsAfterFinished == nil {
		t.Fatalf("expected TTLSecondsAfterFinished to be set on the WWR")
	}
	if *wwr.Spec.TTLSecondsAfterFinished != ttl {
		t.Errorf("expected TTL=%d, got %d", ttl, *wwr.Spec.TTLSecondsAfterFinished)
	}
}

func TestHandler_NoTTLWhenWebhookHasNone(t *testing.T) {
	fc := &handlerFakeClient{} // webhookTTL is nil
	h := NewHandler(fc, func() {})

	req := httptest.NewRequest(http.MethodPost, "/default/my-hook", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}
	wwr := fc.createdWWRs[0]
	if wwr.Spec.TTLSecondsAfterFinished != nil {
		t.Errorf("expected no TTL on WWR when webhook has none, got %d",
			*wwr.Spec.TTLSecondsAfterFinished)
	}
}

func TestHandler_ActiveDeadlinePropagatedToWWR(t *testing.T) {
	deadline := int32(300)
	fc := &handlerFakeClient{webhookDeadline: &deadline}
	h := NewHandler(fc, func() {})

	req := httptest.NewRequest(http.MethodPost, "/default/my-hook", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}
	wwr := fc.createdWWRs[0]
	if wwr.Spec.ActiveDeadlineSeconds == nil {
		t.Fatalf("expected ActiveDeadlineSeconds to be set on the WWR")
	}
	if *wwr.Spec.ActiveDeadlineSeconds != deadline {
		t.Errorf("expected deadline=%d, got %d", deadline, *wwr.Spec.ActiveDeadlineSeconds)
	}
}

func TestHandler_NoDeadlineWhenWebhookHasNone(t *testing.T) {
	fc := &handlerFakeClient{} // webhookDeadline is nil
	h := NewHandler(fc, func() {})

	req := httptest.NewRequest(http.MethodPost, "/default/my-hook", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}
	wwr := fc.createdWWRs[0]
	if wwr.Spec.ActiveDeadlineSeconds != nil {
		t.Errorf("expected no deadline on WWR when webhook has none, got %d",
			*wwr.Spec.ActiveDeadlineSeconds)
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

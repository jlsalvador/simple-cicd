package controller

// fake_client_test.go – in-memory ClientIface for use in reconciler tests.
// Lives in the controller package (not _test) so reconciler internals can
// construct it directly, but is only compiled when running tests.

import (
	"fmt"
	"maps"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jlsalvador/simple-cicd/internal/k8s"
	"github.com/jlsalvador/simple-cicd/internal/types"
)

// compile-time guard
var _ k8s.ClientIface = (*fakeClient)(nil)

// requestSecretEntry records a call to CreateRequestSecret.
type requestSecretEntry struct {
	namespace string
	name      string
	data      types.WebhookRequestData
}

// fakeClient is a thread-safe in-memory implementation of ClientIface.
// Every mutating call records itself so tests can assert side-effects.
type fakeClient struct {
	t  *testing.T
	mu sync.RWMutex

	// --- stored resources ---
	workflows    map[string]*types.Workflow        // key: "ns/name"
	webhooks     map[string]*types.WorkflowWebhook // key: "ns/name"
	wwrs         map[string]*types.WorkflowWebhookRequest
	jobTemplates map[string]map[string]any // raw job templates: key "ns/name"
	liveJobs     map[string]*types.Job     // cloned jobs: key "ns/name"

	// --- secret tracking ---
	// requestSecrets records calls to CreateRequestSecret (handler creates these).
	requestSecrets []requestSecretEntry
	// mirroredSecrets records calls to MirrorSecret ("dstNs/dstName").
	mirroredSecrets []string
	// deleteSecretCalls records calls to DeleteSecret ("ns/name").
	deleteSecretCalls []string

	// --- call counters ---
	updateStatusCalls   atomic.Int32
	patchFinalizerCalls atomic.Int32
	deleteJobCalls      []string // "ns/name"
	deleteWWRCalls      []string // "ns/name"

	// --- id sequence ---
	seq atomic.Uint64
}

func newFakeClient(t *testing.T) *fakeClient {
	t.Helper()
	return &fakeClient{
		t:            t,
		workflows:    make(map[string]*types.Workflow),
		webhooks:     make(map[string]*types.WorkflowWebhook),
		wwrs:         make(map[string]*types.WorkflowWebhookRequest),
		jobTemplates: make(map[string]map[string]any),
		liveJobs:     make(map[string]*types.Job),
	}
}

func (f *fakeClient) key(ns, name string) string { return ns + "/" + name }

func (f *fakeClient) nextID() string {
	id := f.seq.Add(1)
	return fmt.Sprintf("uid-%04d", id)
}

// --------------------------------------------------------------------------
// Seed helpers (called from tests to populate state)
// --------------------------------------------------------------------------

func (f *fakeClient) addWorkflow(wf *types.Workflow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workflows[f.key(wf.Metadata.Namespace, wf.Metadata.Name)] = wf
}

func (f *fakeClient) addWebhook(wh *types.WorkflowWebhook) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.webhooks[f.key(wh.Metadata.Namespace, wh.Metadata.Name)] = wh
}

func (f *fakeClient) addWWR(wwr *types.WorkflowWebhookRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if wwr.Metadata.UID == "" {
		wwr.Metadata.UID = f.nextID()
	}
	f.wwrs[f.key(wwr.Metadata.Namespace, wwr.Metadata.Name)] = wwr
}

// addJobTemplate stores a raw job map that GetJobRaw will return.
func (f *fakeClient) addJobTemplate(namespace, name string, raw map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobTemplates[f.key(namespace, name)] = raw
}

// setJobStatus updates (or inserts) a live job's status, simulating the
// Kubernetes job controller marking a job as succeeded or failed.
func (f *fakeClient) setJobStatus(namespace, name string, status types.JobStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := f.key(namespace, name)
	if j, ok := f.liveJobs[key]; ok {
		j.Status = status
	} else {
		f.liveJobs[key] = &types.Job{
			Metadata: types.ObjectMeta{Namespace: namespace, Name: name},
			Status:   status,
		}
	}
}

// --------------------------------------------------------------------------
// ClientIface – Workflow
// --------------------------------------------------------------------------

func (f *fakeClient) GetWorkflow(namespace, name string) (*types.Workflow, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	wf, ok := f.workflows[f.key(namespace, name)]
	if !ok {
		return nil, fmt.Errorf("workflow %s/%s not found", namespace, name)
	}
	return wf, nil
}

// --------------------------------------------------------------------------
// ClientIface – WorkflowWebhook
// --------------------------------------------------------------------------

func (f *fakeClient) GetWorkflowWebhook(namespace, name string) (*types.WorkflowWebhook, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	wh, ok := f.webhooks[f.key(namespace, name)]
	if !ok {
		return nil, fmt.Errorf("workflowwebhook %s/%s not found", namespace, name)
	}
	return wh, nil
}

// --------------------------------------------------------------------------
// ClientIface – WorkflowWebhookRequest
// --------------------------------------------------------------------------

func (f *fakeClient) CreateWWR(wwr *types.WorkflowWebhookRequest) (*types.WorkflowWebhookRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if wwr.Metadata.UID == "" {
		wwr.Metadata.UID = f.nextID()
	}
	f.wwrs[f.key(wwr.Metadata.Namespace, wwr.Metadata.Name)] = wwr
	return wwr, nil
}

func (f *fakeClient) GetWWR(namespace, name string) (*types.WorkflowWebhookRequest, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	wwr, ok := f.wwrs[f.key(namespace, name)]
	if !ok {
		return nil, fmt.Errorf("wwr %s/%s not found", namespace, name)
	}
	return wwr, nil
}

func (f *fakeClient) ListWWRs(namespace string) ([]types.WorkflowWebhookRequest, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var result []types.WorkflowWebhookRequest
	for _, w := range f.wwrs {
		if w.Metadata.Namespace == namespace {
			result = append(result, *w)
		}
	}
	return result, nil
}

func (f *fakeClient) ListAllWWRs() ([]types.WorkflowWebhookRequest, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	result := make([]types.WorkflowWebhookRequest, 0, len(f.wwrs))
	for _, w := range f.wwrs {
		result = append(result, *w)
	}
	return result, nil
}

func (f *fakeClient) UpdateWWRStatus(wwr *types.WorkflowWebhookRequest) error {
	f.updateStatusCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	key := f.key(wwr.Metadata.Namespace, wwr.Metadata.Name)
	if existing, ok := f.wwrs[key]; ok {
		existing.Status = wwr.Status
	}
	return nil
}

func (f *fakeClient) PatchWWRFinalizers(wwr *types.WorkflowWebhookRequest) error {
	f.patchFinalizerCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	key := f.key(wwr.Metadata.Namespace, wwr.Metadata.Name)
	if existing, ok := f.wwrs[key]; ok {
		existing.Metadata.Finalizers = wwr.Metadata.Finalizers
	}
	return nil
}

func (f *fakeClient) DeleteWWR(namespace, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteWWRCalls = append(f.deleteWWRCalls, f.key(namespace, name))
	delete(f.wwrs, f.key(namespace, name))
	return nil
}

// --------------------------------------------------------------------------
// ClientIface – Secrets
// --------------------------------------------------------------------------

// CreateRequestSecret records the request data and returns a generated name.
// In real operation this is called by the webhook handler, not the reconciler,
// but the fake supports it so handler-level flows can be tested end-to-end if
// needed. The reconciler only calls MirrorSecret and DeleteSecret.
func (f *fakeClient) CreateRequestSecret(namespace, webhookName string, req types.WebhookRequestData) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := webhookName + "-request-" + f.nextID()
	f.requestSecrets = append(f.requestSecrets, requestSecretEntry{
		namespace: namespace,
		name:      name,
		data:      req,
	})
	return name, nil
}

// GetSecretData returns a canned data map built from the stored request secret
// that matches namespace/name. Used when MirrorSecret reads the source Secret.
// Returns an empty map for any unknown secret (tests that don't need real data).
func (f *fakeClient) GetSecretData(namespace, name string) (map[string]string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, e := range f.requestSecrets {
		if e.namespace == namespace && e.name == name {
			return map[string]string{
				"body":       e.data.Body,
				"headers":    e.data.Headers,
				"host":       e.data.Host,
				"method":     e.data.Method,
				"url":        e.data.URL,
				"remoteAddr": e.data.RemoteAddr,
				"timestamp":  e.data.Timestamp,
			}, nil
		}
	}
	// Unknown secret: return empty data (acceptable for tests that only verify
	// that MirrorSecret was called, not that it contains specific values).
	return map[string]string{}, nil
}

// MirrorSecret records that a mirrored copy was requested from srcNs/srcName
// into dstNs/dstName. Stored as "dstNs/dstName" for assertion convenience.
func (f *fakeClient) MirrorSecret(_, _, dstNamespace, dstName, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mirroredSecrets = append(f.mirroredSecrets, f.key(dstNamespace, dstName))
	return nil
}

// DeleteSecret records the deletion call. Returns nil for any input (including
// already-absent secrets) to match the real implementation's 404-swallowing.
func (f *fakeClient) DeleteSecret(namespace, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteSecretCalls = append(f.deleteSecretCalls, f.key(namespace, name))
	return nil
}

// --------------------------------------------------------------------------
// ClientIface – Jobs
// --------------------------------------------------------------------------

func (f *fakeClient) GetJob(namespace, name string) (*types.Job, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	j, ok := f.liveJobs[f.key(namespace, name)]
	if !ok {
		return nil, fmt.Errorf("job %s/%s not found", namespace, name)
	}
	return j, nil
}

func (f *fakeClient) GetJobRaw(namespace, name string) (map[string]any, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	raw, ok := f.jobTemplates[f.key(namespace, name)]
	if !ok {
		return nil, fmt.Errorf("job template %s/%s not found", namespace, name)
	}
	// Return a shallow copy so tests can't accidentally mutate the template.
	cp := make(map[string]any, len(raw))
	maps.Copy(cp, raw)
	return cp, nil
}

// CreateJobRaw "submits" a job: assigns a UID, stores it as a live job with
// Active=1 (running), and returns its assigned name.
func (f *fakeClient) CreateJobRaw(namespace string, job map[string]any) (k8s.CreatedResource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	meta, _ := job["metadata"].(map[string]any)
	genName, _ := meta["generateName"].(string)
	uid := f.nextID()
	name := genName + uid

	f.liveJobs[f.key(namespace, name)] = &types.Job{
		Metadata: types.ObjectMeta{Namespace: namespace, Name: name, UID: uid},
		// Starts as active/running.
		Status: types.JobStatus{Active: 1},
	}
	return k8s.CreatedResource{Name: name, UID: uid}, nil
}

func (f *fakeClient) DeleteJob(namespace, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteJobCalls = append(f.deleteJobCalls, f.key(namespace, name))
	delete(f.liveJobs, f.key(namespace, name))
	return nil
}

func (f *fakeClient) GetLease(_, _ string) (*types.Lease, error) {
	return &types.Lease{}, nil
}
func (f *fakeClient) CreateLease(_ string, _ *types.Lease) (*types.Lease, error) {
	return &types.Lease{}, nil
}
func (f *fakeClient) UpdateLease(_ *types.Lease) (*types.Lease, error) {
	return &types.Lease{}, nil
}

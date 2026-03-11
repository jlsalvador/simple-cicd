package k8s

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/jlsalvador/simple-cicd/internal/types"
)

const (
	serviceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

	// API path prefix for the custom resources.
	crdAPIPrefix = "/apis/" + types.APIGroup + "/" + types.APIVersion
)

// k8sBaseURL returns the Kubernetes API server base URL using the environment
// variables that the kubelet injects into every pod:
//
//	KUBERNETES_SERVICE_HOST – cluster IP of the kubernetes.default.svc service
//	KUBERNETES_SERVICE_PORT – port (normally 443)
//
// Falling back to the well-known default only if the variables are absent
// (e.g. during local development / unit tests).
func k8sBaseURL() string {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return "https://kubernetes.default.svc:443"
	}
	// IPv6 addresses must be wrapped in brackets.
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "https://" + host + ":" + port
}

// Client performs authenticated requests to the Kubernetes API server.
type Client struct {
	http    *http.Client
	token   string
	baseURL string
}

// NewClient reads the in-cluster service-account credentials and builds a Client.
func NewClient() (*Client, error) {
	caCert, err := os.ReadFile(serviceAccountDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("reading ca.crt: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to append CA cert to pool")
	}

	tokenBytes, err := os.ReadFile(serviceAccountDir + "/token")
	if err != nil {
		return nil, fmt.Errorf("reading service account token: %w", err)
	}

	return &Client{
		http: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: pool},
			},
		},
		token:   strings.TrimSpace(string(tokenBytes)),
		baseURL: k8sBaseURL(),
	}, nil
}

// --------------------------------------------------------------------------
// Low-level helpers
// --------------------------------------------------------------------------

func (c *Client) doRequest(method, path string, body any, contentType string) ([]byte, int, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		ct := contentType
		if ct == "" {
			ct = "application/json"
		}
		req.Header.Set("Content-Type", ct)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("execute request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	return data, resp.StatusCode, err
}

func (c *Client) get(path string, out any) error {
	data, status, err := c.doRequest("GET", path, nil, "")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("GET %s: status %d: %s", path, status, string(data))
	}
	return json.Unmarshal(data, out)
}

func (c *Client) post(path string, body, out any) error {
	data, status, err := c.doRequest("POST", path, body, "application/json")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("POST %s: status %d: %s", path, status, string(data))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Client) mergePatch(path string, patch any) error {
	data, status, err := c.doRequest("PATCH", path, patch, "application/merge-patch+json")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("PATCH %s: status %d: %s", path, status, string(data))
	}
	return nil
}

func (c *Client) delete(path string) error {
	data, status, err := c.doRequest("DELETE", path, nil, "")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("DELETE %s: status %d: %s", path, status, string(data))
	}
	return nil
}

// --------------------------------------------------------------------------
// Workflow
// --------------------------------------------------------------------------

func (c *Client) GetWorkflow(namespace, name string) (*types.Workflow, error) {
	path := fmt.Sprintf("%s/namespaces/%s/workflows/%s", crdAPIPrefix, namespace, name)
	var obj types.Workflow
	return &obj, c.get(path, &obj)
}

// --------------------------------------------------------------------------
// WorkflowWebhook
// --------------------------------------------------------------------------

func (c *Client) GetWorkflowWebhook(namespace, name string) (*types.WorkflowWebhook, error) {
	path := fmt.Sprintf("%s/namespaces/%s/workflowwebhooks/%s", crdAPIPrefix, namespace, name)
	var obj types.WorkflowWebhook
	return &obj, c.get(path, &obj)
}

// --------------------------------------------------------------------------
// WorkflowWebhookRequest
// --------------------------------------------------------------------------

func (c *Client) CreateWWR(wwr *types.WorkflowWebhookRequest) (*types.WorkflowWebhookRequest, error) {
	path := fmt.Sprintf("%s/namespaces/%s/workflowwebhookrequests", crdAPIPrefix, wwr.Metadata.Namespace)
	var created types.WorkflowWebhookRequest
	return &created, c.post(path, wwr, &created)
}

func (c *Client) GetWWR(namespace, name string) (*types.WorkflowWebhookRequest, error) {
	path := fmt.Sprintf("%s/namespaces/%s/workflowwebhookrequests/%s", crdAPIPrefix, namespace, name)
	var obj types.WorkflowWebhookRequest
	return &obj, c.get(path, &obj)
}

// ListWWRs returns all WorkflowWebhookRequests in a specific namespace.
func (c *Client) ListWWRs(namespace string) ([]types.WorkflowWebhookRequest, error) {
	path := fmt.Sprintf("%s/namespaces/%s/workflowwebhookrequests", crdAPIPrefix, namespace)
	var list types.WorkflowWebhookRequestList
	if err := c.get(path, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// ListAllWWRs returns all WorkflowWebhookRequests across all namespaces.
func (c *Client) ListAllWWRs() ([]types.WorkflowWebhookRequest, error) {
	path := crdAPIPrefix + "/workflowwebhookrequests"
	var list types.WorkflowWebhookRequestList
	if err := c.get(path, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// UpdateWWRStatus patches the status subresource of the given WWR.
// The CRD declares "subresources: status: {}" so we must use the /status
// endpoint; patching the main resource would silently ignore status fields.
func (c *Client) UpdateWWRStatus(wwr *types.WorkflowWebhookRequest) error {
	path := fmt.Sprintf("%s/namespaces/%s/workflowwebhookrequests/%s/status",
		crdAPIPrefix, wwr.Metadata.Namespace, wwr.Metadata.Name)
	patch := map[string]any{
		"status": wwr.Status,
	}
	return c.mergePatch(path, patch)
}

// PatchWWRFinalizers replaces the finalizers list on the WWR.
// A merge-patch on metadata.finalizers replaces the whole slice, which is
// fine since we only manage a single finalizer.
func (c *Client) PatchWWRFinalizers(wwr *types.WorkflowWebhookRequest) error {
	path := fmt.Sprintf("%s/namespaces/%s/workflowwebhookrequests/%s",
		crdAPIPrefix, wwr.Metadata.Namespace, wwr.Metadata.Name)
	patch := map[string]any{
		"metadata": map[string]any{
			"finalizers": wwr.Metadata.Finalizers,
		},
	}
	return c.mergePatch(path, patch)
}

// DeleteWWR deletes a WorkflowWebhookRequest by namespace and name.
func (c *Client) DeleteWWR(namespace, name string) error {
	path := fmt.Sprintf("%s/namespaces/%s/workflowwebhookrequests/%s", crdAPIPrefix, namespace, name)
	return c.delete(path)
}

// --------------------------------------------------------------------------
// Secrets (v1)
// --------------------------------------------------------------------------

// CreateSecretForJob creates a Secret in namespace with the given name (not
// generateName) containing the HTTP request data from the WWR, already owned
// by the specified job. This order (job first, then secret with ownerRef)
// ensures the Secret is garbage-collected automatically when the Job is deleted.
func (c *Client) CreateSecretForJob(
	namespace, secretName string,
	req types.WebhookRequestData,
	jobName, jobUID string,
) error {
	secret := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      secretName,
			"namespace": namespace,
			"labels": map[string]any{
				types.LabelWWRName:      jobName, // link to the owning job for observability
				types.LabelWWRNamespace: namespace,
			},
			"ownerReferences": []map[string]any{
				{
					"apiVersion":         "batch/v1",
					"kind":               "Job",
					"name":               jobName,
					"uid":                jobUID,
					"controller":         new(true),
					"blockOwnerDeletion": new(true),
				},
			},
		},
		// Values are already base64-encoded by the handler.
		"data": map[string]string{
			"body":       req.Body,
			"headers":    req.Headers,
			"host":       req.Host,
			"method":     req.Method,
			"url":        req.URL,
			"remoteAddr": req.RemoteAddr,
			"timestamp":  req.Timestamp,
			"userAgent":  req.UserAgent,
		},
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/secrets", namespace)
	data, status, err := c.doRequest("POST", path, secret, "application/json")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("POST secret %s/%s: status %d: %s", namespace, secretName, status, string(data))
	}
	return nil
}

// --------------------------------------------------------------------------
// Jobs (batch/v1)
// --------------------------------------------------------------------------

// CreatedResource holds the name and UID returned by the API server after
// creating a resource.
type CreatedResource struct {
	Name string
	UID  string
}

// GetJob fetches a Job and returns only the fields we care about for status checks.
func (c *Client) GetJob(namespace, name string) (*types.Job, error) {
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs/%s", namespace, name)
	var obj types.Job
	return &obj, c.get(path, &obj)
}

// GetJobRaw fetches a Job and returns the full raw JSON map (used for cloning).
func (c *Client) GetJobRaw(namespace, name string) (map[string]any, error) {
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs/%s", namespace, name)
	data, status, err := c.doRequest("GET", path, nil, "")
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("GET job %s/%s: status %d: %s", namespace, name, status, string(data))
	}
	var result map[string]any
	return result, json.Unmarshal(data, &result)
}

// CreateJobRaw posts a prepared job map and returns the name and UID assigned
// by the API server. The UID is needed to set ownerReferences on the
// per-job request Secret created immediately after.
func (c *Client) CreateJobRaw(namespace string, job map[string]any) (CreatedResource, error) {
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs", namespace)
	data, status, err := c.doRequest("POST", path, job, "application/json")
	if err != nil {
		return CreatedResource{}, err
	}
	if status < 200 || status >= 300 {
		return CreatedResource{}, fmt.Errorf("POST job in %s: status %d: %s", namespace, status, string(data))
	}
	var created map[string]any
	if err := json.Unmarshal(data, &created); err != nil {
		return CreatedResource{}, fmt.Errorf("unmarshal created job: %w", err)
	}
	meta, _ := created["metadata"].(map[string]any)
	if meta == nil {
		return CreatedResource{}, fmt.Errorf("created job has no metadata")
	}
	return CreatedResource{
		Name: meta["name"].(string),
		UID:  meta["uid"].(string),
	}, nil
}

// DeleteJob deletes a Job by namespace and name.
// Returns nil if the job is already gone (404).
func (c *Client) DeleteJob(namespace, name string) error {
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs/%s", namespace, name)
	data, status, err := c.doRequest("DELETE", path, nil, "")
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		return nil // already deleted.
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("DELETE job %s/%s: status %d: %s", namespace, name, status, string(data))
	}
	return nil
}

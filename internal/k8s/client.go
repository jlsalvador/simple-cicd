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
	k8sHost           = "https://10.96.0.1:443"

	// API path prefix for the custom resources
	crdAPIPrefix = "/apis/" + types.APIGroup + "/" + types.APIVersion
)

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
		baseURL: k8sHost,
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

// UpdateWWRStatus patches the status of the given WWR using a merge-patch.
// Because the CRD does not define a /status subresource, we patch the
// main resource endpoint.
func (c *Client) UpdateWWRStatus(wwr *types.WorkflowWebhookRequest) error {
	path := fmt.Sprintf("%s/namespaces/%s/workflowwebhookrequests/%s",
		crdAPIPrefix, wwr.Metadata.Namespace, wwr.Metadata.Name)
	patch := map[string]any{
		"status": wwr.Status,
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

// CreateSecret posts a Secret and returns the name assigned by the API server.
func (c *Client) CreateSecret(namespace string, secret map[string]any) (string, error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/secrets", namespace)
	data, status, err := c.doRequest("POST", path, secret, "application/json")
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("POST secret in %s: status %d: %s", namespace, status, string(data))
	}
	var created map[string]any
	if err := json.Unmarshal(data, &created); err != nil {
		return "", fmt.Errorf("unmarshal created secret: %w", err)
	}
	meta, _ := created["metadata"].(map[string]any)
	if meta == nil {
		return "", fmt.Errorf("created secret has no metadata")
	}
	name, _ := meta["name"].(string)
	return name, nil
}

// SetSecretOwner patches a Secret to add an ownerReference pointing to the
// given WorkflowWebhookRequest, so the Secret is garbage-collected with the WWR.
func (c *Client) SetSecretOwner(namespace, secretName string, wwr *types.WorkflowWebhookRequest) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, secretName)
	trueVal := true
	patch := map[string]any{
		"metadata": map[string]any{
			"ownerReferences": []map[string]any{
				{
					"apiVersion":         types.APIGroup + "/" + types.APIVersion,
					"kind":               "WorkflowWebhookRequest",
					"name":               wwr.Metadata.Name,
					"uid":                wwr.Metadata.UID,
					"controller":         &trueVal,
					"blockOwnerDeletion": &trueVal,
				},
			},
		},
	}
	return c.mergePatch(path, patch)
}

// --------------------------------------------------------------------------
// Jobs (batch/v1)
// --------------------------------------------------------------------------

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

// CreateJobRaw posts a prepared job map and returns the name assigned by the API server.
func (c *Client) CreateJobRaw(namespace string, job map[string]any) (string, error) {
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs", namespace)
	data, status, err := c.doRequest("POST", path, job, "application/json")
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("POST job in %s: status %d: %s", namespace, status, string(data))
	}
	var created map[string]any
	if err := json.Unmarshal(data, &created); err != nil {
		return "", fmt.Errorf("unmarshal created job: %w", err)
	}
	meta, _ := created["metadata"].(map[string]any)
	if meta == nil {
		return "", fmt.Errorf("created job has no metadata")
	}
	name, _ := meta["name"].(string)
	return name, nil
}

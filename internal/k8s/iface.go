package k8s

import "github.com/jlsalvador/simple-cicd/internal/types"

// ClientIface is the subset of the Kubernetes API used by the controller and
// webhook handler. Declaring it here (next to the real implementation) keeps
// the interface and the concrete type in sync and allows other packages to
// depend on the abstraction rather than the concrete *Client.
type ClientIface interface {
	// Workflow.

	GetWorkflow(namespace, name string) (*types.Workflow, error)

	// WorkflowWebhook.

	GetWorkflowWebhook(namespace, name string) (*types.WorkflowWebhook, error)

	// WorkflowWebhookRequest.

	CreateWWR(wwr *types.WorkflowWebhookRequest) (*types.WorkflowWebhookRequest, error)
	GetWWR(namespace, name string) (*types.WorkflowWebhookRequest, error)
	ListWWRs(namespace string) ([]types.WorkflowWebhookRequest, error)
	ListAllWWRs() ([]types.WorkflowWebhookRequest, error)
	UpdateWWRStatus(wwr *types.WorkflowWebhookRequest) error
	PatchWWRFinalizers(wwr *types.WorkflowWebhookRequest) error
	DeleteWWR(namespace, name string) error

	// Secrets.

	// CreateRequestSecret creates a Secret containing the base64-encoded HTTP
	// request data that triggered a WWR. The name is generated from
	// webhookName and the actual name assigned by the API server is returned.
	// The Secret has no ownerReference; the WWR cleanup finalizer deletes it.
	CreateRequestSecret(namespace, webhookName string, req types.WebhookRequestData) (string, error)

	// GetSecretData returns the raw data map of a Secret. Values are
	// base64-encoded strings, exactly as returned by the Kubernetes API.
	GetSecretData(namespace, name string) (map[string]string, error)

	// MirrorSecret copies the data of an existing Secret from srcNamespace/srcName
	// into dstNamespace/dstName, setting the given Job as its owner so the copy
	// is garbage-collected automatically when the Job is deleted.
	// Used to propagate the WWR request Secret into cross-namespace job pods.
	MirrorSecret(srcNamespace, srcName, dstNamespace, dstName, jobName, jobUID string) error

	// DeleteSecret deletes a Secret by namespace and name.
	// Returns nil if the Secret is already gone (404).
	DeleteSecret(namespace, name string) error

	// Jobs.

	GetJob(namespace, name string) (*types.Job, error)
	GetJobRaw(namespace, name string) (map[string]any, error)
	CreateJobRaw(namespace string, job map[string]any) (CreatedResource, error)
	DeleteJob(namespace, name string) error

	// Leases.

	GetLease(namespace, name string) (*types.Lease, error)
	CreateLease(namespace string, lease *types.Lease) (*types.Lease, error)
	UpdateLease(lease *types.Lease) (*types.Lease, error)
}

// Ensure *Client satisfies the interface at compile time.
var _ ClientIface = (*Client)(nil)

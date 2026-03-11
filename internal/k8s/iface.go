package k8s

import "github.com/jlsalvador/simple-cicd/internal/types"

// ClientIface is the subset of the Kubernetes API used by the controller and
// webhook handler. Declaring it here (next to the real implementation) keeps
// the interface and the concrete type in sync and allows other packages to
// depend on the abstraction rather than the concrete *Client.
type ClientIface interface {
	// Workflow
	GetWorkflow(namespace, name string) (*types.Workflow, error)

	// WorkflowWebhook
	GetWorkflowWebhook(namespace, name string) (*types.WorkflowWebhook, error)

	// WorkflowWebhookRequest
	CreateWWR(wwr *types.WorkflowWebhookRequest) (*types.WorkflowWebhookRequest, error)
	GetWWR(namespace, name string) (*types.WorkflowWebhookRequest, error)
	ListWWRs(namespace string) ([]types.WorkflowWebhookRequest, error)
	ListAllWWRs() ([]types.WorkflowWebhookRequest, error)
	UpdateWWRStatus(wwr *types.WorkflowWebhookRequest) error
	PatchWWRFinalizers(wwr *types.WorkflowWebhookRequest) error
	DeleteWWR(namespace, name string) error

	// Secrets
	CreateSecretForJob(namespace, secretName string, req types.WebhookRequestData, jobName, jobUID string) error

	// Jobs
	GetJob(namespace, name string) (*types.Job, error)
	GetJobRaw(namespace, name string) (map[string]any, error)
	CreateJobRaw(namespace string, job map[string]any) (CreatedResource, error)
	DeleteJob(namespace, name string) error
}

// Ensure *Client satisfies the interface at compile time.
var _ ClientIface = (*Client)(nil)

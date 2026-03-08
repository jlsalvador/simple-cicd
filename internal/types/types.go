package types

import "time"

const (
	// RequestSecretMountPath is where request data is mounted inside job pods.
	RequestSecretMountPath = "/var/run/secrets/kubernetes.io/request"

	// RequestSecretSuffix is appended to the WWR name to form the Secret name.
	RequestSecretSuffix = "-request"

	APIGroup   = "simple-cicd.jlsalvador.online"
	APIVersion = "v1alpha1"

	// Workflow next conditions
	WhenOnSuccess    = "OnSuccess"
	WhenOnAnySuccess = "OnAnySuccess"
	WhenOnFailure    = "OnFailure"
	WhenOnAnyFailure = "OnAnyFailure"
	WhenAlways       = "Always"

	// WorkflowWebhook concurrency policies
	ConcurrencyAllow   = "Allow"
	ConcurrencyForbid  = "Forbid"
	ConcurrencyReplace = "Replace"

	// Label keys used to link WWRs to their WorkflowWebhook
	LabelWebhookName      = APIGroup + "/webhook-name"
	LabelWebhookNamespace = APIGroup + "/webhook-namespace"

	// Label keys used to link cloned Jobs to their WWR
	LabelWWRName      = APIGroup + "/wwr"
	LabelWWRNamespace = APIGroup + "/wwr-ns"
)

// ObjectMeta mirrors the relevant Kubernetes ObjectMeta fields.
type ObjectMeta struct {
	Name            string            `json:"name,omitempty"`
	GenerateName    string            `json:"generateName,omitempty"`
	Namespace       string            `json:"namespace,omitempty"`
	UID             string            `json:"uid,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
	OwnerReferences []OwnerReference  `json:"ownerReferences,omitempty"`
}

// OwnerReference describes an owner resource.
type OwnerReference struct {
	APIVersion         string `json:"apiVersion"`
	Kind               string `json:"kind"`
	Name               string `json:"name"`
	UID                string `json:"uid"`
	Controller         *bool  `json:"controller,omitempty"`
	BlockOwnerDeletion *bool  `json:"blockOwnerDeletion,omitempty"`
}

// ResourceName is a name reference to a same-namespace resource.
type ResourceName struct {
	Name string `json:"name"`
}

// --------------------------------------------------------------------------
// Workflow
// --------------------------------------------------------------------------

type Workflow struct {
	APIVersion string       `json:"apiVersion,omitempty"`
	Kind       string       `json:"kind,omitempty"`
	Metadata   ObjectMeta   `json:"metadata"`
	Spec       WorkflowSpec `json:"spec"`
}

type WorkflowSpec struct {
	JobsToBeCloned []ResourceName `json:"jobsToBeCloned"`
	Next           []NextWorkflow `json:"next,omitempty"`
	Suspend        bool           `json:"suspend,omitempty"`
}

type NextWorkflow struct {
	Name string `json:"name"`
	When string `json:"when,omitempty"`
}

// --------------------------------------------------------------------------
// WorkflowWebhook
// --------------------------------------------------------------------------

type WorkflowWebhook struct {
	APIVersion string              `json:"apiVersion,omitempty"`
	Kind       string              `json:"kind,omitempty"`
	Metadata   ObjectMeta          `json:"metadata"`
	Spec       WorkflowWebhookSpec `json:"spec"`
}

type WorkflowWebhookSpec struct {
	ConcurrencyPolicy string         `json:"concurrencyPolicy,omitempty"`
	Suspend           bool           `json:"suspend,omitempty"`
	Workflows         []ResourceName `json:"workflows"`
}

// --------------------------------------------------------------------------
// WorkflowWebhookRequest
// --------------------------------------------------------------------------

type WorkflowWebhookRequest struct {
	APIVersion string                       `json:"apiVersion,omitempty"`
	Kind       string                       `json:"kind,omitempty"`
	Metadata   ObjectMeta                   `json:"metadata"`
	Spec       WorkflowWebhookRequestSpec   `json:"spec"`
	Status     WorkflowWebhookRequestStatus `json:"status,omitempty"`
}

type WorkflowWebhookRequestList struct {
	Items []WorkflowWebhookRequest `json:"items"`
}

// WorkflowWebhookRequestSpec stores the references needed to process the request.
// The actual HTTP request data (body, headers, host, method, url) lives in the
// Secret referenced by RequestSecret and is mounted into every cloned job pod at
// /var/run/secrets/kubernetes.io/request/.
type WorkflowWebhookRequestSpec struct {
	WorkflowWebhook ResourceName `json:"workflowWebhook"`
	// RequestSecret is the name of the Secret that holds the HTTP request data
	// that triggered this WorkflowWebhookRequest.
	RequestSecret ResourceName `json:"requestSecret"`
}

// WorkflowWebhookRequestStatus holds the observed state of a WWR.
type WorkflowWebhookRequestStatus struct {
	Conditions       []Condition    `json:"conditions,omitempty"`
	CurrentJobs      []ResourceName `json:"currentJobs,omitempty"`
	CurrentWorkflows []ResourceName `json:"currentWorkflows,omitempty"`
	Done             bool           `json:"done,omitempty"`
	FailedJobs       int            `json:"failedJobs,omitempty"`
	Steps            int            `json:"steps"`
	SuccessfulJobs   int            `json:"successfulJobs,omitempty"`
}

type Condition struct {
	LastTransitionTime time.Time `json:"lastTransitionTime"`
	Message            string    `json:"message"`
	Reason             string    `json:"reason"`
	Status             string    `json:"status"`
	Type               string    `json:"type"`
}

// --------------------------------------------------------------------------
// Job (batch/v1) - only the fields we need to inspect
// --------------------------------------------------------------------------

type Job struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Status     JobStatus  `json:"status,omitempty"`
}

type JobStatus struct {
	Active     int32          `json:"active,omitempty"`
	Succeeded  int32          `json:"succeeded,omitempty"`
	Failed     int32          `json:"failed,omitempty"`
	Conditions []JobCondition `json:"conditions,omitempty"`
}

type JobCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

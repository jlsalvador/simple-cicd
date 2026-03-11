package types

import "time"

const (
	// RequestSecretMountPath is where request data is mounted inside job pods.
	RequestSecretMountPath = "/var/run/secrets/kubernetes.io/request"

	APIGroup   = "simple-cicd.jlsalvador.online"
	APIVersion = "v1alpha1"

	// FinalizerCleanup is added to every WWR so the reconciler can explicitly
	// delete cross-namespace jobs before the WWR itself is removed.
	//
	// Kubernetes GC cannot follow cross-namespace ownerReferences, so this
	// finalizer ensures those jobs are never orphaned.
	FinalizerCleanup = APIGroup + "/cleanup"

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
	Name              string            `json:"name,omitempty"`
	GenerateName      string            `json:"generateName,omitempty"`
	Namespace         string            `json:"namespace,omitempty"`
	UID               string            `json:"uid,omitempty"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	OwnerReferences   []OwnerReference  `json:"ownerReferences,omitempty"`
	Finalizers        []string          `json:"finalizers,omitempty"`
	DeletionTimestamp *time.Time        `json:"deletionTimestamp,omitempty"`
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

// ResourceName is a name reference to a resource, optionally in another namespace.
// When Namespace is empty the consumer falls back to the WWR's own namespace.
type ResourceName struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
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
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	When      string `json:"when,omitempty"`
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

// WebhookRequestData holds the HTTP request fields that triggered a
// WorkflowWebhookRequest.
//
// All string values are base64-encoded so they can be
// stored verbatim in the CRD and copied into a Secret at job-clone time.
//
// Timestamp is a Unix epoch (seconds since 1970-01-01 UTC), base64-encoded.
// Example decoded value: "1735689600"
type WebhookRequestData struct {
	Body       string `json:"body"`
	Headers    string `json:"headers"`
	Host       string `json:"host"`
	Method     string `json:"method"`
	URL        string `json:"url"`
	RemoteAddr string `json:"remoteAddr"`
	Timestamp  string `json:"timestamp"`
	UserAgent  string `json:"userAgent"`
}

// WorkflowWebhookRequestSpec stores the references needed to process the request.
// The HTTP request data is embedded directly so that jobs can be cloned into any
// namespace without needing cross-namespace Secret access.
type WorkflowWebhookRequestSpec struct {
	WorkflowWebhook ResourceName       `json:"workflowWebhook"`
	Request         WebhookRequestData `json:"request"`
}

// WorkflowWebhookRequestStatus holds the observed state of a WWR.
type WorkflowWebhookRequestStatus struct {
	Conditions []Condition `json:"conditions,omitempty"`
	// AllJobs accumulates every job ref ever cloned for this WWR, across all
	// namespaces. Used by the finalizer handler to explicitly delete
	// cross-namespace jobs that Kubernetes GC cannot reach.
	AllJobs          []ResourceName `json:"allJobs,omitempty"`
	CurrentJobs      []ResourceName `json:"currentJobs,omitempty"`
	CurrentWorkflows []ResourceName `json:"currentWorkflows,omitempty"`
	Done             bool           `json:"done,omitempty"`
	FailedJobs       int            `json:"failedJobs,omitempty"`
	Steps            int            `json:"steps"`
	SuccessfulJobs   int            `json:"successfulJobs,omitempty"`
	// StartTime is when the first reconciliation step began (Steps goes 0 → 1).
	StartTime *time.Time `json:"startTime,omitempty"`
	// CompletionTime is when the WWR was marked done.
	CompletionTime *time.Time `json:"completionTime,omitempty"`
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

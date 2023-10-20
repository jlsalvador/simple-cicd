/*
Copyright 2023 José Luis Salvador Rufo <salvador.joseluis@gmail.com>.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"maps"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ConditionType string

const (
	WorkflowWebhookRequestProgressing ConditionType = "Progressing"
	WorkflowWebhookRequestWaiting     ConditionType = "Waiting"
	WorkflowWebhookRequestDone        ConditionType = "Done"
)

// WorkflowWebhookRequestSpec defines the desired state of WorkflowWebhookRequest
type WorkflowWebhookRequestSpec struct {
	// Reference to the WorkflowWebhook.
	// +required
	WorkflowWebhook NamespacedName `json:"workflowWebhook"`

	// Specifies the host on which the URL is sought.
	// +optional
	Host string `json:"host,omitempty"`

	// Specifies the HTTP method (GET, POST, PUT, etc.).
	// +optional
	Method string `json:"method,omitempty"`

	// Specifies the URI being requested.
	// +optional
	Url string `json:"url,omitempty"`

	// Contains headers for the HTTP request.
	// +optional
	Headers map[string][]string `json:"headers,omitempty"`

	// Body of the HTTP request as Base64 encoded data.
	// The serialized form of the Body is a base64 encoded string,
	// representing the arbitrary (possibly non-string) data value.
	// Described in https://tools.ietf.org/html/rfc4648#section-4
	// +optional
	Body []byte `json:"body,omitempty" protobuf:"bytes,2,rep,name=body"`
}

type ConditionStatus string

// These are valid condition statuses. "ConditionTrue" means a resource is in the condition.
// "ConditionFalse" means a resource is not in the condition. "ConditionUnknown" means kubernetes
// can't decide if a resource is in the condition or not.
const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

type Condition struct {
	Type               string          `json:"type"`
	Status             ConditionStatus `json:"status"`
	Reason             string          `json:"reason"`
	Message            string          `json:"message"`
	LastTransitionTime metav1.Time     `json:"lastTransitionTime"`
}

// WorkflowWebhookRequestStatus defines the observed state of a WorkflowWebhookRequest.
type WorkflowWebhookRequestStatus struct {
	// List of Jobs currently running associated with the current WorkflowWebhookRequest.
	// +optional
	CurrentJobs []NamespacedName `json:"currentJobs,omitempty"`

	// List of Workflows currently triggered by this WorkflowWebhookRequest.
	// +optional
	CurrentWorkflows []NamespacedName `json:"currentWorkflows,omitempty"`

	// Count of how many times CurrentWorkflows have been done.
	Iterations int `json:"iterations"`

	// Number of successful Jobs owned by this WorkflowWebhookRequest.
	// +optional
	SuccessfulJobs int `json:"successfulJobs"`

	// Number of failed Jobs owned by this WorkflowWebhookRequest.
	// +optional
	FailedJobs int `json:"failedJobs"`

	// When set to true, instructs the operator to skip
	// this WorkflowWebhookRequest during future reconciliations.
	// +optional
	Done bool `json:"done"`

	// Represents the observations of a WorkflowWebhookRequestStatus's current state.
	// WorkflowWebhookRequestStatus.Status.Conditions.Type are: "Progressing", "Waiting", "Done"
	// WorkflowWebhookRequestStatus.Status.Conditions.Status are one of True, False, Unknown.
	// WorkflowWebhookRequestStatus.Status.Conditions.Reason the value should be a CamelCase string and producers of specific
	// condition types may define expected values and meanings for this field, and whether the values
	// are considered a guaranteed API.
	// WorkflowWebhookRequestStatus.Status.Conditions.Message is a human readable message indicating details about the transition.
	// For further information see: https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties
	//
	// Conditions store the status conditions of the WorkflowWebhookRequestStatus instances
	Conditions []Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true

// WorkflowWebhookRequest is the Schema for the workflowwebhookrequests API
// +kubebuilder:resource:shortName=wwr
// +kubebuilder:printcolumn:name="Done",type="boolean",JSONPath=`.status.done`,description="True when there are not more Jobs to do"
// +kubebuilder:printcolumn:name="Iterations",type=string,JSONPath=`.status.iterations`,priority=1,description="How many times CurrentWorkflows have been done"
// +kubebuilder:printcolumn:name="Successful Jobs",type=string,JSONPath=`.status.successfulJobs`,description="Total number of sucessful Jobs"
// +kubebuilder:printcolumn:name="Failed Jobs",type=string,JSONPath=`.status.failedJobs`,description="Total number of failed Jobs"
// +kubebuilder:printcolumn:name="Current Jobs",type=string,JSONPath=`.status.currentJobs`,priority=1,description="Waiting for these Jobs to be done"
type WorkflowWebhookRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkflowWebhookRequestSpec   `json:"spec,omitempty"`
	Status WorkflowWebhookRequestStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// WorkflowWebhookRequestList contains a list of WorkflowWebhookRequest
type WorkflowWebhookRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkflowWebhookRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WorkflowWebhookRequest{}, &WorkflowWebhookRequestList{})
}

func GetWorkflowWebhookRequestLabels(ww *WorkflowWebhook) map[string]string {
	labels := map[string]string{
		LabelWorkflowWebhookNamespace: ww.Namespace,
		LabelWorkflowWebhookName:      ww.Name,
	}
	maps.Copy(labels, getBaseLabels())
	return labels
}

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkflowWebhookSpec defines the desired state of WorkflowWebhook
type WorkflowWebhookSpec struct {
	// +required
	Workflows []NamespacedName `json:"workflows"`

	// Defaults to false
	// +optional
	Suspend *bool `json:"suspend,omitempty" protobuf:"varint,10,opt,name=suspend"`
}

//+kubebuilder:object:root=true

// WorkflowWebhook is the Schema for the workflowwebhooks API
// +kubebuilder:resource:shortName=ww
type WorkflowWebhook struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec WorkflowWebhookSpec `json:"spec,omitempty"`
}

//+kubebuilder:object:root=true

// WorkflowWebhookList contains a list of WorkflowWebhook
type WorkflowWebhookList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkflowWebhook `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WorkflowWebhook{}, &WorkflowWebhookList{})
}

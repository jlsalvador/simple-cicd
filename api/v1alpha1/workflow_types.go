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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Describes the conditions for when a Job will be handled.
// If not specified, the default behavior is "Always".
//
// Possible statuses:
//   - OnSuccess: The Job will be handled when all previous Jobs were successful.
//   - OnAnySuccess: The Job will be handled when any previous Job was successful.
//   - OnFailure: The Job will be handled when all previous Jobs were not successful.
//   - OnAnyFailure: The Job will be handled when any previous Job was not successful.
//   - Always: The Job will always be handled.
//
// +kubebuilder:validation:Enum=OnSuccess;OnAnySuccess;OnFailure;OnAnyFailure;Always
type When string

const (
	// Forbids a Job to be executed when any previous Job was not successful.
	OnSuccess When = "OnSuccess"

	// Allows a Job to be executed when any previous Job was successful.
	OnAnySuccess When = "OnAnySuccess"

	// Forbids a Job to be executed when any previous Job was successful.
	OnFailure When = "OnFailure"

	// Allows a Job to be executed when any previous Job was not successful.
	OnAnyFailure When = "OnAnyFailure"

	// Always allows a Job to be executed.
	Always When = "Always"
)

type NextWorkflow struct {
	// Workflow namespace
	// +optional
	Namespace *string `json:"namespace,omitempty"`

	// Workflow name
	// +required
	Name string `json:"name"`

	// Describes the conditions for when a Job will be handled.
	// If not specified, the default behavior is "Always".
	//
	// Possible statuses:
	//   - OnSuccess: The Job will be handled when all previous Jobs were successful.
	//   - OnAnySuccess: The Job will be handled when any previous Job was successful.
	//   - OnFailure: The Job will be handled when all previous Jobs were not successful.
	//   - OnAnyFailure: The Job will be handled when any previous Job was not successful.
	//   - Always: The Job will always be handled.
	//
	// +kubebuilder:default="Always"
	// +optional
	When *When `json:"when,omitempty"`
}

func (nw NextWorkflow) String() string {
	when := Always
	if nw.When != nil {
		when = *nw.When
	}
	if nw.Namespace != nil {
		return fmt.Sprintf("%s/%s@%s", *nw.Namespace, nw.Name, when)
	}
	return fmt.Sprintf("%s@%s", nw.Name, when)
}

func (nw NextWorkflow) AsNamespacedName() NamespacedName {
	return NamespacedName{
		Namespace: nw.Namespace,
		Name:      nw.Name,
	}
}

// WorkflowSpec defines the desired state of Workflow
type WorkflowSpec struct {
	// Jobs to be cloned
	// +required
	JobsToBeCloned []NamespacedName `json:"jobsToBeCloned"`

	// Optional list of Workflow to execute next
	// +optional
	Next []NextWorkflow `json:"next,omitempty"`

	// When is true, the execution of this workflow will be skipped, disabling
	// the ability to create new Jobs referred to by this Workflow.
	// Defaults to false.
	//
	// +kubebuilder:default=false
	// +optional
	Suspend *bool `json:"suspend,omitempty" protobuf:"varint,10,opt,name=suspend"`
}

//+kubebuilder:object:root=true

// Workflow is the Schema for the workflows API
// +kubebuilder:resource:shortName=w
// +kubebuilder:printcolumn:name="Suspend",type="boolean",JSONPath=`.spec.suspend`,description="When is true, the execution of this workflow will be skipped."
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type Workflow struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec WorkflowSpec `json:"spec,omitempty"`
}

//+kubebuilder:object:root=true

// WorkflowList contains a list of Workflow
type WorkflowList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Workflow `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Workflow{}, &WorkflowList{})
}

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
	"reflect"
	"testing"
	"time"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetLatestConditionByType(t *testing.T) {
	conditionFirst := Condition{
		Type:               string(WorkflowWebhookRequestProgressing),
		Status:             "Unknown",
		Reason:             "Reconciling",
		Message:            "Starting reconciliation",
		LastTransitionTime: v1.NewTime(time.Date(2024, 02, 25, 13, 55, 54, 0, time.UTC)),
	}
	conditionSecond := Condition{
		Type:               string(WorkflowWebhookRequestWaiting),
		Status:             "Unknown",
		Reason:             "Reconciling",
		Message:            "Waiting for current jobs",
		LastTransitionTime: v1.NewTime(time.Date(2024, 02, 25, 13, 55, 54, 10, time.UTC)),
	}
	conditionThird := Condition{
		Type:               string(WorkflowWebhookRequestProgressing),
		Status:             "Unknown",
		Reason:             "Reconciling",
		Message:            "On interation 1, there were 1 successful Job(s) and 0 failures, with 0 Workflow(s) queued.",
		LastTransitionTime: v1.NewTime(time.Date(2024, 02, 25, 13, 56, 13, 10, time.UTC)),
	}
	conditionFourth := Condition{
		Type:               string(WorkflowWebhookRequestDone),
		Status:             "True",
		Reason:             "Reconciling",
		Message:            "All Jobs are completed. There were 1 successful Job(s) and 0 failures over 1 step(s).",
		LastTransitionTime: v1.NewTime(time.Date(2024, 02, 25, 13, 56, 13, 10, time.UTC)),
	}
	conditions := []Condition{conditionFirst, conditionSecond, conditionThird, conditionFourth}

	type args struct {
		conditions    []Condition
		conditionType ConditionType
	}
	tests := []struct {
		name string
		args args
		want *Condition
	}{
		{
			name: "ok",
			args: args{
				conditions:    conditions,
				conditionType: WorkflowWebhookRequestDone,
			},
			want: &conditionFourth,
		},
		{
			name: "not-found",
			args: args{
				conditions:    conditions,
				conditionType: "Not found",
			},
			want: nil,
		},
		{
			name: "last-progressing",
			args: args{
				conditions:    conditions,
				conditionType: WorkflowWebhookRequestProgressing,
			},
			want: &conditionThird,
		},
		{
			name: "empty-slice",
			args: args{
				conditions:    []Condition{},
				conditionType: WorkflowWebhookRequestDone,
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetLatestConditionByType(tt.args.conditions, tt.args.conditionType); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetLatestConditionByType() = %v, want %v", got, tt.want)
			}
		})
	}
}

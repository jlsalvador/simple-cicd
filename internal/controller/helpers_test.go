package controller

// helpers_test.go – shared builder helpers and fixtures.

import (
	"github.com/jlsalvador/simple-cicd/internal/types"
)

// --------------------------------------------------------------------------
// Resource builders
// --------------------------------------------------------------------------

func makeWWR(ns, name, webhookName string) *types.WorkflowWebhookRequest {
	return &types.WorkflowWebhookRequest{
		APIVersion: types.APIGroup + "/" + types.APIVersion,
		Kind:       "WorkflowWebhookRequest",
		Metadata: types.ObjectMeta{
			Namespace: ns,
			Name:      name,
			UID:       "wwr-uid-" + name,
		},
		Spec: types.WorkflowWebhookRequestSpec{
			WorkflowWebhook: types.ResourceName{Name: webhookName},
			Request:         makeRequestData(),
		},
	}
}

func makeWebhook(ns, name string, workflows []types.ResourceName) *types.WorkflowWebhook {
	return &types.WorkflowWebhook{
		Metadata: types.ObjectMeta{Namespace: ns, Name: name},
		Spec: types.WorkflowWebhookSpec{
			Workflows: workflows,
		},
	}
}

func makeWebhookWithPolicy(ns, name, policy string, workflows []types.ResourceName) *types.WorkflowWebhook {
	wh := makeWebhook(ns, name, workflows)
	wh.Spec.ConcurrencyPolicy = policy
	return wh
}

func makeSuspendedWebhook(ns, name string) *types.WorkflowWebhook {
	return &types.WorkflowWebhook{
		Metadata: types.ObjectMeta{Namespace: ns, Name: name},
		Spec:     types.WorkflowWebhookSpec{Suspend: true, Workflows: nil},
	}
}

func makeWorkflow(ns, name string, jobs []types.ResourceName, next ...types.NextWorkflow) *types.Workflow {
	return &types.Workflow{
		Metadata: types.ObjectMeta{Namespace: ns, Name: name},
		Spec: types.WorkflowSpec{
			JobsToBeCloned: jobs,
			Next:           next,
		},
	}
}

func makeSuspendedWorkflow(ns, name string) *types.Workflow {
	return &types.Workflow{
		Metadata: types.ObjectMeta{Namespace: ns, Name: name},
		Spec:     types.WorkflowSpec{Suspend: true, JobsToBeCloned: nil},
	}
}

// minimalJobRaw returns the smallest valid raw job map that prepareJobForCloning
// can process without panicking.
func minimalJobRaw(name string) map[string]any {
	return map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata": map[string]any{
			"name":      name,
			"namespace": "default",
		},
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{},
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":  "main",
							"image": "busybox",
						},
					},
					"restartPolicy": "Never",
				},
			},
		},
	}
}

func makeRequestData() types.WebhookRequestData {
	return types.WebhookRequestData{
		Body:       "e30=", // base64("{}")
		Headers:    "e30=",
		Host:       "bG9jYWxob3N0",     // base64("localhost")
		Method:     "UE9TVA==",         // base64("POST")
		URL:        "Lw==",             // base64("/")
		RemoteAddr: "MTI3LjAuMC4x",     // base64("127.0.0.1")
		Timestamp:  "MTczNTY4OTYwMA==", // base64("1735689600")
	}
}

func ref(name string) types.ResourceName       { return types.ResourceName{Name: name} }
func refNS(ns, name string) types.ResourceName { return types.ResourceName{Namespace: ns, Name: name} }

// succeededStatus returns a JobStatus representing a successfully completed job.
func succeededStatus() types.JobStatus {
	return types.JobStatus{
		Active:    0,
		Succeeded: 1,
		Conditions: []types.JobCondition{
			{Type: "Complete", Status: "True"},
		},
	}
}

// failedStatus returns a JobStatus representing a permanently failed job.
func failedStatus() types.JobStatus {
	return types.JobStatus{
		Active: 0,
		Failed: 1,
		Conditions: []types.JobCondition{
			{Type: "Failed", Status: "True"},
		},
	}
}

// runningStatus returns a JobStatus representing a still-running job.
func runningStatus() types.JobStatus {
	return types.JobStatus{Active: 1}
}

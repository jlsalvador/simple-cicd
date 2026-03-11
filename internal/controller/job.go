package controller

import (
	"encoding/json"
	"log"

	"github.com/jlsalvador/simple-cicd/internal/types"
)

// jobSucceeded returns true when the job has completed successfully.
func jobSucceeded(job *types.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == "Complete" && c.Status == "True" {
			return true
		}
	}
	return job.Status.Active == 0 && job.Status.Succeeded > 0
}

// jobFailed returns true when the job has permanently failed.
func jobFailed(job *types.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == "Failed" && c.Status == "True" {
			return true
		}
	}
	return job.Status.Active == 0 && job.Status.Failed > 0 && job.Status.Succeeded == 0
}

// conditionMet evaluates a `when` condition against the step's job results.
// stepSucceeded and stepFailed are the counts for the step just completed.
//
//	OnSuccess    - no failures (all jobs were successful, including zero jobs)
//	OnAnySuccess - at least one success
//	OnFailure    - no successes (all jobs failed)
//	OnAnyFailure - at least one failure
//	Always       - always true (default when field is omitted)
func conditionMet(when string, succeeded, failed int) bool {
	switch when {
	case types.WhenOnSuccess, "":
		return failed == 0
	case types.WhenOnAnySuccess:
		return succeeded > 0
	case types.WhenOnFailure:
		return succeeded == 0
	case types.WhenOnAnyFailure:
		return failed > 0
	case types.WhenAlways:
		return true
	default:
		log.Printf("[reconciler] unknown when condition %q - defaulting to true", when)
		return true
	}
}

// prepareJobForCloning deep-copies a raw job map, strips all server-assigned
// fields, and injects the pre-generated request Secret as a volume.
func prepareJobForCloning(raw map[string]any, wwr *types.WorkflowWebhookRequest, secretName string, setOwnerRef bool) map[string]any {
	// Deep copy via round-trip JSON.
	data, _ := json.Marshal(raw)
	var cloned map[string]any
	_ = json.Unmarshal(data, &cloned)

	// --- Metadata ---
	meta, _ := cloned["metadata"].(map[string]any)
	if meta == nil {
		meta = make(map[string]any)
	}

	originalName, _ := meta["name"].(string)

	// Clear server-managed metadata.
	for _, field := range []string{
		"name", "uid", "resourceVersion", "creationTimestamp",
		"selfLink", "managedFields", "generation",
	} {
		delete(meta, field)
	}

	// Strip last-applied-configuration annotation to avoid stale data
	if annots, ok := meta["annotations"].(map[string]any); ok {
		delete(annots, "kubectl.kubernetes.io/last-applied-configuration")
		if len(annots) == 0 {
			delete(meta, "annotations")
		}
	}

	meta["generateName"] = originalName + "-"

	// Add tracking labels.
	labels, _ := meta["labels"].(map[string]any)
	if labels == nil {
		labels = make(map[string]any)
	}
	labels[types.LabelWWRName] = wwr.Metadata.Name
	labels[types.LabelWWRNamespace] = wwr.Metadata.Namespace
	meta["labels"] = labels

	// Only set ownerReference when the job is in the same namespace as the WWR.
	// Kubernetes GC resolves ownerReferences within a single namespace only;
	// a cross-namespace reference is treated as absent and causes the GC to
	// delete the dependent immediately after creation.
	// Cross-namespace jobs are cleaned up by the WWR's finalizer instead.
	if setOwnerRef {
		meta["ownerReferences"] = []map[string]any{
			{
				"apiVersion":         types.APIGroup + "/" + types.APIVersion,
				"kind":               "WorkflowWebhookRequest",
				"name":               wwr.Metadata.Name,
				"uid":                wwr.Metadata.UID,
				"controller":         new(true),
				"blockOwnerDeletion": new(true),
			},
		}
	}
	cloned["metadata"] = meta

	// --- Spec ---
	// Remove the auto-generated selector so Kubernetes regenerates it.
	if spec, ok := cloned["spec"].(map[string]any); ok {
		delete(spec, "selector")

		// Remove labels added by the previous job controller from the pod
		// template so a new selector gets generated cleanly.
		if tmpl, ok := spec["template"].(map[string]any); ok {
			if tmplMeta, ok := tmpl["metadata"].(map[string]any); ok {
				if tmplLabels, ok := tmplMeta["labels"].(map[string]any); ok {
					for _, key := range []string{
						"controller-uid",
						"batch.kubernetes.io/controller-uid",
						"job-name",
						"batch.kubernetes.io/job-name",
					} {
						delete(tmplLabels, key)
					}
				}
			}
		}
	}

	// Inject the per-job request Secret as a volume.
	// The Secret may not exist yet when the Job is submitted; the pod will
	// remain Pending until it appears (created immediately after this call).
	if spec, ok := cloned["spec"].(map[string]any); ok {
		if tmpl, ok := spec["template"].(map[string]any); ok {
			tmplSpec, _ := tmpl["spec"].(map[string]any)
			if tmplSpec == nil {
				tmplSpec = make(map[string]any)
				tmpl["spec"] = tmplSpec
			}

			// Append the secret volume.
			volumes, _ := tmplSpec["volumes"].([]any)
			volumes = append(volumes, map[string]any{
				"name": "request",
				"secret": map[string]any{
					"secretName": secretName,
				},
			})
			tmplSpec["volumes"] = volumes

			// Add the volumeMount to every container in the pod.
			if containers, ok := tmplSpec["containers"].([]any); ok {
				for i, c := range containers {
					container, _ := c.(map[string]any)
					if container == nil {
						continue
					}
					mounts, _ := container["volumeMounts"].([]any)
					mounts = append(mounts, map[string]any{
						"name":      "request",
						"mountPath": types.RequestSecretMountPath,
						"readOnly":  true,
					})
					container["volumeMounts"] = mounts
					containers[i] = container
				}
				tmplSpec["containers"] = containers
			}
		}
	}

	// Ensure the cloned job is not suspended - the original may have
	// suspend: true to prevent accidental direct execution.
	if spec, ok := cloned["spec"].(map[string]any); ok {
		spec["suspend"] = false
	}

	// Remove status - it is set by the server.
	delete(cloned, "status")

	return cloned
}

package controller

import (
	"fmt"
	"log"
	"slices"

	"github.com/jlsalvador/simple-cicd/internal/types"
)

// addFinalizer appends FinalizerCleanup to the WWR and persists it.
func (r *Reconciler) addFinalizer(wwr *types.WorkflowWebhookRequest) error {
	wwr.Metadata.Finalizers = append(wwr.Metadata.Finalizers, types.FinalizerCleanup)
	if err := r.client.PatchWWRFinalizers(wwr); err != nil {
		return fmt.Errorf("adding finalizer to %s/%s: %w",
			wwr.Metadata.Namespace, wwr.Metadata.Name, err)
	}
	log.Printf("[reconciler] %s/%s: finalizer added", wwr.Metadata.Namespace, wwr.Metadata.Name)
	return nil
}

// handleDeletion is called when the WWR's DeletionTimestamp is set.
//
// It deletes every job that was cloned into a namespace other than the
// WWR's own namespace (same-namespace jobs are garbage-collected by
// Kubernetes via ownerReferences). Once done it removes the finalizer
// so Kubernetes can proceed with the actual deletion.
//
// Note: cross-namespace ownerReferences are disallowed by design in
// Kubernetes (docs.k8s.io/concepts/architecture/garbage-collection).
//
// A namespaced owner must exist in the same namespace as the dependent;
// otherwise the GC treats the reference as absent and may delete the
// dependent unexpectedly. We therefore manage cross-namespace cleanup
// ourselves via this finalizer.
func (r *Reconciler) handleDeletion(wwr *types.WorkflowWebhookRequest) error {
	log.Printf("[reconciler] %s/%s: running cleanup finalizer (%d total jobs)",
		wwr.Metadata.Namespace, wwr.Metadata.Name, len(wwr.Status.AllJobs))

	var deleteErr error
	for _, jobRef := range wwr.Status.AllJobs {
		jobNamespace := jobRef.Namespace
		if jobNamespace == "" {
			jobNamespace = wwr.Metadata.Namespace
		}
		// Same-namespace jobs: ownerReferences GC handles them; skip.
		if jobNamespace == wwr.Metadata.Namespace {
			continue
		}
		log.Printf("[reconciler] %s/%s: deleting cross-namespace job %s/%s",
			wwr.Metadata.Namespace, wwr.Metadata.Name, jobNamespace, jobRef.Name)
		if err := r.client.DeleteJob(jobNamespace, jobRef.Name); err != nil {
			log.Printf("[reconciler] error deleting job %s/%s: %v", jobNamespace, jobRef.Name, err)
			deleteErr = err // continue trying the rest; report at the end.
		}
	}
	if deleteErr != nil {
		return fmt.Errorf("one or more cross-namespace jobs could not be deleted for %s/%s",
			wwr.Metadata.Namespace, wwr.Metadata.Name)
	}

	// Remove the finalizer so Kubernetes can complete the deletion.
	wwr.Metadata.Finalizers = removeFinalizer(wwr.Metadata.Finalizers, types.FinalizerCleanup)
	if err := r.client.PatchWWRFinalizers(wwr); err != nil {
		return fmt.Errorf("removing finalizer from %s/%s: %w",
			wwr.Metadata.Namespace, wwr.Metadata.Name, err)
	}
	log.Printf("[reconciler] %s/%s: finalizer removed, deletion unblocked",
		wwr.Metadata.Namespace, wwr.Metadata.Name)
	return nil
}

// hasFinalizer checks if the given finalizer name is present in the
// WorkflowWebhookRequest's finalizers.
func hasFinalizer(wwr *types.WorkflowWebhookRequest, name string) bool {
	return slices.Contains(wwr.Metadata.Finalizers, name)
}

// removeFinalizer removes the specified finalizer from the list of finalizers.
func removeFinalizer(finalizers []string, name string) []string {
	return slices.DeleteFunc(finalizers, func(f string) bool {
		return f == name
	})
}

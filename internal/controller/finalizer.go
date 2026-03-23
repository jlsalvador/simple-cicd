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
// It performs three cleanup steps before removing the finalizer:
//
//  1. Deletes the request Secret in the WWR's own namespace. This Secret has
//     no ownerReference (to avoid the creation-order chicken-and-egg problem),
//     so we delete it explicitly here.
//
//  2. Deletes every job that was cloned into a namespace other than the WWR's
//     own namespace. Same-namespace jobs are garbage-collected by Kubernetes
//     via ownerReferences; cross-namespace ones are not.
//
//     Note: cross-namespace ownerReferences are disallowed by design in
//     Kubernetes (docs.k8s.io/concepts/architecture/garbage-collection):
//     a namespaced owner must exist in the same namespace as the dependent;
//     otherwise the GC treats the reference as absent and may delete the
//     dependent unexpectedly. We therefore manage cross-namespace cleanup
//     ourselves via this finalizer.
//
//  3. Removes the finalizer so Kubernetes can proceed with the actual deletion.
func (r *Reconciler) handleDeletion(wwr *types.WorkflowWebhookRequest) error {
	log.Printf("[reconciler] %s/%s: running cleanup finalizer (request secret: %q, %d total jobs)",
		wwr.Metadata.Namespace, wwr.Metadata.Name,
		wwr.Spec.RequestSecret.Name, len(wwr.Status.AllJobs))

	// Step 1: delete the request Secret.
	// The Secret lives in the WWR's namespace (RequestSecret.Namespace is
	// empty when it equals the WWR namespace, per the handler contract).
	if name := wwr.Spec.RequestSecret.Name; name != "" {
		ns := wwr.Spec.RequestSecret.Namespace
		if ns == "" {
			ns = wwr.Metadata.Namespace
		}
		if err := r.client.DeleteSecret(ns, name); err != nil {
			// DeleteSecret already swallows 404s, so any error here is real.
			// Log and continue: failing to delete the Secret should not block
			// the WWR deletion.
			log.Printf("[reconciler] %s/%s: error deleting request secret %s/%s: %v",
				wwr.Metadata.Namespace, wwr.Metadata.Name, ns, name, err)
		} else {
			log.Printf("[reconciler] %s/%s: request secret %s/%s deleted",
				wwr.Metadata.Namespace, wwr.Metadata.Name, ns, name)
		}
	}

	// Step 2: delete cross-namespace jobs.
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

	// Step 3: remove the finalizer so Kubernetes can complete the deletion.
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

package controller

import (
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/jlsalvador/simple-cicd/internal/types"
)

// initialize sets up a brand-new WWR (Steps == 0) by fetching the
// associated WorkflowWebhook and checking if it is suspended.
// If it is, mark the WWR as done.
// Otherwise, proceed to the next step.
func (r *Reconciler) initialize(wwr *types.WorkflowWebhookRequest) error {
	webhook, err := r.client.GetWorkflowWebhook(wwr.Metadata.Namespace, wwr.Spec.WorkflowWebhook.Name)
	if err != nil {
		return fmt.Errorf("getting WorkflowWebhook %s/%s: %w", wwr.Metadata.Namespace, wwr.Spec.WorkflowWebhook.Name, err)
	}

	if webhook.Spec.Suspend {
		log.Printf("[reconciler] %s/%s: WorkflowWebhook is suspended - marking done",
			wwr.Metadata.Namespace, wwr.Metadata.Name)
		return r.markDone(wwr, "Suspended", "WorkflowWebhook is suspended")
	}

	// Honour concurrency policy.
	if err := r.applyConcurrencyPolicy(wwr, webhook); err != nil {
		return err
	}
	// applyConcurrencyPolicy may have marked the WWR as done (Forbid).
	if wwr.Status.Done {
		return nil
	}

	// Resolve and clone jobs for each initial workflow.
	currentWorkflows, currentJobs, err := r.resolveAndClone(wwr, webhook.Spec.Workflows)
	if err != nil {
		return err
	}

	if len(currentWorkflows) == 0 {
		log.Printf("[reconciler] %s/%s: no active workflows - marking done",
			wwr.Metadata.Namespace, wwr.Metadata.Name)
		wwr.Status.Steps = 1
		return r.markDone(wwr, "NoWorkflows", "no active workflows to execute")
	}

	wwr.Status.CurrentWorkflows = currentWorkflows
	wwr.Status.CurrentJobs = currentJobs
	wwr.Status.AllJobs = append(wwr.Status.AllJobs, currentJobs...)
	wwr.Status.Steps = 1
	now := time.Now().UTC()
	wwr.Status.StartTime = &now

	log.Printf("[reconciler] %s/%s: initialised - %d workflow(s), %d job(s) cloned",
		wwr.Metadata.Namespace, wwr.Metadata.Name, len(currentWorkflows), len(currentJobs))
	return r.client.UpdateWWRStatus(wwr)
}

func (r *Reconciler) applyConcurrencyPolicy(
	wwr *types.WorkflowWebhookRequest,
	webhook *types.WorkflowWebhook,
) error {
	policy := webhook.Spec.ConcurrencyPolicy
	if policy == "" || policy == types.ConcurrencyAllow {
		return nil
	}

	running, err := r.runningWWRsForWebhook(wwr.Metadata.Namespace, wwr.Spec.WorkflowWebhook.Name, wwr.Metadata.Name)
	if err != nil {
		return err
	}

	switch policy {
	case types.ConcurrencyForbid:
		if len(running) > 0 {
			log.Printf("[reconciler] %s/%s: Forbid - another WWR is running, marking done",
				wwr.Metadata.Namespace, wwr.Metadata.Name)
			return r.markDone(wwr, "Forbidden", "another WorkflowWebhookRequest is still running")
		}

	case types.ConcurrencyReplace:
		for _, old := range running {
			log.Printf("[reconciler] %s/%s: Replace - deleting old WWR %s/%s",
				wwr.Metadata.Namespace, wwr.Metadata.Name, old.Metadata.Namespace, old.Metadata.Name)
			if delErr := r.client.DeleteWWR(old.Metadata.Namespace, old.Metadata.Name); delErr != nil {
				log.Printf("[reconciler] error deleting old WWR %s/%s: %v",
					old.Metadata.Namespace, old.Metadata.Name, delErr)
			}
		}
	}
	return nil
}

// checkProgress checks the progress of the current jobs associated with the
// WorkflowWebhookRequest (Steps > 0). If all jobs have completed, it advances
// the WorkflowWebhookRequest to the next phase.
func (r *Reconciler) checkProgress(wwr *types.WorkflowWebhookRequest) error {
	if len(wwr.Status.CurrentJobs) == 0 {
		// No jobs to wait for; try to advance immediately.
		return r.advance(wwr, 0, 0)
	}

	stepSucceeded, stepFailed, stillActive := 0, 0, 0

	for _, jobRef := range wwr.Status.CurrentJobs {
		jobNamespace := jobRef.Namespace
		if jobNamespace == "" {
			jobNamespace = wwr.Metadata.Namespace
		}

		job, err := r.client.GetJob(jobNamespace, jobRef.Name)
		if err != nil {
			// Job missing - treat as failed.
			log.Printf("[reconciler] job %s/%s not found, treating as failed: %v", jobNamespace, jobRef.Name, err)
			stepFailed++
			continue
		}

		switch {
		case jobSucceeded(job):
			stepSucceeded++
		case jobFailed(job):
			stepFailed++
		default:
			stillActive++
		}
	}

	if stillActive > 0 {
		// Jobs are still running; nothing to do until the next tick.
		return nil
	}

	// All current jobs have finished.
	wwr.Status.SuccessfulJobs += stepSucceeded
	wwr.Status.FailedJobs += stepFailed

	return r.advance(wwr, stepSucceeded, stepFailed)
}

// advance collects next-step workflows, clones their jobs, and updates the status.
func (r *Reconciler) advance(wwr *types.WorkflowWebhookRequest, stepSucceeded, stepFailed int) error {
	nextRefs, err := r.collectNextWorkflows(wwr, stepSucceeded, stepFailed)
	if err != nil {
		return err
	}

	if len(nextRefs) == 0 {
		log.Printf("[reconciler] %s/%s: no next workflows - marking done",
			wwr.Metadata.Namespace, wwr.Metadata.Name)
		return r.markDone(wwr, "Done", "all workflows completed")
	}

	nextWorkflows, nextJobs, err := r.resolveAndClone(wwr, nextRefs)
	if err != nil {
		return err
	}

	wwr.Status.CurrentWorkflows = nextWorkflows
	wwr.Status.CurrentJobs = nextJobs
	wwr.Status.AllJobs = append(wwr.Status.AllJobs, nextJobs...)
	wwr.Status.Steps++

	log.Printf("[reconciler] %s/%s: step %d - %d workflow(s), %d job(s) cloned",
		wwr.Metadata.Namespace, wwr.Metadata.Name, wwr.Status.Steps,
		len(nextWorkflows), len(nextJobs))
	return r.client.UpdateWWRStatus(wwr)
}

// collectNextWorkflows examines the `next` field of each current workflow and
// returns the deduplicated list of next workflows whose `when` condition is met.
func (r *Reconciler) collectNextWorkflows(
	wwr *types.WorkflowWebhookRequest,
	stepSucceeded, stepFailed int,
) ([]types.ResourceName, error) {
	seen := make(map[string]struct{})
	var result []types.ResourceName

	for _, wfRef := range wwr.Status.CurrentWorkflows {
		wfNamespace := wfRef.Namespace
		if wfNamespace == "" {
			wfNamespace = wwr.Metadata.Namespace
		}

		workflow, err := r.client.GetWorkflow(wfNamespace, wfRef.Name)
		if err != nil {
			log.Printf("[reconciler] error fetching workflow %s/%s for next-step evaluation: %v",
				wfNamespace, wfRef.Name, err)
			continue
		}

		for _, next := range workflow.Spec.Next {
			if !conditionMet(next.When, stepSucceeded, stepFailed) {
				continue
			}
			// Namespace resolution: explicit > current workflow's namespace.
			nextNamespace := next.Namespace
			if nextNamespace == "" {
				nextNamespace = wfNamespace
			}

			key := nextNamespace + "/" + next.Name
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				result = append(result, types.ResourceName{
					Name:      next.Name,
					Namespace: nextNamespace,
				})
			}
		}
	}
	return result, nil
}

// resolveAndClone fetches each workflow, skips suspended ones, and clones
// their jobs. It returns the active workflow refs and the created job refs.
func (r *Reconciler) resolveAndClone(
	wwr *types.WorkflowWebhookRequest,
	refs []types.ResourceName,
) (activeWorkflows []types.ResourceName, createdJobs []types.ResourceName, err error) {
	for _, ref := range refs {
		wfNamespace := ref.Namespace
		if wfNamespace == "" {
			wfNamespace = wwr.Metadata.Namespace
		}

		workflow, wfErr := r.client.GetWorkflow(wfNamespace, ref.Name)
		if wfErr != nil {
			log.Printf("[reconciler] error getting workflow %s/%s: %v", wfNamespace, ref.Name, wfErr)
			continue
		}

		if workflow.Spec.Suspend {
			log.Printf("[reconciler] skipping suspended workflow %s/%s", wfNamespace, ref.Name)
			continue
		}

		activeWorkflows = append(activeWorkflows, types.ResourceName{
			Name:      ref.Name,
			Namespace: wfNamespace,
		})

		jobs, cloneErr := r.cloneJobsForWorkflow(wwr, workflow, wfNamespace)
		if cloneErr != nil {
			return nil, nil, fmt.Errorf("cloning jobs for workflow %s/%s: %w", wfNamespace, ref.Name, cloneErr)
		}
		createdJobs = append(createdJobs, jobs...)
	}
	return activeWorkflows, createdJobs, nil
}

// cloneJobsForWorkflow clones each job referenced in the workflow spec.
//
// The request Secret strategy depends on whether the job is in the same
// namespace as the WWR:
//
//   - Same namespace: the job mounts the WWR's own request Secret directly.
//     No copy is needed; all same-namespace jobs share it.
//
//   - Different namespace: a per-job mirrored Secret is created in the job's
//     namespace after the Job is created, with the Job set as its owner so
//     Kubernetes GC deletes it automatically when the Job is removed.
//     The mirrored Secret name is pre-generated so it can be declared in the
//     Job manifest before the Secret exists; the pod stays Pending until the
//     mirror arrives (milliseconds in practice).
func (r *Reconciler) cloneJobsForWorkflow(
	wwr *types.WorkflowWebhookRequest,
	workflow *types.Workflow,
	wfNamespace string,
) ([]types.ResourceName, error) {
	var result []types.ResourceName

	// Resolve the namespace of the WWR's own request Secret.
	// The handler stores it in the same namespace as the WWR, so the
	// Namespace field is omitted (empty) and we fall back here.
	mainSecretNs := wwr.Spec.RequestSecret.Namespace
	if mainSecretNs == "" {
		mainSecretNs = wwr.Metadata.Namespace
	}
	mainSecretName := wwr.Spec.RequestSecret.Name

	for _, jobRef := range workflow.Spec.JobsToBeCloned {
		// Resolve the namespace where the template job lives.
		namespace := jobRef.Namespace
		if namespace == "" {
			namespace = wfNamespace
		}

		raw, err := r.client.GetJobRaw(namespace, jobRef.Name)
		if err != nil {
			log.Printf("[reconciler] error fetching job %s/%s for cloning: %v", namespace, jobRef.Name, err)
			continue
		}

		sameNamespace := namespace == wwr.Metadata.Namespace

		// Determine which Secret name to inject into the job's volume:
		//   - Same namespace -> reuse the WWR's own request Secret directly.
		//   - Cross namespace -> a unique mirrored copy will be created after
		//     the Job exists (we need the Job UID for the ownerReference).
		var jobSecretName string
		if sameNamespace {
			jobSecretName = mainSecretName
		} else {
			jobSecretName = generateMirroredSecretName(wwr.Metadata.Name)
		}

		// The ownerReference to the WWR is set only for same-namespace jobs;
		// Kubernetes GC silently drops cross-namespace ownerReferences.
		// Cross-namespace job cleanup is handled by the WWR finalizer.
		cloned := prepareJobForCloning(raw, wwr, jobSecretName, sameNamespace)

		created, err := r.client.CreateJobRaw(namespace, cloned)
		if err != nil {
			return nil, fmt.Errorf("creating cloned job %s/%s: %w", namespace, jobRef.Name, err)
		}
		log.Printf("[reconciler] cloned job %s/%s -> %s/%s (for %s/%s)",
			namespace, jobRef.Name, namespace, created.Name,
			wwr.Metadata.Namespace, wwr.Metadata.Name)

		if !sameNamespace {
			// Mirror the request Secret into the job's namespace, owned by
			// the job so GC cleans it up when the job is deleted.
			if err := r.client.MirrorSecret(
				mainSecretNs, mainSecretName,
				namespace, jobSecretName,
				created.Name, created.UID,
			); err != nil {
				if delErr := r.client.DeleteJob(namespace, created.Name); delErr != nil {
					log.Printf("[reconciler] error deleting job %s/%s after mirror failure: %v",
						namespace, created.Name, delErr)
				}
				return nil, fmt.Errorf("mirroring request secret for job %s/%s: %w",
					namespace, created.Name, err)
			} else {
				log.Printf("[reconciler] mirrored request secret %s/%s -> %s/%s (owned by job %s)",
					mainSecretNs, mainSecretName, namespace, jobSecretName, created.Name)
			}
		}

		result = append(result, types.ResourceName{
			Name:      created.Name,
			Namespace: namespace,
		})
	}
	return result, nil
}

// generateMirroredSecretName returns a unique name for a per-job mirrored
// request Secret in a cross-namespace job. Format: <wwrName>-request-<5 chars>
func generateMirroredSecretName(wwrName string) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	const suffixLen = 5
	suffix := make([]byte, suffixLen)
	for i := range suffix {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			// Fallback: use a fixed character; extremely unlikely to happen.
			suffix[i] = 'x'
			continue
		}
		suffix[i] = alphabet[n.Int64()]
	}
	// Kubernetes names are capped at 253 chars.
	maxWWRLen := 253 - len("-request-") - suffixLen
	if len(wwrName) > maxWWRLen {
		wwrName = wwrName[:maxWWRLen]
	}
	return wwrName + "-request-" + string(suffix)
}

// markDone sets Status.Done, records the CompletionTime, appends a condition,
// and persists the status.
func (r *Reconciler) markDone(wwr *types.WorkflowWebhookRequest, reason, message string) error {
	wwr.Status.Done = true
	wwr.Status.CurrentJobs = nil
	now := time.Now().UTC()
	wwr.Status.CompletionTime = &now
	wwr.Status.Conditions = append(wwr.Status.Conditions, types.Condition{
		LastTransitionTime: now,
		Message:            message,
		Reason:             reason,
		Status:             "True",
		Type:               "Done",
	})
	return r.client.UpdateWWRStatus(wwr)
}

// checkTTL deletes the WWR if its TTLSecondsAfterFinished has elapsed since
// completion. The WWR must already be done when this is called.
func (r *Reconciler) checkTTL(wwr *types.WorkflowWebhookRequest) error {
	ttl := wwr.Spec.TTLSecondsAfterFinished
	if ttl == nil {
		return nil // no TTL configured.
	}
	if wwr.Status.CompletionTime == nil {
		return nil // done but no completion timestamp; skip to be safe.
	}
	elapsed := time.Since(*wwr.Status.CompletionTime)
	ttlDur := time.Duration(*ttl) * time.Second
	if elapsed < ttlDur {
		return nil // TTL not yet expired.
	}
	log.Printf("[reconciler] %s/%s: TTL of %ds expired (completed %s ago) - deleting",
		wwr.Metadata.Namespace, wwr.Metadata.Name, *ttl, elapsed.Round(time.Second))
	return r.client.DeleteWWR(wwr.Metadata.Namespace, wwr.Metadata.Name)
}

// runningWWRsForWebhook returns all non-done WWRs in namespace that reference
// the given webhook, excluding the one with name excludeName.
func (r *Reconciler) runningWWRsForWebhook(
	namespace, webhookName, excludeName string,
) ([]types.WorkflowWebhookRequest, error) {
	all, err := r.client.ListWWRs(namespace)
	if err != nil {
		return nil, err
	}

	var running []types.WorkflowWebhookRequest
	for _, w := range all {
		if w.Metadata.Name == excludeName || w.Status.Done {
			continue
		}
		if w.Spec.WorkflowWebhook.Name == webhookName {
			running = append(running, w)
		}
	}
	return running, nil
}

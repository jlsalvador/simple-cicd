package controller

import (
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/jlsalvador/simple-cicd/internal/types"
)

// initialize Initializes a brand-new WWR (Steps == 0) by fetching the
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
	seen := make(map[string]bool)
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
			if !seen[key] {
				seen[key] = true
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
// Order of operations per job:
//  1. Generate a unique secret name (known before either resource exists).
//  2. Create the Job, with the secret already referenced as a volume.
//     The pod will remain Pending until the Secret is created. This is
//     intentional and resolves within milliseconds.
//  3. Create the Secret in the same namespace, with its ownerReference
//     pointing to the just-created Job, so the Secret is garbage-collected
//     automatically when the Job is deleted.
func (r *Reconciler) cloneJobsForWorkflow(
	wwr *types.WorkflowWebhookRequest,
	workflow *types.Workflow,
	wfNamespace string,
) ([]types.ResourceName, error) {
	var result []types.ResourceName

	for _, jobRef := range workflow.Spec.JobsToBeCloned {
		// Resolve the namespace where the template job lives.
		srcNamespace := jobRef.Namespace
		if srcNamespace == "" {
			srcNamespace = wfNamespace
		}

		raw, err := r.client.GetJobRaw(srcNamespace, jobRef.Name)
		if err != nil {
			log.Printf("[reconciler] error fetching job %s/%s for cloning: %v", srcNamespace, jobRef.Name, err)
			continue
		}

		targetNamespace := srcNamespace

		// Step 1: choose the secret name upfront so we can reference it in
		// the job manifest before the secret actually exists.
		secretName := generateSecretName(wwr.Metadata.Name)

		// Step 2: create the job with the secret volume already declared.
		// The ownerReference to the WWR is only set when the job is in the
		// same namespace: Kubernetes GC silently deletes dependents whose
		// cross-namespace ownerReferences cannot be resolved, which would
		// cause jobs to vanish shortly after creation. Cross-namespace job
		// cleanup is handled exclusively by the finalizer on the WWR.
		sameNamespace := targetNamespace == wwr.Metadata.Namespace
		cloned := prepareJobForCloning(raw, wwr, secretName, sameNamespace)

		created, err := r.client.CreateJobRaw(targetNamespace, cloned)
		if err != nil {
			return nil, fmt.Errorf("creating cloned job %s/%s: %w", targetNamespace, jobRef.Name, err)
		}
		log.Printf("[reconciler] cloned job %s/%s -> %s/%s (for %s/%s)",
			srcNamespace, jobRef.Name, targetNamespace, created.Name,
			wwr.Metadata.Namespace, wwr.Metadata.Name)

		// Step 3: create the secret, owned by the job from the start.
		// Kubernetes GC will delete it when the job is removed (same-namespace).
		if err := r.client.CreateSecretForJob(
			targetNamespace, secretName, wwr.Spec.Request,
			created.Name, created.UID,
		); err != nil {
			// The job is already running; log and continue rather than failing
			// the whole step, to avoid leaving partially-created state.
			log.Printf("[reconciler] warning: created job %s/%s but failed to create its request secret %s/%s: %v",
				targetNamespace, created.Name, targetNamespace, secretName, err)
		} else {
			log.Printf("[reconciler] created request secret %s/%s (owned by job %s)",
				targetNamespace, secretName, created.Name)
		}

		result = append(result, types.ResourceName{
			Name:      created.Name,
			Namespace: targetNamespace,
		})
	}
	return result, nil
}

// generateSecretName returns a unique name for the per-job request Secret.
// Format: <wwrName>-request-<5 random alphanumeric chars>
func generateSecretName(wwrName string) string {
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
	// Kubernetes names are capped at 253 chars; truncate wwrName if necessary
	// so the result never exceeds that limit.
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
		LastTransitionTime: time.Now().UTC(),
		Message:            message,
		Reason:             reason,
		Status:             "True",
		Type:               "Done",
	})
	return r.client.UpdateWWRStatus(wwr)
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

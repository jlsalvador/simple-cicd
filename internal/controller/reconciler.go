package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jlsalvador/simple-cicd/internal/k8s"
	"github.com/jlsalvador/simple-cicd/internal/types"
)

// Reconciler runs a loop that processes all active WorkflowWebhookRequests.
type Reconciler struct {
	client    *k8s.Client
	triggerCh chan struct{}
}

// NewReconciler creates a Reconciler.
func NewReconciler(client *k8s.Client) *Reconciler {
	return &Reconciler{
		client:    client,
		triggerCh: make(chan struct{}, 64),
	}
}

// Trigger signals the reconciler to run an immediate reconciliation cycle.
func (r *Reconciler) Trigger() {
	select {
	case r.triggerCh <- struct{}{}:
	default:
	}
}

// Run starts the reconciliation loop. It runs until ctx is cancelled.
// It reconciles on every ticker tick or when Trigger() is called.
func (r *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	log.Println("[reconciler] started")
	for {
		select {
		case <-ctx.Done():
			log.Println("[reconciler] stopped")
			return
		case <-ticker.C:
			r.reconcileAll()
		case <-r.triggerCh:
			// Drain any additional queued signals before reconciling
			for len(r.triggerCh) > 0 {
				<-r.triggerCh
			}
			r.reconcileAll()
		}
	}
}

func (r *Reconciler) reconcileAll() {
	wwrs, err := r.client.ListAllWWRs()
	if err != nil {
		log.Printf("[reconciler] error listing WorkflowWebhookRequests: %v", err)
		return
	}
	for i := range wwrs {
		wwr := &wwrs[i]
		if err := r.reconcileWWR(wwr); err != nil {
			log.Printf("[reconciler] error reconciling %s/%s: %v",
				wwr.Metadata.Namespace, wwr.Metadata.Name, err)
		}
	}
}

// reconcileWWR runs one reconciliation iteration for a single WWR.
func (r *Reconciler) reconcileWWR(wwr *types.WorkflowWebhookRequest) error {
	if wwr.Status.Done {
		return nil
	}

	if wwr.Status.Steps == 0 {
		// First time we see this WWR
		return r.initialize(wwr)
	}

	return r.checkProgress(wwr)
}

// --------------------------------------------------------------------------
// Phase 1 - Initialise a brand-new WWR (Steps == 0)
// --------------------------------------------------------------------------

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

	// Honour concurrency policy
	if err := r.applyConcurrencyPolicy(wwr, webhook); err != nil {
		return err
	}
	// applyConcurrencyPolicy may have marked the WWR as done (Forbid)
	if wwr.Status.Done {
		return nil
	}

	// Resolve and clone jobs for each initial workflow
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
	wwr.Status.Steps = 1

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

// --------------------------------------------------------------------------
// Phase 2 - Check progress of current jobs (Steps > 0)
// --------------------------------------------------------------------------

func (r *Reconciler) checkProgress(wwr *types.WorkflowWebhookRequest) error {
	if len(wwr.Status.CurrentJobs) == 0 {
		// No jobs to wait for; try to advance immediately
		return r.advance(wwr, 0, 0)
	}

	stepSucceeded, stepFailed, stillActive := 0, 0, 0

	for _, jobRef := range wwr.Status.CurrentJobs {
		job, err := r.client.GetJob(wwr.Metadata.Namespace, jobRef.Name)
		if err != nil {
			// Job missing - treat as failed
			log.Printf("[reconciler] job %s/%s not found, treating as failed: %v", wwr.Metadata.Namespace, jobRef.Name, err)
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
		// Jobs are still running; nothing to do until the next tick
		return nil
	}

	// All current jobs have finished
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
		workflow, err := r.client.GetWorkflow(wwr.Metadata.Namespace, wfRef.Name)
		if err != nil {
			log.Printf("[reconciler] error fetching workflow %s/%s for next-step evaluation: %v",
				wwr.Metadata.Namespace, wfRef.Name, err)
			continue
		}

		for _, next := range workflow.Spec.Next {
			if !conditionMet(next.When, stepSucceeded, stepFailed) {
				continue
			}
			if !seen[next.Name] {
				seen[next.Name] = true
				result = append(result, types.ResourceName{Name: next.Name})
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
		workflow, wfErr := r.client.GetWorkflow(wwr.Metadata.Namespace, ref.Name)
		if wfErr != nil {
			log.Printf("[reconciler] error getting workflow %s/%s: %v", wwr.Metadata.Namespace, ref.Name, wfErr)
			continue
		}

		if workflow.Spec.Suspend {
			log.Printf("[reconciler] skipping suspended workflow %s/%s", wwr.Metadata.Namespace, ref.Name)
			continue
		}

		activeWorkflows = append(activeWorkflows, types.ResourceName{Name: ref.Name})

		jobs, cloneErr := r.cloneJobsForWorkflow(wwr, workflow)
		if cloneErr != nil {
			return nil, nil, fmt.Errorf("cloning jobs for workflow %s/%s: %w", wwr.Metadata.Namespace, ref.Name, cloneErr)
		}
		createdJobs = append(createdJobs, jobs...)
	}
	return activeWorkflows, createdJobs, nil
}

// cloneJobsForWorkflow reads each job referenced in the workflow spec and
// creates a clone owned by the given WWR.
func (r *Reconciler) cloneJobsForWorkflow(
	wwr *types.WorkflowWebhookRequest,
	workflow *types.Workflow,
) ([]types.ResourceName, error) {
	var result []types.ResourceName

	for _, jobRef := range workflow.Spec.JobsToBeCloned {
		raw, err := r.client.GetJobRaw(wwr.Metadata.Namespace, jobRef.Name)
		if err != nil {
			log.Printf("[reconciler] error fetching job %s/%s for cloning: %v", wwr.Metadata.Namespace, jobRef.Name, err)
			continue
		}

		cloned := prepareJobForCloning(raw, wwr)

		createdName, err := r.client.CreateJobRaw(wwr.Metadata.Namespace, cloned)
		if err != nil {
			return nil, fmt.Errorf("creating cloned job %s/%s: %w", wwr.Metadata.Namespace, jobRef.Name, err)
		}

		log.Printf("[reconciler] cloned job %s/%s -> %s/%s (for %s/%s)",
			wwr.Metadata.Namespace, jobRef.Name, wwr.Metadata.Namespace, createdName,
			wwr.Metadata.Namespace, wwr.Metadata.Name)

		result = append(result, types.ResourceName{Name: createdName})
	}
	return result, nil
}

// markDone sets Status.Done and appends a condition, then persists the status.
func (r *Reconciler) markDone(wwr *types.WorkflowWebhookRequest, reason, message string) error {
	wwr.Status.Done = true
	wwr.Status.CurrentJobs = nil
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

// --------------------------------------------------------------------------
// Job helpers
// --------------------------------------------------------------------------

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

// --------------------------------------------------------------------------
// Job cloning
// --------------------------------------------------------------------------

// prepareJobForCloning deep-copies a raw job map and strips all
// server-assigned fields so it can be re-submitted as a new Job.
func prepareJobForCloning(raw map[string]any, wwr *types.WorkflowWebhookRequest) map[string]any {
	// Deep copy via round-trip JSON
	data, _ := json.Marshal(raw)
	var cloned map[string]any
	_ = json.Unmarshal(data, &cloned)

	// --- Metadata ---
	meta, _ := cloned["metadata"].(map[string]any)
	if meta == nil {
		meta = make(map[string]any)
	}

	originalName, _ := meta["name"].(string)

	// Clear server-managed metadata
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

	// Add tracking labels
	labels, _ := meta["labels"].(map[string]any)
	if labels == nil {
		labels = make(map[string]any)
	}
	labels[types.LabelWWRName] = wwr.Metadata.Name
	labels[types.LabelWWRNamespace] = wwr.Metadata.Namespace
	meta["labels"] = labels

	// Set ownerReference pointing to the WWR
	trueVal := true
	meta["ownerReferences"] = []map[string]any{
		{
			"apiVersion":         types.APIGroup + "/" + types.APIVersion,
			"kind":               "WorkflowWebhookRequest",
			"name":               wwr.Metadata.Name,
			"uid":                wwr.Metadata.UID,
			"controller":         &trueVal,
			"blockOwnerDeletion": &trueVal,
		},
	}
	cloned["metadata"] = meta

	// --- Spec ---
	// Remove the auto-generated selector so Kubernetes regenerates it.
	if spec, ok := cloned["spec"].(map[string]any); ok {
		delete(spec, "selector")

		// Remove labels that were added by the previous job controller
		// (controller-uid / job-name) from the pod template so a new
		// selector gets generated cleanly.
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

	// Inject the request secret as a volume into the pod template.
	if spec, ok := cloned["spec"].(map[string]any); ok {
		if tmpl, ok := spec["template"].(map[string]any); ok {
			tmplSpec, _ := tmpl["spec"].(map[string]any)
			if tmplSpec == nil {
				tmplSpec = make(map[string]any)
				tmpl["spec"] = tmplSpec
			}

			// Append the secret volume (idempotent name).
			volumes, _ := tmplSpec["volumes"].([]any)
			volumes = append(volumes, map[string]any{
				"name": "request",
				"secret": map[string]any{
					"secretName": wwr.Spec.RequestSecret.Name,
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

	// Remove status - it is set by the server
	delete(cloned, "status")

	return cloned
}

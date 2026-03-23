package controller

import (
	"strings"
	"testing"
	"time"

	"github.com/jlsalvador/simple-cicd/internal/types"
)

// --------------------------------------------------------------------------
// conditionMet
// --------------------------------------------------------------------------

func TestConditionMet(t *testing.T) {
	cases := []struct {
		when      string
		succeeded int
		failed    int
		want      bool
	}{
		// OnSuccess / empty string: no failures required
		{types.WhenOnSuccess, 3, 0, true},
		{types.WhenOnSuccess, 0, 0, true},
		{"", 0, 0, true},
		{types.WhenOnSuccess, 1, 1, false},
		{types.WhenOnSuccess, 0, 1, false},

		// OnAnySuccess: at least one success
		{types.WhenOnAnySuccess, 1, 0, true},
		{types.WhenOnAnySuccess, 1, 5, true},
		{types.WhenOnAnySuccess, 0, 0, false},
		{types.WhenOnAnySuccess, 0, 3, false},

		// OnFailure: no successes at all
		{types.WhenOnFailure, 0, 1, true},
		{types.WhenOnFailure, 0, 0, true},
		{types.WhenOnFailure, 1, 0, false},
		{types.WhenOnFailure, 1, 1, false},

		// OnAnyFailure: at least one failure
		{types.WhenOnAnyFailure, 0, 1, true},
		{types.WhenOnAnyFailure, 3, 1, true},
		{types.WhenOnAnyFailure, 0, 0, false},
		{types.WhenOnAnyFailure, 3, 0, false},

		// Always: unconditional
		{types.WhenAlways, 0, 0, true},
		{types.WhenAlways, 5, 5, true},

		// Unknown value: defaults to true
		{"bogus", 0, 0, true},
	}

	for _, c := range cases {
		got := conditionMet(c.when, c.succeeded, c.failed)
		if got != c.want {
			t.Errorf("conditionMet(%q, %d, %d) = %v, want %v",
				c.when, c.succeeded, c.failed, got, c.want)
		}
	}
}

// --------------------------------------------------------------------------
// jobSucceeded / jobFailed
// --------------------------------------------------------------------------

func TestJobSucceededAndFailed(t *testing.T) {
	cases := []struct {
		name     string
		status   types.JobStatus
		wantSucc bool
		wantFail bool
	}{
		{
			name:     "condition Complete=True",
			status:   succeededStatus(),
			wantSucc: true, wantFail: false,
		},
		{
			name:     "counters only: succeeded>0 active=0",
			status:   types.JobStatus{Active: 0, Succeeded: 2},
			wantSucc: true, wantFail: false,
		},
		{
			name:     "condition Failed=True",
			status:   failedStatus(),
			wantSucc: false, wantFail: true,
		},
		{
			name:     "counters only: failed>0 active=0 succeeded=0",
			status:   types.JobStatus{Active: 0, Failed: 1},
			wantSucc: false, wantFail: true,
		},
		{
			name:     "still running",
			status:   runningStatus(),
			wantSucc: false, wantFail: false,
		},
		{
			name:     "pending (all zeros)",
			status:   types.JobStatus{},
			wantSucc: false, wantFail: false,
		},
	}

	for _, c := range cases {
		job := &types.Job{Status: c.status}
		if got := jobSucceeded(job); got != c.wantSucc {
			t.Errorf("[%s] jobSucceeded = %v, want %v", c.name, got, c.wantSucc)
		}
		if got := jobFailed(job); got != c.wantFail {
			t.Errorf("[%s] jobFailed = %v, want %v", c.name, got, c.wantFail)
		}
	}
}

// --------------------------------------------------------------------------
// Finalizer management
// --------------------------------------------------------------------------

func TestAddFinalizer(t *testing.T) {
	fc := newFakeClient(t)
	wwr := makeWWR("default", "wwr-1", "hook")
	fc.addWWR(wwr)

	r := NewReconciler(fc)
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("reconcileWWR error: %v", err)
	}

	// The reconciler should have added the finalizer and stopped (no webhook
	// needed on first pass because we return after patching).
	if fc.patchFinalizerCalls.Load() != 1 {
		t.Errorf("expected 1 PatchWWRFinalizers call, got %d", fc.patchFinalizerCalls.Load())
	}
	if !hasFinalizer(wwr, types.FinalizerCleanup) {
		t.Errorf("finalizer not present after reconcile")
	}
}

// --------------------------------------------------------------------------
// Deletion / cleanup finalizer
// --------------------------------------------------------------------------

func TestHandleDeletion_SameNamespaceJobsSkipped(t *testing.T) {
	fc := newFakeClient(t)
	now := time.Now()
	wwr := makeWWR("default", "wwr-del", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
	wwr.Metadata.DeletionTimestamp = &now
	// Two jobs in the SAME namespace -> GC handles them, reconciler skips.
	wwr.Status.AllJobs = []types.ResourceName{
		{Namespace: "default", Name: "job-a"},
		{Namespace: "default", Name: "job-b"},
	}
	fc.addWWR(wwr)

	r := NewReconciler(fc)
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fc.deleteJobCalls) != 0 {
		t.Errorf("expected no DeleteJob calls for same-namespace jobs, got %v", fc.deleteJobCalls)
	}
	// The request Secret must be deleted regardless of whether there are
	// cross-namespace jobs.
	if len(fc.deleteSecretCalls) != 1 {
		t.Errorf("expected 1 DeleteSecret call for request secret, got %d: %v",
			len(fc.deleteSecretCalls), fc.deleteSecretCalls)
	}
	if fc.deleteSecretCalls[0] != "default/test-request-secret" {
		t.Errorf("unexpected secret deleted: %q", fc.deleteSecretCalls[0])
	}
	if fc.patchFinalizerCalls.Load() != 1 {
		t.Errorf("expected finalizer to be removed (1 patch call), got %d", fc.patchFinalizerCalls.Load())
	}
	if hasFinalizer(wwr, types.FinalizerCleanup) {
		t.Errorf("finalizer should have been removed")
	}
}

func TestHandleDeletion_CrossNamespaceJobsDeleted(t *testing.T) {
	fc := newFakeClient(t)
	now := time.Now()
	wwr := makeWWR("default", "wwr-del", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
	wwr.Metadata.DeletionTimestamp = &now
	// One same-NS job, two cross-NS jobs.
	wwr.Status.AllJobs = []types.ResourceName{
		{Namespace: "default", Name: "job-same"},
		{Namespace: "other-ns", Name: "job-cross-1"},
		{Namespace: "other-ns", Name: "job-cross-2"},
	}
	fc.addWWR(wwr)

	r := NewReconciler(fc)
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cross-namespace jobs must be explicitly deleted.
	if len(fc.deleteJobCalls) != 2 {
		t.Errorf("expected 2 DeleteJob calls, got %v", fc.deleteJobCalls)
	}
	for _, call := range fc.deleteJobCalls {
		if call == "default/job-same" {
			t.Errorf("same-namespace job should not have been deleted explicitly")
		}
	}
	// Request Secret must also be deleted.
	if len(fc.deleteSecretCalls) != 1 {
		t.Errorf("expected 1 DeleteSecret call, got %d: %v",
			len(fc.deleteSecretCalls), fc.deleteSecretCalls)
	}
}

// --------------------------------------------------------------------------
// Suspended webhook
// --------------------------------------------------------------------------

func TestReconcile_SuspendedWebhook(t *testing.T) {
	fc := newFakeClient(t)
	wwr := makeWWR("default", "wwr-1", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
	fc.addWWR(wwr)
	fc.addWebhook(makeSuspendedWebhook("default", "hook"))

	r := NewReconciler(fc)
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !wwr.Status.Done {
		t.Errorf("expected WWR to be marked done when webhook is suspended")
	}
}

// --------------------------------------------------------------------------
// No active workflows
// --------------------------------------------------------------------------

func TestReconcile_NoActiveWorkflows(t *testing.T) {
	fc := newFakeClient(t)
	wwr := makeWWR("default", "wwr-1", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
	fc.addWWR(wwr)
	// Webhook references a workflow that is suspended.
	fc.addWebhook(makeWebhook("default", "hook", []types.ResourceName{ref("wf-a")}))
	fc.addWorkflow(makeSuspendedWorkflow("default", "wf-a"))

	r := NewReconciler(fc)
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !wwr.Status.Done {
		t.Errorf("expected WWR done when all workflows are suspended")
	}
}

// --------------------------------------------------------------------------
// Happy path: single workflow, single job, completes successfully
// --------------------------------------------------------------------------

func TestReconcile_SingleJobSuccess(t *testing.T) {
	fc := newFakeClient(t)
	wwr := makeWWR("default", "wwr-1", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
	fc.addWWR(wwr)
	fc.addWebhook(makeWebhook("default", "hook", []types.ResourceName{ref("wf-a")}))
	fc.addWorkflow(makeWorkflow("default", "wf-a", []types.ResourceName{ref("job-tmpl")}))
	fc.addJobTemplate("default", "job-tmpl", minimalJobRaw("job-tmpl"))

	r := NewReconciler(fc)

	// --- Tick 1: initialize -> jobs cloned ---
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("tick 1 error: %v", err)
	}
	if wwr.Status.Steps != 1 {
		t.Fatalf("expected Steps=1 after init, got %d", wwr.Status.Steps)
	}
	if len(wwr.Status.CurrentJobs) != 1 {
		t.Fatalf("expected 1 current job, got %d", len(wwr.Status.CurrentJobs))
	}
	// Same-namespace job: reconciler reuses the WWR's existing request Secret
	// directly - no MirrorSecret call is expected.
	if len(fc.mirroredSecrets) != 0 {
		t.Errorf("expected no mirrored secrets for a same-namespace job, got %d: %v",
			len(fc.mirroredSecrets), fc.mirroredSecrets)
	}

	// --- Tick 2: job still running -> no change ---
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("tick 2 error: %v", err)
	}
	if wwr.Status.Done {
		t.Fatalf("should not be done while job is active")
	}

	// --- Mark job as succeeded ---
	jobRef := wwr.Status.CurrentJobs[0]
	fc.setJobStatus(jobRef.Namespace, jobRef.Name, succeededStatus())

	// --- Tick 3: job done, no next workflows -> mark done ---
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("tick 3 error: %v", err)
	}
	if !wwr.Status.Done {
		t.Errorf("expected WWR done after job succeeded")
	}
	if wwr.Status.SuccessfulJobs != 1 {
		t.Errorf("expected SuccessfulJobs=1, got %d", wwr.Status.SuccessfulJobs)
	}
}

// --------------------------------------------------------------------------
// Happy path: single workflow, single job, fails
// --------------------------------------------------------------------------

func TestReconcile_SingleJobFailure(t *testing.T) {
	fc := newFakeClient(t)
	wwr := makeWWR("default", "wwr-1", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
	fc.addWWR(wwr)
	fc.addWebhook(makeWebhook("default", "hook", []types.ResourceName{ref("wf-a")}))
	fc.addWorkflow(makeWorkflow("default", "wf-a", []types.ResourceName{ref("job-tmpl")}))
	fc.addJobTemplate("default", "job-tmpl", minimalJobRaw("job-tmpl"))

	r := NewReconciler(fc)
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("init error: %v", err)
	}

	jobRef := wwr.Status.CurrentJobs[0]
	fc.setJobStatus(jobRef.Namespace, jobRef.Name, failedStatus())

	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("progress check error: %v", err)
	}
	if !wwr.Status.Done {
		t.Errorf("expected WWR done after job failed")
	}
	if wwr.Status.FailedJobs != 1 {
		t.Errorf("expected FailedJobs=1, got %d", wwr.Status.FailedJobs)
	}
}

// --------------------------------------------------------------------------
// Next workflow: OnSuccess condition – chains on success, skips on failure
// --------------------------------------------------------------------------

func TestReconcile_NextWorkflow_OnSuccess(t *testing.T) {
	for _, tc := range []struct {
		name        string
		firstStatus types.JobStatus
		wantChained bool
	}{
		{"success chains", succeededStatus(), true},
		{"failure skips", failedStatus(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := newFakeClient(t)
			wwr := makeWWR("default", "wwr-1", "hook")
			wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
			fc.addWWR(wwr)
			fc.addWebhook(makeWebhook("default", "hook", []types.ResourceName{ref("wf-a")}))
			fc.addWorkflow(makeWorkflow("default", "wf-a",
				[]types.ResourceName{ref("job-tmpl")},
				types.NextWorkflow{Name: "wf-b", When: types.WhenOnSuccess},
			))
			fc.addWorkflow(makeWorkflow("default", "wf-b",
				[]types.ResourceName{ref("job-tmpl-2")},
			))
			fc.addJobTemplate("default", "job-tmpl", minimalJobRaw("job-tmpl"))
			fc.addJobTemplate("default", "job-tmpl-2", minimalJobRaw("job-tmpl-2"))

			r := NewReconciler(fc)
			// Init
			if err := r.reconcileWWR(wwr); err != nil {
				t.Fatal(err)
			}
			// Simulate job result
			jobRef := wwr.Status.CurrentJobs[0]
			fc.setJobStatus(jobRef.Namespace, jobRef.Name, tc.firstStatus)
			// Advance
			if err := r.reconcileWWR(wwr); err != nil {
				t.Fatal(err)
			}

			if tc.wantChained {
				if wwr.Status.Done {
					t.Errorf("expected chained step, but WWR is done")
				}
				if wwr.Status.Steps != 2 {
					t.Errorf("expected Steps=2, got %d", wwr.Status.Steps)
				}
			} else {
				if !wwr.Status.Done {
					t.Errorf("expected WWR done (no chaining on failure)")
				}
			}
		})
	}
}

// --------------------------------------------------------------------------
// Next workflow: OnFailure condition
// --------------------------------------------------------------------------

func TestReconcile_NextWorkflow_OnFailure(t *testing.T) {
	for _, tc := range []struct {
		name        string
		firstStatus types.JobStatus
		wantChained bool
	}{
		{"failure chains", failedStatus(), true},
		{"success skips", succeededStatus(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := newFakeClient(t)
			wwr := makeWWR("default", "wwr-1", "hook")
			wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
			fc.addWWR(wwr)
			fc.addWebhook(makeWebhook("default", "hook", []types.ResourceName{ref("wf-a")}))
			fc.addWorkflow(makeWorkflow("default", "wf-a",
				[]types.ResourceName{ref("job-tmpl")},
				types.NextWorkflow{Name: "wf-fallback", When: types.WhenOnFailure},
			))
			fc.addWorkflow(makeWorkflow("default", "wf-fallback",
				[]types.ResourceName{ref("job-tmpl-2")},
			))
			fc.addJobTemplate("default", "job-tmpl", minimalJobRaw("job-tmpl"))
			fc.addJobTemplate("default", "job-tmpl-2", minimalJobRaw("job-tmpl-2"))

			r := NewReconciler(fc)
			if err := r.reconcileWWR(wwr); err != nil {
				t.Fatal(err)
			}
			jobRef := wwr.Status.CurrentJobs[0]
			fc.setJobStatus(jobRef.Namespace, jobRef.Name, tc.firstStatus)
			if err := r.reconcileWWR(wwr); err != nil {
				t.Fatal(err)
			}

			if tc.wantChained && wwr.Status.Done {
				t.Errorf("expected chained fallback step, but WWR is done")
			}
			if !tc.wantChained && !wwr.Status.Done {
				t.Errorf("expected WWR done when failure condition not met")
			}
		})
	}
}

// --------------------------------------------------------------------------
// Next workflow: Always condition
// --------------------------------------------------------------------------

func TestReconcile_NextWorkflow_Always(t *testing.T) {
	for _, status := range []types.JobStatus{succeededStatus(), failedStatus()} {
		fc := newFakeClient(t)
		wwr := makeWWR("default", "wwr-1", "hook")
		wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
		fc.addWWR(wwr)
		fc.addWebhook(makeWebhook("default", "hook", []types.ResourceName{ref("wf-a")}))
		fc.addWorkflow(makeWorkflow("default", "wf-a",
			[]types.ResourceName{ref("job-tmpl")},
			types.NextWorkflow{Name: "wf-always", When: types.WhenAlways},
		))
		fc.addWorkflow(makeWorkflow("default", "wf-always", []types.ResourceName{ref("job-tmpl-2")}))
		fc.addJobTemplate("default", "job-tmpl", minimalJobRaw("job-tmpl"))
		fc.addJobTemplate("default", "job-tmpl-2", minimalJobRaw("job-tmpl-2"))

		r := NewReconciler(fc)
		if err := r.reconcileWWR(wwr); err != nil {
			t.Fatal(err)
		}
		jobRef := wwr.Status.CurrentJobs[0]
		fc.setJobStatus(jobRef.Namespace, jobRef.Name, status)
		if err := r.reconcileWWR(wwr); err != nil {
			t.Fatal(err)
		}

		if wwr.Status.Done {
			t.Errorf("Always condition: expected chained step regardless of job result, but got done")
		}
	}
}

// --------------------------------------------------------------------------
// Cross-namespace workflow and job
// --------------------------------------------------------------------------

func TestReconcile_CrossNamespace(t *testing.T) {
	fc := newFakeClient(t)
	wwr := makeWWR("ns-a", "wwr-1", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
	fc.addWWR(wwr)
	// Webhook in ns-a, workflow in ns-b, job template in ns-b.
	fc.addWebhook(makeWebhook("ns-a", "hook", []types.ResourceName{refNS("ns-b", "wf-remote")}))
	fc.addWorkflow(makeWorkflow("ns-b", "wf-remote", []types.ResourceName{ref("job-tmpl")}))
	fc.addJobTemplate("ns-b", "job-tmpl", minimalJobRaw("job-tmpl"))

	r := NewReconciler(fc)
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("init error: %v", err)
	}

	if len(wwr.Status.CurrentJobs) != 1 {
		t.Fatalf("expected 1 current job, got %d", len(wwr.Status.CurrentJobs))
	}
	// Job should live in ns-b.
	jobRef := wwr.Status.CurrentJobs[0]
	if jobRef.Namespace != "ns-b" {
		t.Errorf("expected job in ns-b, got %q", jobRef.Namespace)
	}
	// AllJobs must be populated for finalizer cleanup.
	if len(wwr.Status.AllJobs) != 1 {
		t.Errorf("expected AllJobs to have 1 entry")
	}
	// Cross-namespace job: the request Secret must be mirrored into ns-b.
	if len(fc.mirroredSecrets) != 1 {
		t.Errorf("expected 1 mirrored secret for cross-namespace job, got %d: %v",
			len(fc.mirroredSecrets), fc.mirroredSecrets)
	}

	// Simulate deletion: cross-namespace job must be explicitly deleted,
	// and the request Secret must be cleaned up.
	now := time.Now()
	wwr.Metadata.DeletionTimestamp = &now
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("deletion error: %v", err)
	}
	if len(fc.deleteJobCalls) != 1 {
		t.Errorf("expected 1 DeleteJob call for cross-namespace job, got %v", fc.deleteJobCalls)
	}
	if len(fc.deleteSecretCalls) != 1 {
		t.Errorf("expected 1 DeleteSecret call for request secret, got %d: %v",
			len(fc.deleteSecretCalls), fc.deleteSecretCalls)
	}
}

// --------------------------------------------------------------------------
// Cross-namespace next workflow
// --------------------------------------------------------------------------

func TestReconcile_CrossNamespace_NextWorkflow(t *testing.T) {
	fc := newFakeClient(t)
	wwr := makeWWR("ns-a", "wwr-1", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
	fc.addWWR(wwr)
	fc.addWebhook(makeWebhook("ns-a", "hook", []types.ResourceName{ref("wf-a")}))
	fc.addWorkflow(makeWorkflow("ns-a", "wf-a",
		[]types.ResourceName{ref("job-tmpl")},
		types.NextWorkflow{Name: "wf-b", Namespace: "ns-b", When: types.WhenAlways},
	))
	fc.addWorkflow(makeWorkflow("ns-b", "wf-b", []types.ResourceName{ref("job-tmpl-b")}))
	fc.addJobTemplate("ns-a", "job-tmpl", minimalJobRaw("job-tmpl"))
	fc.addJobTemplate("ns-b", "job-tmpl-b", minimalJobRaw("job-tmpl-b"))

	r := NewReconciler(fc)
	// Init step 1
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatal(err)
	}
	fc.setJobStatus(wwr.Status.CurrentJobs[0].Namespace, wwr.Status.CurrentJobs[0].Name, succeededStatus())

	// Advance to step 2 (cross-namespace next workflow)
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatal(err)
	}
	if wwr.Status.Done {
		t.Fatalf("expected step 2 to run in ns-b, but WWR is done")
	}
	if wwr.Status.Steps != 2 {
		t.Errorf("expected Steps=2, got %d", wwr.Status.Steps)
	}
	jobRef := wwr.Status.CurrentJobs[0]
	if jobRef.Namespace != "ns-b" {
		t.Errorf("expected step-2 job in ns-b, got %q", jobRef.Namespace)
	}
}

// --------------------------------------------------------------------------
// ConcurrencyPolicy: Forbid
// --------------------------------------------------------------------------

func TestReconcile_ConcurrencyForbid(t *testing.T) {
	fc := newFakeClient(t)

	// An already-running WWR.
	running := makeWWR("default", "wwr-old", "hook")
	running.Metadata.Finalizers = []string{types.FinalizerCleanup}
	running.Status.Steps = 1 // already initialized, not done
	fc.addWWR(running)

	// New WWR that should be rejected.
	wwr := makeWWR("default", "wwr-new", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
	fc.addWWR(wwr)
	fc.addWebhook(makeWebhookWithPolicy("default", "hook", types.ConcurrencyForbid,
		[]types.ResourceName{ref("wf-a")}))

	r := NewReconciler(fc)
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !wwr.Status.Done {
		t.Errorf("expected new WWR to be rejected (done) when Forbid policy and another is running")
	}
}

// --------------------------------------------------------------------------
// ConcurrencyPolicy: Replace
// --------------------------------------------------------------------------

func TestReconcile_ConcurrencyReplace(t *testing.T) {
	fc := newFakeClient(t)

	running := makeWWR("default", "wwr-old", "hook")
	running.Metadata.Finalizers = []string{types.FinalizerCleanup}
	running.Status.Steps = 1
	fc.addWWR(running)

	wwr := makeWWR("default", "wwr-new", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
	fc.addWWR(wwr)
	fc.addWebhook(makeWebhookWithPolicy("default", "hook", types.ConcurrencyReplace,
		[]types.ResourceName{ref("wf-a")}))
	fc.addWorkflow(makeWorkflow("default", "wf-a", []types.ResourceName{ref("job-tmpl")}))
	fc.addJobTemplate("default", "job-tmpl", minimalJobRaw("job-tmpl"))

	r := NewReconciler(fc)
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Old WWR must have been deleted.
	if len(fc.deleteWWRCalls) != 1 || fc.deleteWWRCalls[0] != "default/wwr-old" {
		t.Errorf("expected old WWR to be deleted, deleteWWRCalls=%v", fc.deleteWWRCalls)
	}
	// New WWR should have proceeded.
	if wwr.Status.Done {
		t.Errorf("new WWR should not be done after Replace; it should have started")
	}
	if wwr.Status.Steps != 1 {
		t.Errorf("expected Steps=1 after Replace init, got %d", wwr.Status.Steps)
	}
}

// --------------------------------------------------------------------------
// Multiple jobs in one workflow step: mixed results
// --------------------------------------------------------------------------

func TestReconcile_MultipleJobs_MixedResults(t *testing.T) {
	fc := newFakeClient(t)
	wwr := makeWWR("default", "wwr-1", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
	fc.addWWR(wwr)
	fc.addWebhook(makeWebhook("default", "hook", []types.ResourceName{ref("wf-a")}))
	fc.addWorkflow(makeWorkflow("default", "wf-a",
		[]types.ResourceName{ref("job-ok"), ref("job-fail")},
		// OnAnyFailure chains to a recovery workflow.
		types.NextWorkflow{Name: "wf-recovery", When: types.WhenOnAnyFailure},
	))
	fc.addWorkflow(makeWorkflow("default", "wf-recovery", []types.ResourceName{ref("job-recovery")}))
	fc.addJobTemplate("default", "job-ok", minimalJobRaw("job-ok"))
	fc.addJobTemplate("default", "job-fail", minimalJobRaw("job-fail"))
	fc.addJobTemplate("default", "job-recovery", minimalJobRaw("job-recovery"))

	r := NewReconciler(fc)
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatal(err)
	}
	if len(wwr.Status.CurrentJobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(wwr.Status.CurrentJobs))
	}

	// One succeeds, one fails.
	fc.setJobStatus(wwr.Status.CurrentJobs[0].Namespace, wwr.Status.CurrentJobs[0].Name, succeededStatus())
	fc.setJobStatus(wwr.Status.CurrentJobs[1].Namespace, wwr.Status.CurrentJobs[1].Name, failedStatus())

	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatal(err)
	}
	// OnAnyFailure should have triggered recovery.
	if wwr.Status.Done {
		t.Errorf("expected recovery workflow to run, not done yet")
	}
	if wwr.Status.Steps != 2 {
		t.Errorf("expected Steps=2 (recovery step), got %d", wwr.Status.Steps)
	}
	if wwr.Status.SuccessfulJobs != 1 || wwr.Status.FailedJobs != 1 {
		t.Errorf("job counters wrong: succ=%d fail=%d",
			wwr.Status.SuccessfulJobs, wwr.Status.FailedJobs)
	}
}

// --------------------------------------------------------------------------
// AllJobs accumulates across steps
// --------------------------------------------------------------------------

func TestReconcile_AllJobs_AccumulatesAcrossSteps(t *testing.T) {
	fc := newFakeClient(t)
	wwr := makeWWR("default", "wwr-1", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
	fc.addWWR(wwr)
	fc.addWebhook(makeWebhook("default", "hook", []types.ResourceName{ref("wf-a")}))
	fc.addWorkflow(makeWorkflow("default", "wf-a",
		[]types.ResourceName{ref("job-1")},
		types.NextWorkflow{Name: "wf-b", When: types.WhenAlways},
	))
	fc.addWorkflow(makeWorkflow("default", "wf-b", []types.ResourceName{ref("job-2")}))
	fc.addJobTemplate("default", "job-1", minimalJobRaw("job-1"))
	fc.addJobTemplate("default", "job-2", minimalJobRaw("job-2"))

	r := NewReconciler(fc)
	// Step 1
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatal(err)
	}
	fc.setJobStatus(wwr.Status.CurrentJobs[0].Namespace, wwr.Status.CurrentJobs[0].Name, succeededStatus())
	// Step 2
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatal(err)
	}

	if len(wwr.Status.AllJobs) != 2 {
		t.Errorf("expected AllJobs to have 2 entries after 2 steps, got %d", len(wwr.Status.AllJobs))
	}
}

// --------------------------------------------------------------------------
// Missing job treated as failed
// --------------------------------------------------------------------------

func TestReconcile_MissingJobTreatedAsFailed(t *testing.T) {
	fc := newFakeClient(t)
	wwr := makeWWR("default", "wwr-1", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
	fc.addWWR(wwr)
	fc.addWebhook(makeWebhook("default", "hook", []types.ResourceName{ref("wf-a")}))
	fc.addWorkflow(makeWorkflow("default", "wf-a", []types.ResourceName{ref("job-tmpl")}))
	fc.addJobTemplate("default", "job-tmpl", minimalJobRaw("job-tmpl"))

	r := NewReconciler(fc)
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatal(err)
	}

	// Delete the live job to simulate it disappearing.
	jobRef := wwr.Status.CurrentJobs[0]
	delete(fc.liveJobs, jobRef.Namespace+"/"+jobRef.Name)

	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatal(err)
	}
	if wwr.Status.FailedJobs != 1 {
		t.Errorf("expected missing job to count as failed, got FailedJobs=%d", wwr.Status.FailedJobs)
	}
}

// --------------------------------------------------------------------------
// Done WWR with no TTL: checkTTL is a no-op -> no extra UpdateWWRStatus call
// --------------------------------------------------------------------------

func TestReconcile_DoneWWRIsSkipped(t *testing.T) {
	fc := newFakeClient(t)
	wwr := makeWWR("default", "wwr-done", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}
	wwr.Status.Done = true
	fc.addWWR(wwr)

	r := NewReconciler(fc)
	initialUpdateCalls := fc.updateStatusCalls.Load()
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fc.updateStatusCalls.Load() != initialUpdateCalls {
		t.Errorf("UpdateWWRStatus should not be called for a done WWR with no TTL")
	}
	if len(fc.deleteWWRCalls) != 0 {
		t.Errorf("DeleteWWR should not be called when TTL is not set")
	}
}

// --------------------------------------------------------------------------
// TTL: WWR is deleted after TTL seconds have elapsed
// --------------------------------------------------------------------------

func TestReconcile_TTLExpired(t *testing.T) {
	fc := newFakeClient(t)
	wwr := makeWWR("default", "wwr-ttl", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}

	ttl := int32(60)
	wwr.Spec.TTLSecondsAfterFinished = &ttl
	wwr.Status.Done = true
	// Set CompletionTime well in the past so TTL is already exceeded.
	past := time.Now().Add(-2 * time.Minute)
	wwr.Status.CompletionTime = &past
	fc.addWWR(wwr)

	r := NewReconciler(fc)
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fc.deleteWWRCalls) != 1 || fc.deleteWWRCalls[0] != "default/wwr-ttl" {
		t.Errorf("expected WWR to be deleted after TTL expiry, deleteWWRCalls=%v", fc.deleteWWRCalls)
	}
}

func TestReconcile_TTLNotYetExpired(t *testing.T) {
	fc := newFakeClient(t)
	wwr := makeWWR("default", "wwr-ttl", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}

	ttl := int32(3600) // 1 hour.
	wwr.Spec.TTLSecondsAfterFinished = &ttl
	wwr.Status.Done = true
	// CompletionTime just now -> TTL not yet exceeded.
	now := time.Now()
	wwr.Status.CompletionTime = &now
	fc.addWWR(wwr)

	r := NewReconciler(fc)
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fc.deleteWWRCalls) != 0 {
		t.Errorf("expected WWR not to be deleted before TTL expiry, deleteWWRCalls=%v", fc.deleteWWRCalls)
	}
}

func TestReconcile_TTLZero(t *testing.T) {
	fc := newFakeClient(t)
	wwr := makeWWR("default", "wwr-ttl-zero", "hook")
	wwr.Metadata.Finalizers = []string{types.FinalizerCleanup}

	ttl := int32(0) // delete immediately
	wwr.Spec.TTLSecondsAfterFinished = &ttl
	wwr.Status.Done = true
	past := time.Now().Add(-1 * time.Second)
	wwr.Status.CompletionTime = &past
	fc.addWWR(wwr)

	r := NewReconciler(fc)
	if err := r.reconcileWWR(wwr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fc.deleteWWRCalls) != 1 {
		t.Errorf("expected immediate deletion with TTL=0, deleteWWRCalls=%v", fc.deleteWWRCalls)
	}
}

// --------------------------------------------------------------------------
// generateMirroredSecretName
// --------------------------------------------------------------------------

func TestGenerateMirroredSecretName(t *testing.T) {
	name := generateMirroredSecretName("my-wwr")
	if len(name) > 253 {
		t.Errorf("secret name exceeds 253 chars: %d", len(name))
	}
	if !strings.Contains(name, "-request-") {
		t.Errorf("secret name %q does not contain %q", name, "-request-")
	}
}

func TestGenerateMirroredSecretName_LongWWRName(t *testing.T) {
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'a'
	}
	name := generateMirroredSecretName(string(long))
	if len(name) > 253 {
		t.Errorf("secret name from long wwr name exceeds 253 chars: %d", len(name))
	}
}

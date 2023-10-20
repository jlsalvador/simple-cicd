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

package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"go.uber.org/zap/zapcore"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	simplecicdv1alpha1 "github.com/jlsalvador/simple-cicd/api/v1alpha1"
	"github.com/jlsalvador/simple-cicd/pkg/common"
	"github.com/jlsalvador/simple-cicd/pkg/rfc1123"
)

var DEBUG = false

// Duration to the next reconciliation.
var requeueAfter = func() time.Duration {
	if DEBUG {
		return time.Second * 10
	} else {
		return time.Millisecond * 500
	}
}()

const conditionMessageWaitingForCurrentJobs = "Waiting for current jobs"

var wwrLog = ctrl.Log.WithName("workflowWebhookRequest controller")
var wwrLogDebug = func() logr.Logger {
	if DEBUG {
		return zap.New(
			zap.UseDevMode(true),
			zap.Level(zapcore.DebugLevel),
		)
	} else {
		return zap.New(
			zap.UseDevMode(true),
			zap.Level(zapcore.DebugLevel),
			zap.WriteTo(io.Discard),
		)
	}
}()

// WorkflowWebhookRequestReconciler reconciles a WorkflowWebhookRequest object
type WorkflowWebhookRequestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflowwebhookrequests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflowwebhookrequests/finalizers,verbs=update
//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflowwebhooks,verbs=get;list
//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflowwebhooks/finalizers,verbs=update
//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflows,verbs=get;list;watch
//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflows/finalizers,verbs=update
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.0/pkg/reconcile
func (r *WorkflowWebhookRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	wwrLog.WithValues("run", req.NamespacedName)

	wwr, err := r.ensureWorkflowWebhookRequest(ctx, req.NamespacedName)
	if err != nil {
		return ctrl.Result{}, err
	} else if wwr == nil {
		// Object deleted, do nothing.
		return ctrl.Result{}, nil
	}

	if wwr.Status.Done {
		// This WorkflowWebhookRequest is Done, do nothing.
		return ctrl.Result{}, nil
	}

	if err := r.reconcileConditions(ctx, wwr); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileCurrentWorkFlows(ctx, wwr); err != nil {
		return ctrl.Result{}, err
	}

	secret, err := r.ensureSecret(ctx, wwr)
	if err != nil {
		return ctrl.Result{}, err
	}

	if requeue, err := r.reconcileCurrentJobs(ctx, wwr, secret); err != nil {
		return ctrl.Result{}, err
	} else if requeue {
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	if requeue, err := r.checkCurrentJobs(ctx, wwr); err != nil {
		return ctrl.Result{}, err
	} else if requeue {
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	// If you are here, WorkflowWebhookRequest has no jobs to do, so do nothing
	return ctrl.Result{}, nil
}

// Fetch the WorkflowWebhookRequest instance
//
// The purpose is check if the Custom Resource for the Kind WorkflowWebhookRequest
// is applied on the cluster if not we return nil to stop the reconciliation
func (r *WorkflowWebhookRequestReconciler) ensureWorkflowWebhookRequest(ctx context.Context, wwrnn types.NamespacedName) (*simplecicdv1alpha1.WorkflowWebhookRequest, error) {
	wwr := &simplecicdv1alpha1.WorkflowWebhookRequest{}
	if err := r.Get(ctx, wwrnn, wwr); err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then, it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			wwrLogDebug.Info("resource not found. Ignoring since object must be deleted", "WorkflowWebhookRequest", wwrnn)
			return nil, nil
		}
		// Error reading the object - requeue the request.
		emsg := fmt.Sprintf("Failed to get workflowWebhookRequest %q", wwrnn)
		wwrLog.Error(err, emsg)
		return nil, errors.Join(err, errors.New(emsg))
	}
	wwrLogDebug.Info("WorkflowWebhookRequest fetched", "WorkflowWebhookRequest", wwrnn)
	return wwr, nil
}

// If there are not any WorkflowWebhookRequest.Status.Conditions just set a first
// one as "Progressing"
func (r *WorkflowWebhookRequestReconciler) reconcileConditions(ctx context.Context, wwr *simplecicdv1alpha1.WorkflowWebhookRequest) error {
	if wwr.Status.Conditions != nil {
		return nil
	}

	wwrLog.Info("request received", "WorkflowWebhookRequest", wwr.Namespace+"/"+wwr.Name)

	// Save "Progressing" status
	wwr.Status.Conditions = []simplecicdv1alpha1.Condition{{
		Type:               string(simplecicdv1alpha1.WorkflowWebhookRequestProgressing),
		Status:             simplecicdv1alpha1.ConditionUnknown,
		Reason:             "Reconciling",
		Message:            "Starting reconciliation",
		LastTransitionTime: metav1.Now(),
	}}
	return r.updateWwr(ctx, wwr)
}

// Ensure that the Secret with the payload (headers, body, method, etc) exists
func (r *WorkflowWebhookRequestReconciler) ensureSecret(ctx context.Context, wwr *simplecicdv1alpha1.WorkflowWebhookRequest) (secret *corev1.Secret, err error) {
	secretNamespacedName := types.NamespacedName{
		Namespace: wwr.Namespace,
		Name:      wwr.Name,
	}
	secret = &corev1.Secret{}
	if err := r.Get(ctx, secretNamespacedName, secret); err != nil {
		if !apierrors.IsNotFound(err) {
			wwrLog.Error(err, "unexpected behaviour")
			return nil, err
		}

		// Secret not found, so let's create a new one
		secret = createSecret(wwr)

		// Set the ownerRef for the Secret
		// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
		if err := ctrl.SetControllerReference(wwr, secret, r.Scheme); err != nil {
			emsg := fmt.Errorf(`can not set reference for Secret %s/%s as %s/%s`, secret.Namespace, secret.Name, wwr.Namespace, wwr.Name)
			wwrLog.Error(err, emsg.Error(), "Secret", secret)
			return nil, errors.Join(err, emsg)
		}

		// Create new Secret
		if err := r.Create(ctx, secret); err != nil {
			emsg := fmt.Errorf(`can not create new Secret "%s/%s"`, secret.Namespace, secret.Name)
			wwrLog.Error(err, emsg.Error(), "Secret", secret)
			return nil, errors.Join(err, emsg)
		}
		wwrLogDebug.Info("New Secret created", "Secret", secret)
	}
	return secret, nil
}

// Let's just fill the Status.CurrentWorkflows when it is not available
func (r *WorkflowWebhookRequestReconciler) reconcileCurrentWorkFlows(ctx context.Context, wwr *simplecicdv1alpha1.WorkflowWebhookRequest) error {
	if wwr.Status.CurrentWorkflows != nil {
		return nil
	}

	// Fetch the ref. WorkflowWebhook
	wwnn := wwr.Spec.WorkflowWebhook.AsType(wwr.Namespace)
	ww := &simplecicdv1alpha1.WorkflowWebhook{}
	if err := r.Get(ctx, wwnn, ww); err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then, it usually means that it
			// was deleted or not created. In this way, we will stop the reconciliation
			wwrLog.Info("resource not found. Ignoring since object must be deleted", "WorkflowWebhook", wwnn)
			return nil
		}
		// Error reading the object - requeue the request.
		emsg := fmt.Errorf("failed to get workflowWebhook %s", wwr.Spec.WorkflowWebhook)
		wwrLog.Error(err, emsg.Error(), "WorkflowWebhookRequest", wwr)
		return errors.Join(err, emsg)
	}
	wwrLogDebug.Info("WorkflowWebhook fetched", "WorkflowWebhook", wwnn)

	wwr.Status.CurrentWorkflows = ww.Spec.Workflows
	return r.updateWwr(ctx, wwr)
}

// Let's just fill the Status.CurrentJobs
func (r *WorkflowWebhookRequestReconciler) reconcileCurrentJobs(ctx context.Context, wwr *simplecicdv1alpha1.WorkflowWebhookRequest, secret *corev1.Secret) (requeue bool, err error) {
	if wwr.Status.CurrentJobs != nil {
		return false, nil
	}

	i := 0
	// Create Jobs from each WorkflowWebhook.Spec.Workflows
	for _, workflowNamespacedName := range wwr.Status.CurrentWorkflows {
		// Fetch Workflow that has Jobs to be cloned
		workflow := &simplecicdv1alpha1.Workflow{}
		if err := r.Get(ctx, workflowNamespacedName.AsType(wwr.Namespace), workflow); err != nil {
			emsg := fmt.Errorf("can not fetch Workflow %s", workflowNamespacedName)
			wwrLog.Error(err, emsg.Error())
			return false, errors.Join(err, emsg)
		}

		// Skip suspended Workflow
		if workflow.Spec.Suspend != nil && *workflow.Spec.Suspend {
			continue
		}

		// Create a Job for each Workflow.Spec.JobsToBeCloned and add ref. into
		// WorkflowWebhookRequest.Spec.CurrentJobs
		for _, jtbc := range workflow.Spec.JobsToBeCloned {
			// Ensure jtbc.Namespace
			ns := common.DefaultString(jtbc.Namespace, wwr.Namespace)
			jtbc.Namespace = &ns

			// Clone as a new Job from suspended Job.
			if err := r.createJob(ctx, jtbc, wwr, workflow, i, secret); err != nil {
				emsg := fmt.Errorf("something happened while cloning job %s", jtbc)
				wwrLog.Error(err, emsg.Error())
				return false, errors.Join(err, emsg)
			}
			i++
		}
	}

	if len(wwr.Status.Conditions) > 0 && wwr.Status.Conditions[len(wwr.Status.Conditions)-1].Message != conditionMessageWaitingForCurrentJobs {
		wwr.Status.Conditions = append(wwr.Status.Conditions, simplecicdv1alpha1.Condition{
			Type:               string(simplecicdv1alpha1.WorkflowWebhookRequestWaiting),
			Status:             simplecicdv1alpha1.ConditionUnknown,
			Reason:             "Reconciling",
			Message:            conditionMessageWaitingForCurrentJobs,
			LastTransitionTime: metav1.Now(),
		})
	}

	err = r.updateWwr(ctx, wwr)

	// Requeue if there is not error
	return err == nil, err
}

func (r *WorkflowWebhookRequestReconciler) createJob(
	ctx context.Context,
	jtbc simplecicdv1alpha1.NamespacedName,
	wwr *simplecicdv1alpha1.WorkflowWebhookRequest,
	workflow *simplecicdv1alpha1.Workflow,
	jobIndex int,
	secret *corev1.Secret,
) error {
	// Create new labels
	wwnn := wwr.Spec.WorkflowWebhook
	labels := simplecicdv1alpha1.GetJobLabels(
		*jtbc.Namespace,
		jtbc.Name,
		workflow.Namespace,
		workflow.Name,
		common.DefaultString(wwnn.Namespace, wwr.Namespace),
		wwnn.Name,
		wwr.Namespace,
		wwr.Name,
	)

	job := &batchv1.Job{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: *jtbc.Namespace,
		Name:      jtbc.Name,
	}, job); err != nil {
		emsg := fmt.Errorf(`can not fetch Job "%s"`, jtbc.String())
		wwrLog.Error(err, emsg.Error())
		return errors.Join(err, emsg)
	}

	// Generate new Job NamespacedName
	fullUnsafeName := fmt.Sprintf(
		"%s-%d-%d-%s",
		wwr.Name,
		wwr.Status.Iterations,
		jobIndex,
		job.Name,
	)
	newJobNamespacedName := simplecicdv1alpha1.NamespacedName{
		Namespace: &wwr.Namespace,
		Name:      rfc1123.GenerateSafeLengthName(fullUnsafeName),
	}

	// Check if the Job already exists
	newJob := &batchv1.Job{}
	if err := r.Get(ctx, newJobNamespacedName.AsType(wwr.Namespace), newJob); err != nil {
		if !apierrors.IsNotFound(err) {
			emsg := fmt.Errorf(`can not fetch Job %q`, newJobNamespacedName)
			wwrLog.Error(err, emsg.Error())
			return errors.Join(err, emsg)
		}

		// New Job not found, clone suspended Job.
		newJob = cloneJob(job, newJobNamespacedName, labels)
		appendSecretVolumeToJob(newJob, *secret)

		// Set the ownerRef for the Job
		// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
		if err := ctrl.SetControllerReference(wwr, newJob, r.Scheme); err != nil {
			emsg := fmt.Errorf(`can not set reference for Job %s/%s as %s/%s`, *jtbc.Namespace, jtbc.Name, wwr.Namespace, wwr.Name)
			wwrLog.Error(err, emsg.Error())
			return errors.Join(err, emsg)
		}
		wwrLogDebug.Info("New Job to be created", "Job", newJob)

		// Create new Job
		if err := r.Create(ctx, newJob); err != nil {
			emsg := fmt.Errorf(`can not create new Job "%s/%s" from Job "%s/%s"`, newJob.Namespace, newJob.Name, job.Namespace, job.Name)
			wwrLog.Error(err, emsg.Error())
			return errors.Join(err, emsg)
		}
	}

	wwr.Status.CurrentJobs = append(wwr.Status.CurrentJobs, newJobNamespacedName)
	wwrLog.Info("added job", "WorkflowWebhookRequest", wwr.Namespace+"/"+wwr.Name, "Job", newJobNamespacedName)
	return nil
}

// If WorkflowWebhookRequest has some queued Jobs, check their status, requeue it
// if it is necessary, and progress to the next workflows.
func (r *WorkflowWebhookRequestReconciler) checkCurrentJobs(ctx context.Context, wwr *simplecicdv1alpha1.WorkflowWebhookRequest) (bool, error) {
	if len(wwr.Status.CurrentJobs) == 0 {
		return false, nil
	}

	// Check current Jobs Status.
	// Requeue if there are uncompleted Jobs.
	jobs := []*batchv1.Job{}
	for _, jobNamespacedName := range wwr.Status.CurrentJobs {
		job := &batchv1.Job{}
		if err := r.Get(ctx, jobNamespacedName.AsType(wwr.Namespace), job); err != nil {
			emsg := fmt.Errorf(`can not fetch Job "%s"`, jobNamespacedName)
			wwrLog.Error(err, emsg.Error(), "WorkflowWebhookRequest", wwr)
			return false, errors.Join(err, emsg)
		}

		isDone, _ := jobStatus(job)
		if !isDone {
			wwrLogDebug.Info("Job is running, requeue reconciliation", "Job", jobNamespacedName)
			return true, nil
		}

		jobs = append(jobs, job)
	}

	// Update successful and failures Jobs counters
	nSuccessfulCurrentIteration := 0
	nFailuresCurrentIteration := 0
	for _, job := range jobs {
		_, isError := jobStatus(job)
		if isError {
			wwr.Status.FailedJobs++
			nFailuresCurrentIteration++
		} else {
			wwr.Status.SuccessfulJobs++
			nSuccessfulCurrentIteration++
		}
	}

	// Calculate CurrentWorkflow for the next iteration
	nextWorkflows := []simplecicdv1alpha1.NamespacedName{}
	for _, wnn := range wwr.Status.CurrentWorkflows {
		jFromW, nFailures := jobsFromWorkflow(wnn, jobs, wwr.Namespace)

		w := &simplecicdv1alpha1.Workflow{}
		if err := r.Get(ctx, wnn.AsType(wwr.Namespace), w); err != nil {
			emsg := fmt.Errorf("can not fetch Workflow %s", wnn)
			wwrLog.Error(err, emsg.Error(), "WorkflowWebhookRequest", wwr)
			return false, errors.Join(err, emsg)
		}

		for _, nw := range w.Spec.Next {
			if isConditionWhenValid(nw.When, len(jFromW), nFailures) {
				nextWorkflows = append(nextWorkflows, nw.AsNamespacedName())
			}
		}
	}
	nNextWorkflows := len(nextWorkflows)

	// CurrentJobs are completed, clean up for the next iteration
	wwr.Status.CurrentJobs = []simplecicdv1alpha1.NamespacedName{}
	wwr.Status.CurrentWorkflows = nextWorkflows
	wwr.Status.Iterations++

	// Status for the current iteration
	wwr.Status.Conditions = append(wwr.Status.Conditions, simplecicdv1alpha1.Condition{
		Type:               string(simplecicdv1alpha1.WorkflowWebhookRequestProgressing),
		Status:             simplecicdv1alpha1.ConditionUnknown,
		Reason:             "Reconciling",
		Message:            fmt.Sprintf("On interation %d, there were %d successful Job(s) and %d failures, with %d Workflow(s) queued.", wwr.Status.Iterations, nSuccessfulCurrentIteration, nFailuresCurrentIteration, nNextWorkflows),
		LastTransitionTime: metav1.Now(),
	})

	// If WorkflowWebhookRequest is Done, set some Status as it.
	if nNextWorkflows == 0 {
		wwr.Status.Done = true // This WorkflowWebhookRequest is Done, no more Workflows
		wwr.Status.Conditions = append(wwr.Status.Conditions, simplecicdv1alpha1.Condition{
			Type:               string(simplecicdv1alpha1.WorkflowWebhookRequestDone),
			Status:             simplecicdv1alpha1.ConditionTrue,
			Reason:             "Reconciling",
			Message:            fmt.Sprintf("All Jobs are completed. There were %d successful Job(s) and %d failures over %d iteration(s).", wwr.Status.SuccessfulJobs, wwr.Status.FailedJobs, wwr.Status.Iterations),
			LastTransitionTime: metav1.Now(),
		})

		wwrLog.Info("request completed", "WorkflowWebhookRequest", wwr.Namespace+"/"+wwr.Name)
	}

	if err := r.updateWwr(ctx, wwr); err != nil {
		return false, err
	}

	// Requeue WorkflowWebhookRequest if there are more Workflows
	return nNextWorkflows > 0, nil
}

func jobsFromWorkflow(wnn simplecicdv1alpha1.NamespacedName, jobs []*batchv1.Job, defaultNamespace string) (jobsFromWorkflow []*batchv1.Job, nFailures int) {
	jobsFromWorkflow = []*batchv1.Job{}
	for _, job := range jobs {
		if job.ObjectMeta.Labels[simplecicdv1alpha1.LabelWorkFlowNamespace] == common.DefaultString(wnn.Namespace, defaultNamespace) && job.ObjectMeta.Labels[simplecicdv1alpha1.LabelWorkFlowName] == wnn.Name {
			jobsFromWorkflow = append(jobsFromWorkflow, job)
			if _, isError := jobStatus(job); isError {
				nFailures++
			}
		}
	}
	return
}

// Update WorkflowWebhookRequest and its .Status subresource
func (r *WorkflowWebhookRequestReconciler) updateWwr(ctx context.Context, wwr *simplecicdv1alpha1.WorkflowWebhookRequest) error {
	if err := r.Update(ctx, wwr); err != nil {
		wwrLog.Error(err, "Failed to update WorkflowWebhookRequest")
		return err
	}
	wwrLogDebug.Info("Updated WorkflowWebhookRequest", "WorkflowWebhookRequest", wwr)
	return nil
}

// Checks whether a given 'when' condition is valid based on the number of
// successful and failed jobs.
//
// Parameters:
//   - when: The condition to be checked.
//   - nJobs: The total number of jobs.
//   - nErrors: The number of jobs that have encountered errors.
//
// Returns:
//   - true if the condition is valid based on the next criteria, otherwise false.
//
// The conditions are as follows:
//   - If 'when' is empty or 'Always', the function always returns true.
//   - If 'when' is 'OnAnyFailure' or 'OnAnySuccess', it returns true if there are errors (nErrors > 0) but not all jobs have failed (nErrors < nJobs).
//   - If 'when' is 'OnSuccess' or 'OnAnySuccess', it returns true if there are no errors (nErrors == 0).
//   - If 'when' is 'OnFailure' or 'OnAnyFailure', it returns true if all jobs have failed (nErrors == nJobs).
func isConditionWhenValid(when *simplecicdv1alpha1.When, nJobs int, nFailures int) bool {
	return (when == nil || *when == simplecicdv1alpha1.Always) ||
		(nFailures > 0 && nFailures < nJobs && (*when == simplecicdv1alpha1.OnAnyFailure || *when == simplecicdv1alpha1.OnAnySuccess)) ||
		(nFailures == 0 && (*when == simplecicdv1alpha1.OnSuccess || *when == simplecicdv1alpha1.OnAnySuccess)) ||
		(nFailures == nJobs && (*when == simplecicdv1alpha1.OnFailure || *when == simplecicdv1alpha1.OnAnyFailure))
}

// Returns if Job is Done and if it is Failed
func jobStatus(job *batchv1.Job) (done bool, failed bool) {
	for _, c := range job.Status.Conditions {
		switch c.Type {
		case batchv1.JobFailed:
			wwrLogDebug.Info("Job was Failed", "Job", job)
			return true, true
		case batchv1.JobComplete:
			return true, false
		}
	}
	return false, false
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkflowWebhookRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&simplecicdv1alpha1.WorkflowWebhookRequest{}).
		Complete(r)
}

// Mount each Secret key as a file inside every Job (init)containers.
//
// Expected mounted files:
//   - /var/run/secrets/kubernetes.io/request/host
//   - /var/run/secrets/kubernetes.io/request/method
//   - /var/run/secrets/kubernetes.io/request/url
//   - /var/run/secrets/kubernetes.io/request/headers
//   - /var/run/secrets/kubernetes.io/request/body
func appendSecretVolumeToJob(job *batchv1.Job, secret corev1.Secret) {
	if job == nil {
		return
	}

	// Append Secret volume
	job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: secret.Name,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secret.Name,
			},
		},
	})

	// Append VolumeMounts to each (init)containers
	for _, containers := range [][]corev1.Container{
		job.Spec.Template.Spec.InitContainers,
		job.Spec.Template.Spec.Containers,
	} {
		for i := range containers {
			for key := range secret.Data {
				containers[i].VolumeMounts = append(
					containers[i].VolumeMounts,
					corev1.VolumeMount{
						Name:      secret.Name,
						MountPath: fmt.Sprintf("/var/run/secrets/kubernetes.io/request/%s", key),
						SubPath:   key,
						ReadOnly:  true,
					},
				)
			}
		}
	}
}

// Creates a Secret with each WorkflowWebhookRequest payload.
//
// The Secret will contains:
//   - WorkflowWebhookRequest.Spec.Host
//   - WorkflowWebhookRequest.Spec.Method
//   - WorkflowWebhookRequest.Spec.Url
//   - WorkflowWebhookRequest.Spec.Headers
//   - WorkflowWebhookRequest.Spec.Body
func createSecret(wwr *simplecicdv1alpha1.WorkflowWebhookRequest) *corev1.Secret {
	headers := func(headers http.Header) string {
		hs := []string{}
		for k, vs := range headers {
			for _, v := range vs {
				hs = append(hs, fmt.Sprintf("%s: %s", k, v))
			}
		}
		return strings.Join(hs, "\n")
	}(wwr.Spec.Headers)

	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      wwr.Name,
			Namespace: wwr.Namespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"host":    []byte(wwr.Spec.Host),
			"method":  []byte(wwr.Spec.Method),
			"url":     []byte(wwr.Spec.Url),
			"headers": []byte(headers),
			"body":    wwr.Spec.Body,
		},
	}
}

// We do not job.DeepCopy() because labels, annotations, and objectrefs (explicit is better than implicit)
func cloneJob(job *batchv1.Job, njnn simplecicdv1alpha1.NamespacedName, newLabels map[string]string) *batchv1.Job {
	njSuspend := false
	nj := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: job.TypeMeta.APIVersion,
			Kind:       job.TypeMeta.Kind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      njnn.Name,
			Namespace: *njnn.Namespace,
			Labels:    newLabels,
		},
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds:   job.Spec.ActiveDeadlineSeconds,
			BackoffLimit:            job.Spec.BackoffLimit,
			BackoffLimitPerIndex:    job.Spec.BackoffLimitPerIndex,
			CompletionMode:          job.Spec.CompletionMode,
			Completions:             job.Spec.Completions,
			MaxFailedIndexes:        job.Spec.MaxFailedIndexes,
			Parallelism:             job.Spec.Parallelism,
			PodFailurePolicy:        job.Spec.PodFailurePolicy,
			PodReplacementPolicy:    job.Spec.PodReplacementPolicy,
			Suspend:                 &njSuspend,
			TTLSecondsAfterFinished: job.Spec.TTLSecondsAfterFinished,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					ActiveDeadlineSeconds:         job.Spec.Template.Spec.ActiveDeadlineSeconds,
					Affinity:                      job.Spec.Template.Spec.Affinity,
					AutomountServiceAccountToken:  job.Spec.Template.Spec.AutomountServiceAccountToken,
					Containers:                    job.Spec.Template.Spec.Containers,
					DNSConfig:                     job.Spec.Template.Spec.DNSConfig,
					DNSPolicy:                     job.Spec.Template.Spec.DNSPolicy,
					EnableServiceLinks:            job.Spec.Template.Spec.EnableServiceLinks,
					EphemeralContainers:           job.Spec.Template.Spec.EphemeralContainers,
					HostAliases:                   job.Spec.Template.Spec.HostAliases,
					HostIPC:                       job.Spec.Template.Spec.HostIPC,
					Hostname:                      job.Spec.Template.Spec.Hostname,
					HostNetwork:                   job.Spec.Template.Spec.HostNetwork,
					HostPID:                       job.Spec.Template.Spec.HostPID,
					HostUsers:                     job.Spec.Template.Spec.HostUsers,
					ImagePullSecrets:              job.Spec.Template.Spec.ImagePullSecrets,
					InitContainers:                job.Spec.Template.Spec.InitContainers,
					NodeName:                      job.Spec.Template.Spec.NodeName,
					NodeSelector:                  job.Spec.Template.Spec.NodeSelector,
					OS:                            job.Spec.Template.Spec.OS,
					Overhead:                      job.Spec.Template.Spec.Overhead,
					PreemptionPolicy:              job.Spec.Template.Spec.PreemptionPolicy,
					Priority:                      job.Spec.Template.Spec.Priority,
					PriorityClassName:             job.Spec.Template.Spec.PriorityClassName,
					ReadinessGates:                job.Spec.Template.Spec.ReadinessGates,
					ResourceClaims:                job.Spec.Template.Spec.ResourceClaims,
					RestartPolicy:                 job.Spec.Template.Spec.RestartPolicy,
					RuntimeClassName:              job.Spec.Template.Spec.RuntimeClassName,
					SchedulerName:                 job.Spec.Template.Spec.SchedulerName,
					SchedulingGates:               job.Spec.Template.Spec.SchedulingGates,
					SecurityContext:               job.Spec.Template.Spec.SecurityContext,
					ServiceAccountName:            job.Spec.Template.Spec.ServiceAccountName,
					SetHostnameAsFQDN:             job.Spec.Template.Spec.SetHostnameAsFQDN,
					ShareProcessNamespace:         job.Spec.Template.Spec.ShareProcessNamespace,
					Subdomain:                     job.Spec.Template.Spec.Subdomain,
					TerminationGracePeriodSeconds: job.Spec.Template.Spec.TerminationGracePeriodSeconds,
					Tolerations:                   job.Spec.Template.Spec.Tolerations,
					TopologySpreadConstraints:     job.Spec.Template.Spec.TopologySpreadConstraints,
					Volumes:                       job.Spec.Template.Spec.Volumes,
				},
			},
		},
	}
	return nj
}

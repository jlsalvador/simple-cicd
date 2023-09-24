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
	"net/http"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	simplecicdv1alpha1 "github.com/jlsalvador/simple-cicd/api/v1alpha1"
	"github.com/jlsalvador/simple-cicd/internal/rfc1123"
)

// WorkflowWebhookRequestReconciler reconciles a WorkflowWebhookRequest object
type WorkflowWebhookRequestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflowwebhookrequests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflowwebhookrequests/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflowwebhookrequests/finalizers,verbs=update
//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflowwebhooks,verbs=get;list
//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflowwebhooks/status,verbs=get;list
//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflowwebhooks/finalizers,verbs=update
//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflows,verbs=get;list;watch
//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflows/status,verbs=get;list
//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflows/finalizers,verbs=update
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.0/pkg/reconcile
func (r *WorkflowWebhookRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the WorkflowWebhookRequest instance
	// The purpose is check if the Custom Resource for the Kind WorkflowWebhookRequest
	// is applied on the cluster if not we return nil to stop the reconciliation
	wwr := &simplecicdv1alpha1.WorkflowWebhookRequest{}
	if err := r.Get(ctx, req.NamespacedName, wwr); err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then, it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			log.Info("workflowWebhookRequest resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		emsg := fmt.Sprintf("Failed to get workflowWebhookRequest %q", req.NamespacedName)
		log.Error(err, emsg)
		return ctrl.Result{}, errors.Join(err, errors.New(emsg))
	}
	log.Info(
		"WorkflowWebhookRequest fetched",
		"WorkflowWebhookRequest", wwr,
		"Spec", wwr.Spec,
		"Status", wwr.Status,
	)

	// This WorkflowWebhookRequest is Done, do nothing
	if wwr.Spec.Done {
		return ctrl.Result{}, nil
	}

	// If there are not any WorkflowWebhookRequest.Status.Conditions just set a first one as "Progressing"
	if wwr.Status.Conditions == nil {
		// Save "Progressing" status
		meta.SetStatusCondition(&wwr.Status.Conditions, metav1.Condition{
			Type:    string(simplecicdv1alpha1.WorkflowWebhookRequestProgressing),
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation",
		})
		if err := r.Status().Update(ctx, wwr); err != nil {
			log.Error(err, "Failed to update WorkflowWebhookRequest status")
			return ctrl.Result{}, err
		}
		log.Info(
			"Updated WorkflowWebhookRequest.Status",
			"WorkflowWebhookRequest", wwr,
			"Spec", wwr.Spec,
			"Status", wwr.Status,
		)
	}

	// Ensure that the Secret with the payload (headers, body, method, etc) exists
	secret := &corev1.Secret{}
	if err := r.Get(ctx, req.NamespacedName, secret); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "unexpected behaviour")
			return ctrl.Result{}, err
		}

		// Secret not found, so let's create a new one
		secret = createSecret(wwr)

		// Set the ownerRef for the Secret
		// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
		if err := ctrl.SetControllerReference(wwr, secret, r.Scheme); err != nil {
			emsg := fmt.Errorf(`can not set reference for Secret %s/%s as %s/%s`, secret.Namespace, secret.Name, wwr.Namespace, wwr.Name)
			log.Error(err, emsg.Error())
			return ctrl.Result{}, errors.Join(err, emsg)
		}
		log.Info(
			"New Secret to be created",
			"Secret", secret,
		)

		// Create new Secret
		if err := r.Create(ctx, secret); err != nil {
			emsg := fmt.Errorf(`can not create new Secret "%s/%s"`, secret.Namespace, secret.Name)
			log.Error(err, emsg.Error())
			return ctrl.Result{}, errors.Join(err, emsg)
		}
		log.Info("New Secret created", "Secret", secret)
	}

	// Let's just fill the Status.CurrentWorkflows when it is not available
	if wwr.Spec.CurrentWorkflows == nil {
		log.Info(
			"Updating WorkflowWebhookRequest.Spec.CurrentWorkflows",
			"WorkflowWebhookRequest", wwr,
			"Spec", wwr.Spec,
			"Status", wwr.Status,
		)

		// Fetch the ref. WorkflowWebhook
		ww := &simplecicdv1alpha1.WorkflowWebhook{}
		if err := r.Get(ctx, wwr.Spec.WorkflowWebhook.AsType(wwr.Namespace), ww); err != nil {
			if apierrors.IsNotFound(err) {
				// If the custom resource is not found then, it usually means that it was deleted or not created
				// In this way, we will stop the reconciliation
				log.Info("workflowWebhook resource not found. Ignoring since object must be deleted")
				return ctrl.Result{}, nil
			}
			// Error reading the object - requeue the request.
			emsg := fmt.Sprintf("Failed to get workflowWebhook %s", wwr.Spec.WorkflowWebhook)
			log.Error(err, emsg)
			return ctrl.Result{}, errors.Join(err, errors.New(emsg))
		}
		log.Info(
			"WorkflowWebhook fetched",
			"WorkflowWebhook", ww,
			"Spec", ww.Spec,
			"Status", ww.Status,
		)

		wwr.Spec.CurrentWorkflows = ww.Spec.Workflows
		if err := r.Update(ctx, wwr); err != nil {
			log.Error(err, "Failed to update WorkflowWebhookRequest.Spec.CurrentWorkflows")
			return ctrl.Result{}, err
		}
		log.Info(
			"Updated WorkflowWebhookRequest",
			"WorkflowWebhookRequest", wwr,
			"Spec", wwr.Spec,
			"Status", wwr.Status,
		)
	}

	// Let's just fill the Spec.NextWorkflows when it is not available
	if wwr.Spec.NextWorkflows == nil {
		log.Info(
			"Updating WorkflowWebhookRequest.Spec.NextWorkflows",
			"WorkflowWebhookRequest", wwr,
			"Spec", wwr.Spec,
			"Status", wwr.Status,
		)

		for _, workflowNamespacedName := range wwr.Spec.CurrentWorkflows {
			w := &simplecicdv1alpha1.Workflow{}
			if err := r.Get(ctx, workflowNamespacedName.AsType(wwr.Namespace), w); err != nil {
				emsg := fmt.Errorf("can not fetch Workflow %s", workflowNamespacedName)
				log.Error(err, emsg.Error())
				return ctrl.Result{}, errors.Join(err, emsg)
			}
			log.Info(
				"Append to WorkflowWebhookRequest.Spec.NextWorkflows",
				"Next", w.Spec.Next,
			)
			wwr.Spec.NextWorkflows = append(wwr.Spec.NextWorkflows, w.Spec.Next...)
		}
		if err := r.Update(ctx, wwr); err != nil {
			log.Error(err, "Failed to update WorkflowWebhookRequest status")
			return ctrl.Result{}, err
		}
		log.Info(
			"Updated WorkflowWebhookRequest",
			"Spec", wwr.Spec,
			"Status", wwr.Status,
		)
	}

	// Let's just fill the Spec.CurrentJobs
	if wwr.Spec.CurrentJobs == nil {
		log.Info(
			"Updating WorkflowWebhookRequest.Spec.CurrentJobs",
			"WorkflowWebhookRequest", wwr,
			"Status", wwr.Status,
			"Spec", wwr.Spec,
		)

		// Create Jobs from each WorkflowWebhook.Spec.Workflows
		for _, workflowNamespacedName := range wwr.Spec.CurrentWorkflows {

			// Fetch Workflow to fetch its jobs to be cloned
			workflow := &simplecicdv1alpha1.Workflow{}
			if err := r.Get(ctx, workflowNamespacedName.AsType(wwr.Namespace), workflow); err != nil {
				emsg := fmt.Errorf("can not fetch Workflow %s", workflowNamespacedName)
				log.Error(err, emsg.Error())
				return ctrl.Result{}, errors.Join(err, emsg)
			}

			// Create a Job for each Workflow.Spec.JobsToBeCloned and add ref. into WorkflowWebhookRequest.Spec.CurrentJobs
			numJobsToBeCloned := len(workflow.Spec.JobsToBeCloned)
			for i, jnn := range workflow.Spec.JobsToBeCloned {
				job := &batchv1.Job{}
				if err := r.Get(ctx, jnn.AsType(wwr.Namespace), job); err != nil {
					emsg := fmt.Errorf(`can not fetch Job "%s"`, jnn)
					log.Error(err, emsg.Error())
					return ctrl.Result{}, errors.Join(err, emsg)
				}
				log.Info(
					"Fetch Job to be cloned",
					"Job", job,
					"Spec", job.Spec,
					"Status", job.Status,
				)

				// Generate new Job NamespacedName
				var fullUnsafeName string
				wwrUid := strings.ReplaceAll(string(wwr.GetUID()), "-", "")
				if numJobsToBeCloned == 1 {
					fullUnsafeName = fmt.Sprintf("%s-%s-%s", wwr.Name, job.Name, wwrUid)
				} else {
					fullUnsafeName = fmt.Sprintf("%s-%s-%d-%s", wwr.Name, job.Name, i, wwrUid)
				}
				newJobNamespacedName := simplecicdv1alpha1.NamespacedName{
					Namespace: &workflow.Namespace,
					Name:      rfc1123.GenerateSafeLengthName(fullUnsafeName),
				}

				// Check if the Job already exists
				newJob := &batchv1.Job{}
				if err := r.Get(ctx, newJobNamespacedName.AsType(wwr.Namespace), newJob); err != nil {
					if !apierrors.IsNotFound(err) {
						emsg := fmt.Errorf(`can not fetch Job %q`, newJobNamespacedName)
						log.Error(err, emsg.Error())
						return ctrl.Result{}, errors.Join(err, emsg)
					}

					// New Job not found, create it
					// Clone job with Job.Spec.Suspend = false
					newJob = cloneJob(job, newJobNamespacedName)

					// Append Secret volume to Job containers
					appendSecretVolumeToJobContainers(newJob, *secret)

					// Set the ownerRef for the Job
					// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
					if err := ctrl.SetControllerReference(wwr, newJob, r.Scheme); err != nil {
						emsg := fmt.Errorf(`can not set reference for Job %s/%s as %s/%s`, *jnn.Namespace, jnn.Name, wwr.Namespace, wwr.Name)
						log.Error(err, emsg.Error())
						return ctrl.Result{}, errors.Join(err, emsg)
					}
					log.Info(
						"New Job to be created",
						"Job", newJob,
						"Spec", newJob.Spec,
						"Status", newJob.Status,
					)

					// Create new Job
					if err := r.Create(ctx, newJob); err != nil {
						emsg := fmt.Errorf(`can not create new Job "%s/%s" from Job "%s/%s"`, newJob.Namespace, newJob.Name, job.Namespace, job.Name)
						log.Error(err, emsg.Error())
						return ctrl.Result{}, errors.Join(err, emsg)
					}
					log.Info("New Job created", "Job", newJobNamespacedName)
				}

				wwr.Spec.CurrentJobs = append(wwr.Spec.CurrentJobs, newJobNamespacedName)
				log.Info("Added Job", "Job", newJobNamespacedName)

			} // End JobToBeCloned

		} // End Workflow

		// Update WorkflowWebhookRequest.Spec.CurrentJobs
		if err := r.Update(ctx, wwr); err != nil {
			log.Error(err, "Failed to update WorkflowWebhookRequest status")
			return ctrl.Result{}, err
		}
		log.Info(
			"Updated WorkflowWebhookRequest.Spec.CurrentJobs",
			"WorkflowWebhookRequest", wwr,
			"Status", wwr.Status,
			"Spec", wwr.Spec,
		)

		// Save "Waiting" status
		meta.SetStatusCondition(&wwr.Status.Conditions, metav1.Condition{
			Type:    string(simplecicdv1alpha1.WorkflowWebhookRequestWaiting),
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Waiting for current jobs",
		})
		if err := r.Status().Update(ctx, wwr); err != nil {
			log.Error(err, "Failed to update WorkflowWebhookRequest status")
			return ctrl.Result{}, err
		}

		// Stop here and check again after a bit
		return ctrl.Result{
			RequeueAfter: requeueAfter,
		}, nil
	}

	// If there are some queued Jobs, check its status in order to progress to the next ones.
	if len(wwr.Spec.CurrentJobs) > 0 {
		log.Info(
			"Checking each WorkflowWebhookRequest.CurrentJobs[].Status.Failed",
			"WorkflowWebhookRequest", wwr,
			"Spec", wwr.Spec,
			"Status", wwr.Status,
		)

		numJobs := len(wwr.Spec.CurrentJobs) // Save this value for later because we will empty WrokflowWebhookRequest.Spec.CurrentJobs
		numErrors := 0
		for _, jobNamespacedName := range wwr.Spec.CurrentJobs {

			log.Info("Fetching Job", "Job", jobNamespacedName)
			job := &batchv1.Job{}
			if err := r.Get(ctx, jobNamespacedName.AsType(wwr.Namespace), job); err != nil {
				emsg := fmt.Errorf(`can not fetch Job "%s"`, jobNamespacedName)
				log.Error(err, emsg.Error())
				return ctrl.Result{}, errors.Join(err, emsg)
			}
			log.Info(
				"Fetched Job",
				"Job", job,
				"Spec", job.Spec,
				"Status", job.Status,
			)

			// Check if job is done somehow
			isDone := false
		skipConditions:
			for _, c := range job.Status.Conditions {
				log.Info(
					"Job.Status.Conditions",
					"Job", job.Name,
					"Condition", c,
				)
				switch c.Type {
				case batchv1.JobFailed:
					log.Info(
						"Job was Failed",
						"Job", jobNamespacedName,
						"Failed", job.Status.Failed,
					)
					numErrors++
					isDone = true
					break skipConditions
				case batchv1.JobComplete:
					isDone = true
					break skipConditions
				}
			}

			// If Job is no done requeue WorkflowWebhookRequest
			if !isDone {
				log.Info(
					"Job is running, requeue reconciliation",
					"Job", jobNamespacedName,
					"Active", job.Status.Active,
				)
				return ctrl.Result{
					RequeueAfter: requeueAfter,
				}, nil
			}
		}

		// Empty current Jobs
		wwr.Spec.CurrentJobs = []simplecicdv1alpha1.NamespacedName{}

		// Empty current Workflows
		wwr.Spec.CurrentWorkflows = []simplecicdv1alpha1.NamespacedName{}

		// Set next Workflows as current Workflows
		for _, nextWorkflowNamespacedName := range wwr.Spec.NextWorkflows {
			log.Info(
				"Debug",
				"nextWorkflow", nextWorkflowNamespacedName,
				"numErrors", numErrors,
			)
			if (nextWorkflowNamespacedName.When == nil || *nextWorkflowNamespacedName.When == simplecicdv1alpha1.Always) ||
				(numErrors > 0 && numErrors < numJobs && (*nextWorkflowNamespacedName.When == simplecicdv1alpha1.OnAnyFailure || *nextWorkflowNamespacedName.When == simplecicdv1alpha1.OnAnySuccess)) ||
				(numErrors == 0 && *nextWorkflowNamespacedName.When == simplecicdv1alpha1.OnSuccess) ||
				(numErrors == numJobs && *nextWorkflowNamespacedName.When == simplecicdv1alpha1.OnFailure) {

				// Add next workflow as current
				wwr.Spec.CurrentWorkflows = append(wwr.Spec.CurrentWorkflows, nextWorkflowNamespacedName.AsNamespacedName())
			}
		}

		// Empty next Workflows
		wwr.Spec.NextWorkflows = []simplecicdv1alpha1.NextWorkflow{}

		// Check if there are more Workflows to process or if the WorkflowWebhookRequest is done
		var requeue bool
		var newCondition metav1.Condition
		numWorkflows := len(wwr.Spec.CurrentWorkflows)
		if numWorkflows == 0 {
			// Do not requeue WorkflowWebhookRequest, there are not more Workflows
			requeue = false
			wwr.Spec.Done = true // This WorkflowWebhookRequest is Done, no more Workflows

			newCondition = metav1.Condition{
				Type:    string(simplecicdv1alpha1.WorkflowWebhookRequestDone),
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: fmt.Sprintf("Every Job is done with %d errors", numErrors),
			}
		} else {
			// Requeue WorkflowWebhookRequest because there are new Workflows
			requeue = true

			newCondition = metav1.Condition{
				Type:    string(simplecicdv1alpha1.WorkflowWebhookRequestProgressing),
				Status:  metav1.ConditionUnknown,
				Reason:  "Reconciling",
				Message: fmt.Sprintf("%d workflows queued", numWorkflows),
			}
		}

		log.Info(
			"Updating WorkflowWebhookRequest",
			"WorkflowWebhookRequest", wwr,
			"Spec", wwr.Spec,
			"Status", wwr.Status,
		)
		if err := r.Update(ctx, wwr); err != nil {
			log.Error(err, "Failed to update WorkflowWebhookRequest status")
			return ctrl.Result{}, err
		}
		log.Info(
			"Updated WorkflowWebhookRequest",
			"WorkflowWebhookRequest", wwr,
			"Spec", wwr.Spec,
			"Status", wwr.Status,
		)

		meta.SetStatusCondition(&wwr.Status.Conditions, newCondition)
		if err := r.Status().Update(ctx, wwr); err != nil {
			log.Error(err, "Failed to update WorkflowWebhookRequest status")
			return ctrl.Result{}, err
		}

		// We are done
		return ctrl.Result{Requeue: requeue}, nil
	}

	// If you are here, WorkflowWebhookRequest has no jobs to do, so do nothing
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkflowWebhookRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&simplecicdv1alpha1.WorkflowWebhookRequest{}).
		Complete(r)
}

var requeueAfter = func() time.Duration {
	t, err := time.ParseDuration("10s")
	if err != nil {
		panic(err)
	}
	return t
}()

func appendSecretVolumeToJobContainers(newJob *batchv1.Job, secret corev1.Secret) {
	// Append Secret volume
	newJob.Spec.Template.Spec.Volumes = append(newJob.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: secret.Name,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secret.Name,
			},
		},
	})

	// Append VolumeMounts to each (init)containers
	for _, containers := range [][]corev1.Container{
		newJob.Spec.Template.Spec.InitContainers,
		newJob.Spec.Template.Spec.Containers,
	} {
		for i := range containers {
			for _, key := range []string{
				"host",
				"method",
				"url",
				"headers",
				"body",
			} {
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
func cloneJob(job *batchv1.Job, njnn simplecicdv1alpha1.NamespacedName) *batchv1.Job {
	njSuspend := false
	nj := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: job.TypeMeta.APIVersion,
			Kind:       job.TypeMeta.Kind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      njnn.Name,
			Namespace: *njnn.Namespace,
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

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
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	simplecicdv1alpha1 "github.com/jlsalvador/simple-cicd/api/v1alpha1"
)

var _ = Describe("WorkflowWebhookRequest controller", func() {

	const (
		nodeTimeout = time.Second * 60
		timeout     = time.Second * 10
		interval    = time.Second * 1
	)

	namespace := "default"
	namePrefix := "test-"
	suspend := true
	backoffLimit := int32(0)
	onSuccess := simplecicdv1alpha1.OnSuccess
	onFailure := simplecicdv1alpha1.OnFailure

	jobCatRequest := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.Identifier(),
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "echo-request",
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			Suspend:      &suspend,
			BackoffLimit: &backoffLimit,
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					RestartPolicy: v1.RestartPolicyNever,
					Containers: []v1.Container{
						{
							Name:  "echo-request",
							Image: "bash",
							Command: []string{
								"cat",
								"/var/run/secrets/kubernetes.io/request/body",
								"/var/run/secrets/kubernetes.io/request/headers",
								"/var/run/secrets/kubernetes.io/request/host",
								"/var/run/secrets/kubernetes.io/request/method",
								"/var/run/secrets/kubernetes.io/request/url",
							},
						},
					},
				},
			},
		},
	}
	jobSuccess := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.Identifier(),
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "success",
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			Suspend:      &suspend,
			BackoffLimit: &backoffLimit,
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					RestartPolicy: v1.RestartPolicyNever,
					Containers: []v1.Container{
						{
							Name:    "success",
							Image:   "bash",
							Command: []string{"exit", "0"},
						},
					},
				},
			},
		},
	}
	jobFailure := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.Identifier(),
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "failure",
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			Suspend:      &suspend,
			BackoffLimit: &backoffLimit,
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					RestartPolicy: v1.RestartPolicyNever,
					Containers: []v1.Container{
						{
							Name:    "failure",
							Image:   "bash",
							Command: []string{"exit", "1"},
						},
					},
				},
			},
		},
	}
	workflowCatRequest := &simplecicdv1alpha1.Workflow{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simplecicdv1alpha1.GroupVersion.Identifier(),
			Kind:       "Workflow",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "cat-request",
			Namespace: namespace,
		},
		Spec: simplecicdv1alpha1.WorkflowSpec{
			JobsToBeCloned: []simplecicdv1alpha1.NamespacedName{
				{Name: jobCatRequest.ObjectMeta.Name},
			},
		},
	}
	workflowAllSuccess := &simplecicdv1alpha1.Workflow{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simplecicdv1alpha1.GroupVersion.Identifier(),
			Kind:       "Workflow",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "all-success",
			Namespace: namespace,
		},
		Spec: simplecicdv1alpha1.WorkflowSpec{
			JobsToBeCloned: []simplecicdv1alpha1.NamespacedName{
				{Name: jobSuccess.ObjectMeta.Name},
				{Name: jobSuccess.ObjectMeta.Name},
			},
			Next: []simplecicdv1alpha1.NextWorkflow{
				{Name: workflowCatRequest.ObjectMeta.Name, When: &onSuccess},
			},
		},
	}
	workflowAllFails := &simplecicdv1alpha1.Workflow{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simplecicdv1alpha1.GroupVersion.Identifier(),
			Kind:       "Workflow",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "all-fails",
			Namespace: namespace,
		},
		Spec: simplecicdv1alpha1.WorkflowSpec{
			JobsToBeCloned: []simplecicdv1alpha1.NamespacedName{
				{Name: jobFailure.ObjectMeta.Name},
				{Name: jobFailure.ObjectMeta.Name},
			},
			Next: []simplecicdv1alpha1.NextWorkflow{
				{Name: workflowCatRequest.ObjectMeta.Name, When: &onFailure},
			},
		},
	}
	workflowAllSome := &simplecicdv1alpha1.Workflow{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simplecicdv1alpha1.GroupVersion.Identifier(),
			Kind:       "Workflow",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "some",
			Namespace: namespace,
		},
		Spec: simplecicdv1alpha1.WorkflowSpec{
			JobsToBeCloned: []simplecicdv1alpha1.NamespacedName{
				{Name: jobSuccess.ObjectMeta.Name},
				{Name: jobFailure.ObjectMeta.Name},
			},
		},
	}
	workflowWebhook := &simplecicdv1alpha1.WorkflowWebhook{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simplecicdv1alpha1.GroupVersion.Identifier(),
			Kind:       "WorkflowWebhook",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "start",
			Namespace: namespace,
		},
		Spec: simplecicdv1alpha1.WorkflowWebhookSpec{
			Workflows: []simplecicdv1alpha1.NamespacedName{
				{Name: workflowAllSuccess.ObjectMeta.Name},
			},
		},
	}

	Context("Generate test environment", func() {
		It("Should finish a WorkflowWebhookRequest", func(ctx SpecContext) {
			By("By creating all Jobs")
			for _, job := range []*batchv1.Job{
				jobCatRequest,
				jobSuccess,
				jobFailure,
			} {
				Expect(k8sClient.Create(ctx, job)).Should(Succeed())
				Eventually(func() bool {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Namespace: job.Namespace,
						Name:      job.Name,
					}, job)
					return err == nil
				}, timeout, interval).Should(BeTrue())
			}

			By("By creating all Workflows")
			for _, workflow := range []*simplecicdv1alpha1.Workflow{
				workflowCatRequest,
				workflowAllSuccess,
				workflowAllFails,
				workflowAllSome,
			} {
				Expect(k8sClient.Create(ctx, workflow)).Should(Succeed())
				Eventually(func() bool {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Namespace: workflow.Namespace,
						Name:      workflow.Name,
					}, workflow)
					return err == nil
				}, timeout, interval).Should(BeTrue())
			}

			By("By creating the WorkflowWebhook")
			Expect(k8sClient.Create(ctx, workflowWebhook)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: workflowWebhook.Namespace,
					Name:      workflowWebhook.Name,
				}, workflowWebhook)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			By("By trigger the creation of a WorkflowWebhookRequest through listener")
			//FIXME: Replace hard-coded address
			resp, err := http.Post(fmt.Sprintf("http://localhost:9000/%s/%s", workflowWebhook.ObjectMeta.Namespace, workflowWebhook.ObjectMeta.Name), "application/json", bytes.NewReader([]byte(`{"username":"john doe","password":"super.Secr3t"}`)))
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			By("By reading response payload with WorkflowWebhookRequest NamespacedName")
			pr := &simplecicdv1alpha1.NamespacedName{}
			err = json.NewDecoder(resp.Body).Decode(pr)
			Expect(err).NotTo(HaveOccurred())

			wwrnn := types.NamespacedName{
				Namespace: *pr.Namespace,
				Name:      pr.Name,
			}

			By("By waiting for a new WorkflowWebhookRequest")
			Eventually(func() bool {
				wwr := &simplecicdv1alpha1.WorkflowWebhookRequest{}
				err := k8sClient.Get(ctx, wwrnn, wwr)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			//TODO: Check Jobs

			// By("By checking the WorkflowWebhookRequest is Done")
			// Eventually(func() (bool, error) {
			// 	wwr := &simplecicdv1alpha1.WorkflowWebhookRequest{}
			// 	err := k8sClient.Get(ctx, wwrnn, wwr)
			// 	if err != nil {
			// 		return false, err
			// 	}
			// 	return wwr.Spec.Done, nil
			// }, nodeTimeout, interval).Should(BeTrue())

		}, NodeTimeout(nodeTimeout))
	})

	// Context("When updating CronJob Status", func() {
	// 	It("Should increase CronJob Status.Active count when new Jobs are created", func() {
	// 		By("By creating a new CronJob")
	// 		cronJob := &cronjobv1.CronJob{
	// 			TypeMeta: metav1.TypeMeta{
	// 				APIVersion: "batch.tutorial.kubebuilder.io/v1",
	// 				Kind:       "CronJob",
	// 			},
	// 			ObjectMeta: metav1.ObjectMeta{
	// 				Name:      CronjobName,
	// 				Namespace: CronjobNamespace,
	// 			},
	// 			Spec: cronjobv1.CronJobSpec{
	// 				Schedule: "1 * * * *",
	// 				JobTemplate: batchv1.JobTemplateSpec{
	// 					Spec: batchv1.JobSpec{
	// 						// For simplicity, we only fill out the required fields.
	// 						Template: v1.PodTemplateSpec{
	// 							Spec: v1.PodSpec{
	// 								// For simplicity, we only fill out the required fields.
	// 								Containers: []v1.Container{
	// 									{
	// 										Name:  "test-container",
	// 										Image: "test-image",
	// 									},
	// 								},
	// 								RestartPolicy: v1.RestartPolicyOnFailure,
	// 							},
	// 						},
	// 					},
	// 				},
	// 			},
	// 		}
	// 		Expect(k8sClient.Create(ctx, cronJob)).Should(Succeed())

	// 		cronjobLookupKey := types.NamespacedName{Name: CronjobName, Namespace: CronjobNamespace}
	// 		createdCronjob := &cronjobv1.CronJob{}

	// 		// We'll need to retry getting this newly created CronJob, given that creation may not immediately happen.
	// 		Eventually(func() bool {
	// 			err := k8sClient.Get(ctx, cronjobLookupKey, createdCronjob)
	// 			if err != nil {
	// 				return false
	// 			}
	// 			return true
	// 		}, timeout, interval).Should(BeTrue())
	// 		// Let's make sure our Schedule string value was properly converted/handled.
	// 		Expect(createdCronjob.Spec.Schedule).Should(Equal("1 * * * *"))

	// 		By("By checking the CronJob has zero active Jobs")
	// 		Consistently(func() (int, error) {
	// 			err := k8sClient.Get(ctx, cronjobLookupKey, createdCronjob)
	// 			if err != nil {
	// 				return -1, err
	// 			}
	// 			return len(createdCronjob.Status.Active), nil
	// 		}, duration, interval).Should(Equal(0))
	// 		By("By creating a new Job")
	// 		testJob := &batchv1.Job{
	// 			ObjectMeta: metav1.ObjectMeta{
	// 				Name:      JobName,
	// 				Namespace: CronjobNamespace,
	// 			},
	// 			Spec: batchv1.JobSpec{
	// 				Template: v1.PodTemplateSpec{
	// 					Spec: v1.PodSpec{
	// 						// For simplicity, we only fill out the required fields.
	// 						Containers: []v1.Container{
	// 							{
	// 								Name:  "test-container",
	// 								Image: "test-image",
	// 							},
	// 						},
	// 						RestartPolicy: v1.RestartPolicyOnFailure,
	// 					},
	// 				},
	// 			},
	// 			Status: batchv1.JobStatus{
	// 				Active: 2,
	// 			},
	// 		}

	// 		// Note that your CronJob’s GroupVersionKind is required to set up this owner reference.
	// 		kind := reflect.TypeOf(cronjobv1.CronJob{}).Name()
	// 		gvk := cronjobv1.GroupVersion.WithKind(kind)

	// 		controllerRef := metav1.NewControllerRef(createdCronjob, gvk)
	// 		testJob.SetOwnerReferences([]metav1.OwnerReference{*controllerRef})
	// 		Expect(k8sClient.Create(ctx, testJob)).Should(Succeed())

	// 		By("By checking that the CronJob has one active Job")
	// 		Eventually(func() ([]string, error) {
	// 			err := k8sClient.Get(ctx, cronjobLookupKey, createdCronjob)
	// 			if err != nil {
	// 				return nil, err
	// 			}

	// 			names := []string{}
	// 			for _, job := range createdCronjob.Status.Active {
	// 				names = append(names, job.Name)
	// 			}
	// 			return names, nil
	// 		}, timeout, interval).Should(ConsistOf(JobName), "should list our active job %s in the active jobs list in status", JobName)
	// 	})
	// })
})

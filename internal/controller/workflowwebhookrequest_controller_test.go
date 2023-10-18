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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	simplecicdv1alpha1 "github.com/jlsalvador/simple-cicd/api/v1alpha1"
)

const contentTypeJson = "application/json"

var _ = Describe("WorkflowWebhookRequest controller", func() {

	const (
		nodeTimeout = time.Second * 60
		timeout     = time.Second * 10
		interval    = time.Second * 1
	)

	namespace := "default"
	namePrefix := "test-"
	suspend := true
	replace := simplecicdv1alpha1.Replace
	forbid := simplecicdv1alpha1.Forbid
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
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
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
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
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
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
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
			JobsToBeCloned: []simplecicdv1alpha1.JobsToBeCloned{
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
			JobsToBeCloned: []simplecicdv1alpha1.JobsToBeCloned{
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
			JobsToBeCloned: []simplecicdv1alpha1.JobsToBeCloned{
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
			JobsToBeCloned: []simplecicdv1alpha1.JobsToBeCloned{
				{Name: jobSuccess.ObjectMeta.Name},
				{Name: jobFailure.ObjectMeta.Name},
			},
		},
	}
	workflowSuspended := &simplecicdv1alpha1.Workflow{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simplecicdv1alpha1.GroupVersion.Identifier(),
			Kind:       "Workflow",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "suspended",
			Namespace: namespace,
		},
		Spec: simplecicdv1alpha1.WorkflowSpec{
			Suspend: &suspend,
			JobsToBeCloned: []simplecicdv1alpha1.JobsToBeCloned{
				{Name: jobSuccess.ObjectMeta.Name},
				{Name: jobFailure.ObjectMeta.Name},
			},
		},
	}
	workflowReplace := &simplecicdv1alpha1.Workflow{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simplecicdv1alpha1.GroupVersion.Identifier(),
			Kind:       "Workflow",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "replace",
			Namespace: namespace,
		},
		Spec: simplecicdv1alpha1.WorkflowSpec{
			JobsToBeCloned: []simplecicdv1alpha1.JobsToBeCloned{
				{Name: jobSuccess.ObjectMeta.Name, ConcurrencyPolicy: &replace},
			},
		},
	}
	workflowForbid := &simplecicdv1alpha1.Workflow{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simplecicdv1alpha1.GroupVersion.Identifier(),
			Kind:       "Workflow",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "forbid",
			Namespace: namespace,
		},
		Spec: simplecicdv1alpha1.WorkflowSpec{
			JobsToBeCloned: []simplecicdv1alpha1.JobsToBeCloned{
				{Name: jobSuccess.ObjectMeta.Name, ConcurrencyPolicy: &forbid},
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
				{Name: workflowSuspended.ObjectMeta.Name},
				{Name: workflowReplace.ObjectMeta.Name},
				{Name: workflowForbid.ObjectMeta.Name},
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
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{
						Namespace: job.Namespace,
						Name:      job.Name,
					}, job)
				}, timeout, interval).Should(Succeed())
			}

			By("By creating all Workflows", func() {
				for _, workflow := range []*simplecicdv1alpha1.Workflow{
					workflowCatRequest,
					workflowAllSuccess,
					workflowAllFails,
					workflowAllSome,
					workflowSuspended,
					workflowReplace,
					workflowForbid,
				} {
					Expect(k8sClient.Create(ctx, workflow)).Should(Succeed())
					Eventually(func() error {
						return k8sClient.Get(ctx, types.NamespacedName{
							Namespace: workflow.Namespace,
							Name:      workflow.Name,
						}, workflow)
					}, timeout, interval).Should(Succeed())
				}
			})

			By("By creating the WorkflowWebhook", func() {
				Expect(k8sClient.Create(ctx, workflowWebhook)).Should(Succeed())
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{
						Namespace: workflowWebhook.Namespace,
						Name:      workflowWebhook.Name,
					}, workflowWebhook)
				}, timeout, interval).Should(Succeed())
			})

			pr := &simplecicdv1alpha1.NamespacedName{}
			By("By trigger the creation of a WorkflowWebhookRequest through listener and reading payload", func() {
				url := fmt.Sprintf(
					"http://%s/%s/%s",
					webhookListener.Addr(),
					workflowWebhook.ObjectMeta.Namespace,
					workflowWebhook.ObjectMeta.Name,
				)
				payload := []byte(`{"username":"john doe","password":"super.Secr3t"}`)

				resp, err := http.Post(url, contentTypeJson, bytes.NewReader(payload))
				Expect(err).Should(Succeed())
				defer resp.Body.Close()

				By("By reading response payload with WorkflowWebhookRequest NamespacedName")
				Expect(json.NewDecoder(resp.Body).Decode(pr)).Should(Succeed())
			})
			wwrnn := types.NamespacedName{
				Namespace: *pr.Namespace,
				Name:      pr.Name,
			}
			wwr := &simplecicdv1alpha1.WorkflowWebhookRequest{}

			By(fmt.Sprintf("By waiting for WorkflowWebhookRequest.Status.Done %s", wwrnn), func() {
				Eventually(func() bool {
					Eventually(func() error {
						return k8sClient.Get(ctx, wwrnn, wwr)
					}, timeout, interval).Should(Succeed())

					for _, jn := range wwr.Status.CurrentJobs {
						// Fetch Job
						job := &batchv1.Job{}
						Eventually(func() bool {
							err := k8sClient.Get(ctx, jn.AsType(wwr.Namespace), job)
							return err == nil && job != nil
						}, timeout, interval).Should(BeTrue())

						// Simulate Job Complete
						job.Status.Conditions = append(job.Status.Conditions, batchv1.JobCondition{
							Type:   batchv1.JobComplete,
							Status: corev1.ConditionTrue,
						})
						err := k8sClient.Status().Update(ctx, job)
						Expect(err).NotTo(HaveOccurred())
					}

					return wwr.Status.Done
				}, nodeTimeout, interval).Should(BeTrue())
			})

			By("Deleting the WorkflowWebhookRequest")
			Expect(k8sClient.Delete(ctx, wwr)).Should(Succeed())

		}, NodeTimeout(nodeTimeout))
	})
})

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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	simplecicdv1alpha1 "github.com/jlsalvador/simple-cicd/api/v1alpha1"
)

const contentTypeJson = "application/json"

var _ = Describe("WorkflowWebhookRequest controller", func() {
	type tdt struct {
		ww              *simplecicdv1alpha1.WorkflowWebhook
		steps           int
		nSuccessfulJobs int
		nFailedJobs     int
	}
	for _, tt := range []tdt{
		{workflowWebhookNormal, 2, 7, 3},
		{workflowWebhookSuspended, 1, 0, 0},
		{workflowWebhookForbid, 1, 1, 0},
		{workflowWebhookReplace, 1, 1, 0},
	} {
		// Doc: https://github.com/golang/go/wiki/LoopvarExperiment
		wwLoopvar := tt.ww
		stepsLoopvar := tt.steps
		nSuccessfulJobsLoopvar := tt.nSuccessfulJobs
		nFailedJobsLoopvar := tt.nFailedJobs

		Context("WorkflowWebhookRequest "+wwLoopvar.ObjectMeta.Name, func() {
			It("Should finish correctly", func(ctx SpecContext) {
				testWw(wwLoopvar, stepsLoopvar, nSuccessfulJobsLoopvar, nFailedJobsLoopvar)
			}, NodeTimeout(nodeTimeout))
		})
	}
})

func testWw(ww *simplecicdv1alpha1.WorkflowWebhook, steps int, nSuccessfulJobs int, nFailedJobs int) {
	var pr *simplecicdv1alpha1.NamespacedName
	By(fmt.Sprintf("By trigger the creation of %s/%s through listener and reading payload", ww.Namespace, ww.Name), func() {
		url := fmt.Sprintf(
			"http://%s/%s/%s",
			webhookListener.Addr(),
			ww.ObjectMeta.Namespace,
			ww.ObjectMeta.Name,
		)
		payload := []byte(`{"username":"john doe","password":"super.Secr3t"}`)

		resp, err := http.Post(url, contentTypeJson, bytes.NewReader(payload))
		Expect(err).Should(Succeed())
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusServiceUnavailable {
			Expect(*ww.Spec.Suspend).Should(BeTrue())
			return
		}

		pr = &simplecicdv1alpha1.NamespacedName{}
		Expect(json.NewDecoder(resp.Body).Decode(pr)).Should(Succeed())
	})

	if pr == nil {
		Expect(*ww.Spec.Suspend).Should(BeTrue())
		return
	}

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

			wwrLog.Info(fmt.Sprintf("WorkflowWebhookRequest %s", wwrnn), "CurrentJobs", wwr.Status.CurrentJobs)

			for _, jn := range wwr.Status.CurrentJobs {
				// Fetch Job
				job := &batchv1.Job{}
				Eventually(func() bool {
					err := k8sClient.Get(ctx, jn.AsType(wwr.Namespace), job)
					return err == nil && job != nil
				}, timeout, interval).Should(BeTrue())

				// Simulate Job completion
				condition := batchv1.JobCondition{
					Type:   batchv1.JobComplete,
					Status: corev1.ConditionTrue,
				}
				if job.ObjectMeta.Labels[simplecicdv1alpha1.LabelJobName] == jobFailure.Name {
					condition = batchv1.JobCondition{
						Type:   batchv1.JobFailed,
						Status: corev1.ConditionTrue,
					}
					// } else if job.ObjectMeta.Labels[LabelJobName] == jobWait.Name {
					// 	time.Sleep(time.Second * 5)
				}
				job.Status.Conditions = append(job.Status.Conditions, condition)
				err := k8sClient.Status().Update(ctx, job)
				Expect(err).NotTo(HaveOccurred())
			}

			return wwr.Status.Done
		}, nodeTimeout, interval).Should(BeTrue())
	})

	By("Checking total steps")
	Expect(wwr.Status.Steps).Should(Equal(steps))

	By("Checking total successful jobs")
	Expect(wwr.Status.SuccessfulJobs).Should(Equal(nSuccessfulJobs))

	By("Checking total failed jobs")
	Expect(wwr.Status.FailedJobs).Should(Equal(nFailedJobs))

	By(fmt.Sprintf("Deleting the WorkflowWebhookRequest %s", wwrnn))
	Expect(k8sClient.Delete(ctx, wwr)).Should(Succeed())
}

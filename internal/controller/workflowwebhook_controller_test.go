package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	simplecicdv1alpha1 "github.com/jlsalvador/simple-cicd/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("WorkflowWebhook controller", func() {
	workflowWebhookEmpty := &simplecicdv1alpha1.WorkflowWebhook{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simplecicdv1alpha1.GroupVersion.Identifier(),
			Kind:       "WorkflowWebhook",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "empty",
			Namespace: namespace,
		},
		Spec: simplecicdv1alpha1.WorkflowWebhookSpec{
			Workflows: []simplecicdv1alpha1.NamespacedName{},
		},
	}

	Context("WorkflowWebhook "+workflowWebhookEmpty.ObjectMeta.Name, func() {
		It("Webhook listener", func(ctx SpecContext) {
			By("Creating WorkflowWebhook", func() {
				Expect(k8sClient.Create(ctx, workflowWebhookEmpty)).Should(Succeed())
			})

			By("Deleting WorkflowWebhook", func() {
				Expect(k8sClient.Delete(ctx, workflowWebhookEmpty)).Should(Succeed())
			})
		}, NodeTimeout(nodeTimeout))
	})
})

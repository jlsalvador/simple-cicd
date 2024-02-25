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
	"path/filepath"
	"runtime"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	simplecicdv1alpha1 "github.com/jlsalvador/simple-cicd/api/v1alpha1"
	listener "github.com/jlsalvador/simple-cicd/internal/workflowWebhookListener"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	cfg             *rest.Config
	k8sClient       client.Client
	webhookListener *listener.Webhook
	testEnv         *envtest.Environment
	ctx             context.Context
	cancel          context.CancelCauseFunc
)

// Configures the test environment
const (
	nodeTimeout = time.Second * 60
	timeout     = time.Second * 10
	interval    = time.Second * 1

	namespace  = "default"
	namePrefix = "test-"
)

// Pointer-referred variables for the test assets
var (
	suspend           = true
	replace           = simplecicdv1alpha1.Replace
	forbid            = simplecicdv1alpha1.Forbid
	backoffLimit      = int32(0)
	onSuccess         = simplecicdv1alpha1.OnSuccess
	onFailure         = simplecicdv1alpha1.OnFailure
	onAnySuccess      = simplecicdv1alpha1.OnAnySuccess
	onAnyFailure      = simplecicdv1alpha1.OnAnyFailure
	ttlSecondsNear    = int64(1)
	ttlSecondsDistant = int64(1 * 60 * 60)
)

// Jobs
var (
	jobCatRequest = &batchv1.Job{
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
	jobSuccess = &batchv1.Job{
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
	jobFailure = &batchv1.Job{
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
	jobWait = &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.Identifier(),
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "wait",
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
							Name:    "wait",
							Image:   "bash",
							Command: []string{"sleep", "10"},
						},
					},
				},
			},
		},
	}
)

// Workflows
var (
	workflowCatRequest = &simplecicdv1alpha1.Workflow{
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
	workflowAllSuccess = &simplecicdv1alpha1.Workflow{
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
	workflowAllFails = &simplecicdv1alpha1.Workflow{
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
	workflowAllSome = &simplecicdv1alpha1.Workflow{
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
			Next: []simplecicdv1alpha1.NextWorkflow{
				{Name: workflowCatRequest.ObjectMeta.Name, When: &onAnySuccess},
				{Name: workflowCatRequest.ObjectMeta.Name, When: &onAnyFailure},
			},
		},
	}
	workflowWait = &simplecicdv1alpha1.Workflow{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simplecicdv1alpha1.GroupVersion.Identifier(),
			Kind:       "Workflow",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "wait",
			Namespace: namespace,
		},
		Spec: simplecicdv1alpha1.WorkflowSpec{
			JobsToBeCloned: []simplecicdv1alpha1.NamespacedName{
				{Name: jobWait.ObjectMeta.Name},
			},
		},
	}
	workflowSuspended = &simplecicdv1alpha1.Workflow{
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
			JobsToBeCloned: []simplecicdv1alpha1.NamespacedName{
				{Name: jobSuccess.ObjectMeta.Name},
				{Name: jobFailure.ObjectMeta.Name},
			},
		},
	}
)

// WorkflowWebhooks
var (
	workflowWebhookNormal = &simplecicdv1alpha1.WorkflowWebhook{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simplecicdv1alpha1.GroupVersion.Identifier(),
			Kind:       "WorkflowWebhook",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "normal",
			Namespace: namespace,
		},
		Spec: simplecicdv1alpha1.WorkflowWebhookSpec{
			Workflows: []simplecicdv1alpha1.NamespacedName{
				{Name: workflowAllSuccess.ObjectMeta.Name},
				{Name: workflowAllSome.ObjectMeta.Name},
				{Name: workflowAllFails.ObjectMeta.Name},
				{Name: workflowSuspended.ObjectMeta.Name},
			},
		},
	}
	workflowWebhookSuspended = &simplecicdv1alpha1.WorkflowWebhook{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simplecicdv1alpha1.GroupVersion.Identifier(),
			Kind:       "WorkflowWebhook",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "suspended",
			Namespace: namespace,
		},
		Spec: simplecicdv1alpha1.WorkflowWebhookSpec{
			Suspend: &suspend,
			Workflows: []simplecicdv1alpha1.NamespacedName{
				{Name: workflowAllSuccess.ObjectMeta.Name},
			},
		},
	}
	workflowWebhookReplace = &simplecicdv1alpha1.WorkflowWebhook{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simplecicdv1alpha1.GroupVersion.Identifier(),
			Kind:       "WorkflowWebhook",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "replace",
			Namespace: namespace,
		},
		Spec: simplecicdv1alpha1.WorkflowWebhookSpec{
			ConcurrencyPolicy: &replace,
			Workflows: []simplecicdv1alpha1.NamespacedName{
				{Name: workflowWait.ObjectMeta.Name},
			},
		},
	}
	workflowWebhookForbid = &simplecicdv1alpha1.WorkflowWebhook{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simplecicdv1alpha1.GroupVersion.Identifier(),
			Kind:       "WorkflowWebhook",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "forbid",
			Namespace: namespace,
		},
		Spec: simplecicdv1alpha1.WorkflowWebhookSpec{
			ConcurrencyPolicy: &forbid,
			Workflows: []simplecicdv1alpha1.NamespacedName{
				{Name: workflowWait.ObjectMeta.Name},
			},
		},
	}
	workflowWebhookTtlNear = &simplecicdv1alpha1.WorkflowWebhook{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simplecicdv1alpha1.GroupVersion.Identifier(),
			Kind:       "WorkflowWebhook",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "ttl-near",
			Namespace: namespace,
		},
		Spec: simplecicdv1alpha1.WorkflowWebhookSpec{
			TtlSecondsAfterFinished: &ttlSecondsNear,
			Workflows: []simplecicdv1alpha1.NamespacedName{
				{Name: workflowCatRequest.ObjectMeta.Name},
			},
		},
	}
	workflowWebhookTtlDistant = &simplecicdv1alpha1.WorkflowWebhook{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simplecicdv1alpha1.GroupVersion.Identifier(),
			Kind:       "WorkflowWebhook",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namePrefix + "ttl-distant",
			Namespace: namespace,
		},
		Spec: simplecicdv1alpha1.WorkflowWebhookSpec{
			TtlSecondsAfterFinished: &ttlSecondsDistant,
			Workflows: []simplecicdv1alpha1.NamespacedName{
				{Name: workflowCatRequest.ObjectMeta.Name},
			},
		},
	}
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancelCause(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.28.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = simplecicdv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	webhookListener, err = listener.New(&listener.Config{
		Addr:   "127.0.0.1:0", // Listen on any free localhost port
		Client: k8sManager.GetClient(),
	})
	Expect(err).ToNot(HaveOccurred(), "failed to run listener")

	err = (&WorkflowWebhookReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&WorkflowWebhookRequestReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
	go func() {
		defer GinkgoRecover()
		err = webhookListener.Start(ctx, cancel)
		Expect(err).ToNot(HaveOccurred(), "failed to run listener")
	}()

	for _, job := range []*batchv1.Job{
		jobCatRequest,
		jobSuccess,
		jobFailure,
		jobWait,
	} {
		Expect(k8sClient.Create(ctx, job)).Should(Succeed())
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{
				Namespace: job.Namespace,
				Name:      job.Name,
			}, job)
		}, timeout, interval).Should(Succeed())
	}

	for _, workflow := range []*simplecicdv1alpha1.Workflow{
		workflowCatRequest,
		workflowAllSuccess,
		workflowAllFails,
		workflowAllSome,
		workflowSuspended,
		workflowWait,
	} {
		Expect(k8sClient.Create(ctx, workflow)).Should(Succeed())
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{
				Namespace: workflow.Namespace,
				Name:      workflow.Name,
			}, workflow)
		}, timeout, interval).Should(Succeed())
	}

	for _, ww := range []*simplecicdv1alpha1.WorkflowWebhook{
		workflowWebhookNormal,
		workflowWebhookSuspended,
		workflowWebhookReplace,
		workflowWebhookForbid,
		workflowWebhookTtlNear,
		workflowWebhookTtlDistant,
	} {
		Expect(k8sClient.Create(ctx, ww)).Should(Succeed())
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{
				Namespace: workflowWebhookNormal.Namespace,
				Name:      workflowWebhookNormal.Name,
			}, workflowWebhookNormal)
		}, timeout, interval).Should(Succeed())
	}
})

var _ = AfterSuite(func() {
	cancel(errors.New(""))
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

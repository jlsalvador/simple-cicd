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
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	simplecicdv1alpha1 "github.com/jlsalvador/simple-cicd/api/v1alpha1"
	listener "github.com/jlsalvador/simple-cicd/internal/workflowWebhookListener"
)

var wwLog = ctrl.Log.WithName("workflowWebhook controller")

// WorkflowWebhookReconciler reconciles a WorkflowWebhook object
type WorkflowWebhookReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflowwebhooks,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=simple-cicd.jlsalvador.online,resources=workflowwebhooks/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.0/pkg/reconcile
func (r *WorkflowWebhookReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ww := &simplecicdv1alpha1.WorkflowWebhook{}
	if err := r.Get(ctx, req.NamespacedName, ww); err != nil {
		if apierrors.IsNotFound(err) {
			wwLog.Info(fmt.Sprintf(`unregistering WorkflowWebhook "%s/%s"`, req.Namespace, req.Name))
			listener.UnregisterWebhook(req.Namespace, req.Name)
			return ctrl.Result{}, nil
		}
		wwLog.Error(err, fmt.Sprintf(`can not fetch "%s/%s"`, req.Namespace, req.Name))
		return ctrl.Result{}, err
	}
	wwLog.Info(fmt.Sprintf(`registering WorkflowWebhook "%s/%s"`, req.Namespace, req.Name))
	listener.RegisterWebhook(r.Client, ctx, req.Namespace, req.Name)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkflowWebhookReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&simplecicdv1alpha1.WorkflowWebhook{}).
		Complete(r)
}

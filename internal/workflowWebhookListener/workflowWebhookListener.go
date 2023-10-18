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

package workflowWebhookListener

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	simplecicdv1alpha1 "github.com/jlsalvador/simple-cicd/api/v1alpha1"
	"github.com/jlsalvador/simple-cicd/internal/handler"
)

type Config struct {
	Addr   string
	Client client.Client
}

type Webhook struct {
	config   Config
	listener net.Listener
}

var log = ctrl.Log.WithName("workflowWebhook listener")
var h = handler.NewHandler()

// Starts WorkflowWebhookListener and blocks until the context is cancelled.
// Returns an error if there is an error.
func (wc *Webhook) Start(ctx context.Context, cancel context.CancelCauseFunc) error {

	// Create listener (with our custom Context) for HTTP server
	var lc = net.ListenConfig{}
	var err error
	wc.listener, err = lc.Listen(ctx, "tcp", wc.config.Addr)
	if err != nil {
		return err
	}

	// Start HTTP server
	h := &http.Server{
		Addr:    wc.config.Addr,
		Handler: h,
	}
	go func() {
		log.Info("listening for WorkflowWebhooks", "address", wc.config.Addr)
		if err := h.Serve(wc.listener); err != nil {
			cancel(err)
		}
	}()

	// Wait until context is Done
	<-ctx.Done()
	log.Info("shutting down listener for WorkflowWebhooks", "address", wc.config.Addr)
	return h.Shutdown(ctx)
}

func (wc *Webhook) Addr() string {
	return wc.listener.Addr().String()
}

func New(config *Config) (*Webhook, error) {
	if config == nil {
		return nil, errors.New("must specify Config")
	}

	if config.Addr == "" {
		config.Addr = ":9000"
	}
	if config.Client == nil {
		return nil, errors.New("must specify Config.Client")
	}

	return &Webhook{
		config: *config,
	}, nil
}

func RegisterWebhook(c client.Client, ctx context.Context, namespace string, name string) {
	pattern := fmt.Sprintf("/%s/%s", namespace, name)
	h.RegisterHandler(pattern, func(w http.ResponseWriter, r *http.Request) {
		webhookHandler(c, ctx, w, r)
	})
}

func UnregisterWebhook(namespace string, name string) {
	pattern := fmt.Sprintf("/%s/%s", namespace, name)
	h.UnregisterHandler(pattern)
}

// Will create a WorkflowWebhookRequest
func webhookHandler(c client.Client, ctx context.Context, w http.ResponseWriter, r *http.Request) {
	// Fetch WorkflowWebhook from http.Request.URL.Path
	ww, err := getWorkflowWebhookFromPath(c, ctx, r.URL.Path)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(err.Error()))
		return
	}

	// Skip suspended WorkflowWebhook
	if ww.Spec.Suspend != nil && *ww.Spec.Suspend {
		log.Info("skipping suspended WorkflowWebhook", "WorkflowWebhook", ww.Namespace+"/"+ww.Name)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(http.StatusText(http.StatusServiceUnavailable)))
		return
	}

	// Create a WorkflowWebhookRequest
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}
	defer r.Body.Close()

	namePrefix := fmt.Sprintf("%s-", ww.Name)
	wwr := &simplecicdv1alpha1.WorkflowWebhookRequest{
		TypeMeta: v1.TypeMeta{
			APIVersion: fmt.Sprintf("%s/%s", simplecicdv1alpha1.GroupVersion.Group, simplecicdv1alpha1.GroupVersion.Version),
			Kind:       "WorkflowWebhookRequest",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:         "",           // Will be auto-generated by WorkflowWebhookRequest.ObjectMeta.GenerateName
			Namespace:    ww.Namespace, // Same namespace as the WorkflowWebhook
			GenerateName: namePrefix,   // Will set WorkflowWebhookRequest.ObjectMeta.Name on Create
			Labels: map[string]string{
				// Proper annotations/labels as "controlledBy" or "createdBy"
				"app.kubernetes.io/instance": ww.Name,
				//TODO: Fill "app.kubernetes.io/version"
				"app.kubernetes.io/part-of":    "workflow-operator",
				"app.kubernetes.io/created-by": "controller-manager",
			},
		},
		Spec: simplecicdv1alpha1.WorkflowWebhookRequestSpec{
			WorkflowWebhook: simplecicdv1alpha1.NamespacedName{
				Namespace: &ww.Namespace,
				Name:      ww.Name,
			},
			Host:    r.Host,
			Method:  r.Method,
			Url:     r.URL.String(),
			Headers: r.Header,
			Body:    body,
		},
		Status: simplecicdv1alpha1.WorkflowWebhookRequestStatus{}, // Will be filled by the controller
	}
	if err := c.Create(ctx, wwr); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	// Write WorkflowWebhookRequest NamespacedName as HTTP response
	w.WriteHeader(http.StatusCreated)
	payload := simplecicdv1alpha1.NamespacedName{
		Namespace: &wwr.Namespace,
		Name:      wwr.Name,
	}
	json.NewEncoder(w).Encode(payload)
}

func getWorkflowWebhookFromPath(c client.Client, ctx context.Context, path string) (*simplecicdv1alpha1.WorkflowWebhook, error) {
	// Get namespace and name from url path
	name, namespace, err := getNameAndNamespaceFromPath(path)
	if err != nil {
		return nil, err
	}

	// Fetch WorkflowWebhook by namespace and name
	ww := &simplecicdv1alpha1.WorkflowWebhook{}
	if err = c.Get(
		ctx,
		types.NamespacedName{Namespace: namespace, Name: name},
		ww,
	); err != nil {
		return nil, errors.Join(err, fmt.Errorf(`WorkflowWebhook "%s/%s" not found`, namespace, name))
	}

	return ww, nil
}

func getNameAndNamespaceFromPath(path string) (name string, namespace string, err error) {
	re := regexp.MustCompile(`^/(?P<namespace>[\w-]+)/(?P<name>[\w-]+)`)

	m := re.FindStringSubmatch(path)
	if len(m) != len(re.SubexpNames()) {
		err = fmt.Errorf("can not get name and namespace from path %q and regexp %q", path, re)
		return
	}
	namespace = m[re.SubexpIndex("namespace")]
	name = m[re.SubexpIndex("name")]
	return
}

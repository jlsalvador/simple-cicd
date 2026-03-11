package webhook

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jlsalvador/simple-cicd/internal/k8s"
	"github.com/jlsalvador/simple-cicd/internal/types"
)

// Handler handles incoming webhook HTTP requests and creates
// WorkflowWebhookRequest resources in Kubernetes.
type Handler struct {
	client    k8s.ClientIface
	triggerFn func() // called after a WWR is created to wake the reconciler.
}

// NewHandler creates a Handler.
func NewHandler(client k8s.ClientIface, triggerFn func()) *Handler {
	return &Handler{client: client, triggerFn: triggerFn}
}

// ServeHTTP implements http.Handler.
//
// Expected path: /{namespace}/{webhookName}
//
// Example: POST /default/hello-world
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// /healthz is handled by the mux before reaching here, but guard anyway.
	if r.URL.Path == "/healthz" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	namespace, webhookName, err := parsePath(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	headersJSON, err := json.Marshal(r.Header)
	if err != nil {
		http.Error(w, "failed to marshal request headers", http.StatusInternalServerError)
		return
	}

	// Embed the HTTP request data directly in the WWR spec (base64-encoded).
	//
	// A Secret is created per cloned job at reconcile time, in the job's own
	// namespace, so cross-namespace job execution is fully supported.
	//
	// Timestamp is a Unix epoch (seconds UTC) to keep the format simple and
	// language-agnostic for job pods that consume it.
	requestData := types.WebhookRequestData{
		Body:       base64.StdEncoding.EncodeToString(body),
		Headers:    base64.StdEncoding.EncodeToString(headersJSON),
		Host:       base64.StdEncoding.EncodeToString([]byte(r.Host)),
		Method:     base64.StdEncoding.EncodeToString([]byte(r.Method)),
		URL:        base64.StdEncoding.EncodeToString([]byte(r.URL.String())),
		RemoteAddr: base64.StdEncoding.EncodeToString([]byte(r.RemoteAddr)),
		Timestamp:  base64.StdEncoding.EncodeToString(fmt.Append(nil, time.Now().Unix())),
		UserAgent:  base64.StdEncoding.EncodeToString([]byte(r.UserAgent())),
	}

	wwr := &types.WorkflowWebhookRequest{
		APIVersion: types.APIGroup + "/" + types.APIVersion,
		Kind:       "WorkflowWebhookRequest",
		Metadata: types.ObjectMeta{
			GenerateName: webhookName + "-",
			Namespace:    namespace,
			Labels: map[string]string{
				types.LabelWebhookName:      webhookName,
				types.LabelWebhookNamespace: namespace,
			},
		},
		Spec: types.WorkflowWebhookRequestSpec{
			WorkflowWebhook: types.ResourceName{Name: webhookName},
			Request:         requestData,
		},
	}

	created, err := h.client.CreateWWR(wwr)
	if err != nil {
		log.Printf("[webhook] error creating WWR for %s/%s: %v", namespace, webhookName, err)
		http.Error(w, "failed to create WorkflowWebhookRequest", http.StatusInternalServerError)
		return
	}
	log.Printf("[webhook] created WWR %s/%s for webhook %s/%s",
		namespace, created.Metadata.Name, namespace, webhookName)

	// Wake the reconciler immediately instead of waiting for the next tick.
	h.triggerFn()

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, "WorkflowWebhookRequest %q created\n", created.Metadata.Name)
}

// parsePath extracts namespace and webhookName from a URL path like
// /{namespace}/{webhookName}.
func parsePath(urlPath string) (namespace, webhookName string, err error) {
	cleaned := strings.TrimPrefix(urlPath, "/")
	parts := strings.SplitN(cleaned, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid path %q: expected /{namespace}/{webhookName}", urlPath)
	}
	return parts[0], parts[1], nil
}

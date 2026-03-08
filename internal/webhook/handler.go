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
	client    *k8s.Client
	triggerFn func() // called after a WWR is created to wake the reconciler
}

// NewHandler creates a Handler.
func NewHandler(client *k8s.Client, triggerFn func()) *Handler {
	return &Handler{client: client, triggerFn: triggerFn}
}

// ServeHTTP implements http.Handler.
// Expected path: /{namespace}/{webhookName}
// Example: POST /default/hello-world
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// /healthz is handled by the mux before reaching here, but guard anyway
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

	// --- Step 1: create the request Secret ---
	// The secret is created first so the WWR spec can reference it by name.
	// Using generateName lets the API server assign a unique, collision-free name.
	headersJSON, err := json.Marshal(r.Header)
	if err != nil {
		http.Error(w, "failed to marshal request headers", http.StatusInternalServerError)
		return
	}

	secretObj := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"generateName": webhookName + "-request-",
			"namespace":    namespace,
			"labels": map[string]any{
				types.LabelWebhookName:      webhookName,
				types.LabelWebhookNamespace: namespace,
			},
		},
		// All Secret.data values must be base64-encoded.
		"data": map[string]string{
			"body":       base64.StdEncoding.EncodeToString(body),
			"headers":    base64.StdEncoding.EncodeToString(headersJSON),
			"host":       base64.StdEncoding.EncodeToString([]byte(r.Host)),
			"method":     base64.StdEncoding.EncodeToString([]byte(r.Method)),
			"url":        base64.StdEncoding.EncodeToString([]byte(r.URL.String())),
			"remoteAddr": base64.StdEncoding.EncodeToString([]byte(r.RemoteAddr)),
			"timestamp":  base64.StdEncoding.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339))),
			"userAgent":  base64.StdEncoding.EncodeToString([]byte(r.UserAgent())),
		},
	}

	secretName, err := h.client.CreateSecret(namespace, secretObj)
	if err != nil {
		log.Printf("[webhook] error creating request secret for %s/%s: %v", namespace, webhookName, err)
		http.Error(w, "failed to create request secret", http.StatusInternalServerError)
		return
	}
	log.Printf("[webhook] created request secret %s/%s", namespace, secretName)

	// --- Step 2: create the WorkflowWebhookRequest referencing the secret ---
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
			RequestSecret:   types.ResourceName{Name: secretName},
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

	// Patch the secret with an ownerReference to the WWR so it is
	// garbage-collected automatically when the WWR is deleted.
	if created.Metadata.UID != "" {
		if err := h.client.SetSecretOwner(namespace, secretName, created); err != nil {
			// Non-fatal: the secret will be orphaned but the WWR will still work.
			log.Printf("[webhook] warning: could not set ownerReference on secret %s/%s: %v",
				namespace, secretName, err)
		}
	}

	// Wake the reconciler immediately instead of waiting for the next tick
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

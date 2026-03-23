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
//
// The handler follows this sequence:
//  1. Parse path and read request body.
//  2. Look up the WorkflowWebhook (returns 404 for unknown webhooks).
//  3. Create a request Secret containing the base64-encoded HTTP data.
//  4. Create the WWR referencing that Secret.
//  5. If step 4 fails, delete the orphaned Secret before returning the error.
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

	// Verify the WorkflowWebhook exists before doing anything else.
	// Returning 404 here avoids creating orphaned Secrets or WWRs for
	// unknown webhooks.
	webhook, err := h.client.GetWorkflowWebhook(namespace, webhookName)
	if err != nil {
		log.Printf("[webhook] WorkflowWebhook %s/%s not found: %v", namespace, webhookName, err)
		http.Error(w,
			fmt.Sprintf("WorkflowWebhook %q not found in namespace %q", webhookName, namespace),
			http.StatusNotFound)
		return
	}

	// Encode the HTTP request data. All fields are base64 so they can be
	// stored verbatim in the Secret's data map.
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
	}

	// Create the request Secret before the WWR. We cannot set the WWR as its
	// owner here (chicken-and-egg: the Secret must exist before the WWR, but
	// the WWR UID is only available after creation). The WWR cleanup finalizer
	// deletes this Secret explicitly instead.
	secretName, err := h.client.CreateRequestSecret(namespace, webhookName, requestData)
	if err != nil {
		log.Printf("[webhook] error creating request secret for %s/%s: %v", namespace, webhookName, err)
		http.Error(w, "failed to create request secret", http.StatusInternalServerError)
		return
	}
	log.Printf("[webhook] created request secret %s/%s", namespace, secretName)

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
			// Namespace is omitted: the Secret lives in the same namespace as
			// the WWR, so the reconciler falls back to wwr.Metadata.Namespace.
			RequestSecret:           types.ResourceName{Name: secretName},
			TTLSecondsAfterFinished: webhook.Spec.TTLSecondsAfterFinished,
			ActiveDeadlineSeconds:   webhook.Spec.ActiveDeadlineSeconds,
		},
	}

	created, err := h.client.CreateWWR(wwr)
	if err != nil {
		log.Printf("[webhook] error creating WWR for %s/%s: %v", namespace, webhookName, err)
		// Clean up the orphaned request Secret; if this fails we just log it.
		if delErr := h.client.DeleteSecret(namespace, secretName); delErr != nil {
			log.Printf("[webhook] warning: could not delete orphaned request secret %s/%s: %v",
				namespace, secretName, delErr)
		}
		http.Error(w, "failed to create WorkflowWebhookRequest", http.StatusInternalServerError)
		return
	}
	log.Printf("[webhook] created WWR %s/%s (secret %s, TTL %s, deadline %s) for webhook %s/%s",
		namespace, created.Metadata.Name,
		secretName,
		formatTTL(webhook.Spec.TTLSecondsAfterFinished),
		formatTTL(webhook.Spec.ActiveDeadlineSeconds),
		namespace, webhookName)

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

// formatTTL returns a human-readable TTL string for logging.
func formatTTL(ttl *int32) string {
	if ttl == nil {
		return "none"
	}
	return fmt.Sprintf("%ds", *ttl)
}

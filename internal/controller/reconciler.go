package controller

import (
	"context"
	"log"
	"time"

	"github.com/jlsalvador/simple-cicd/internal/k8s"
	"github.com/jlsalvador/simple-cicd/internal/types"
)

// Reconciler runs a loop that processes all active WorkflowWebhookRequests.
//
// Use [NewReconciler] to create a new Reconciler.
type Reconciler struct {
	client    k8s.ClientIface
	triggerCh chan struct{}
}

// NewReconciler creates a Reconciler.
func NewReconciler(client k8s.ClientIface) *Reconciler {
	return &Reconciler{
		client:    client,
		triggerCh: make(chan struct{}, 64),
	}
}

// Trigger signals the reconciler to run an immediate reconciliation cycle.
func (r *Reconciler) Trigger() {
	select {
	case r.triggerCh <- struct{}{}:
	default:
	}
}

// Run starts the reconciliation loop. It runs until ctx is cancelled.
// It reconciles on every ticker tick or when Trigger() is called.
func (r *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	log.Println("[reconciler] started")
	for {
		select {
		case <-ctx.Done():
			log.Println("[reconciler] stopped")
			return
		case <-ticker.C:
			r.reconcileAll()
		case <-r.triggerCh:
			// Drain any additional queued signals before reconciling.
			for len(r.triggerCh) > 0 {
				<-r.triggerCh
			}
			r.reconcileAll()
		}
	}
}

// reconcileAll reconciles all WorkflowWebhookRequests in the cluster.
func (r *Reconciler) reconcileAll() {
	wwrs, err := r.client.ListAllWWRs()
	if err != nil {
		log.Printf("[reconciler] error listing WorkflowWebhookRequests: %v", err)
		return
	}
	for i := range wwrs {
		wwr := &wwrs[i]
		if err := r.reconcileWWR(wwr); err != nil {
			log.Printf("[reconciler] error reconciling %s/%s: %v",
				wwr.Metadata.Namespace, wwr.Metadata.Name, err)
		}
	}
}

// reconcileWWR runs one reconciliation iteration for a single WWR.
//
// Lifecycle:
//  1. If the WWR is being deleted (DeletionTimestamp set), run the cleanup
//     finalizer and return.
//  2. If the finalizer is not yet present, add it and return (the next tick
//     will pick up the updated object).
//  3. If the WWR is done, check whether its TTL has expired and delete it if so.
//  4. Normal processing: initialize on first sight, otherwise check progress.
func (r *Reconciler) reconcileWWR(wwr *types.WorkflowWebhookRequest) error {
	// 1. Deletion path.
	if wwr.Metadata.DeletionTimestamp != nil {
		return r.handleDeletion(wwr)
	}

	// 2. Ensure our finalizer is registered.
	if !hasFinalizer(wwr, types.FinalizerCleanup) {
		return r.addFinalizer(wwr)
	}

	// 3. TTL expiry check for completed WWRs.
	if wwr.Status.Done {
		return r.checkTTL(wwr)
	}

	// 4. Normal processing.
	if wwr.Status.Steps == 0 {
		return r.initialize(wwr)
	}
	return r.checkProgress(wwr)
}

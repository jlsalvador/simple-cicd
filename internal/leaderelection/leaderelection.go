// Package leaderelection implements leader election backed by a
// Kubernetes coordination.k8s.io/v1 Lease.
//
// Only one replica is the leader at a time; the others stand by and
// retry acquisition at every RetryPeriod tick. When the leader loses
// its lease the callback context is cancelled, allowing the caller to
// stop work gracefully.
package leaderelection

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jlsalvador/simple-cicd/internal/k8s"
	"github.com/jlsalvador/simple-cicd/internal/types"
)

// Config holds all parameters for leader election.
type Config struct {
	// Client is used to read and write the Lease resource.
	Client k8s.ClientIface

	// Namespace is where the Lease object lives.
	// Default: NAMESPACE env var, then current namespace, then "default".
	Namespace string

	// LeaseName is the name of the Lease object (shared by all replicas).
	// Default: LEASE_NAME env var, then "simple-cicd-operator".
	LeaseName string

	// Identity uniquely identifies this replica (e.g. pod name).
	// Default: POD_NAME env var, then hostname, then "simple-cicd-operator".
	Identity string

	// LeaseDuration is how long a lease remains valid without renewal.
	// A candidate waits this long before taking an expired lease.
	// Default: 15 s.
	LeaseDuration time.Duration

	// RenewDeadline is how long the current leader tries to renew before
	// stepping down.
	// Default: 10 s.
	RenewDeadline time.Duration

	// RetryPeriod is the interval between acquisition / renewal attempts.
	// Default: 2 s.
	RetryPeriod time.Duration
}

func (c *Config) applyDefaults() {
	if c.LeaseDuration == 0 {
		c.LeaseDuration = 15 * time.Second
	}
	if c.RenewDeadline == 0 {
		c.RenewDeadline = 10 * time.Second
	}
	if c.RetryPeriod == 0 {
		c.RetryPeriod = 2 * time.Second
	}

	if c.Namespace == "" {
		c.Namespace = getDefaultNamespace()
	}
	if c.LeaseName == "" {
		c.LeaseName = getDefaultLeaseName()
	}
	if c.Identity == "" {
		c.Identity = getDefaultIdentity()
	}
}

// getDefaultNamespace returns the namespace where the Lease should be created.
//
// Reads the pod's own namespace from the service-account projection first,
// then falls back to the NAMESPACE env var, then "default".
func getDefaultNamespace() string {
	const nsFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	if data, err := os.ReadFile(nsFile); err == nil {
		if ns := strings.TrimSpace(string(data)); ns != "" {
			return ns
		}
	}
	if ns := os.Getenv("NAMESPACE"); ns != "" {
		return ns
	}
	return "default"
}

// getDefaultLeaseName returns the name of the Lease to be created.
// Reads the LEASE_NAME env var first, then "simple-cicd-operator".
func getDefaultLeaseName() string {
	if n := os.Getenv("LEASE_NAME"); n != "" {
		return n
	}
	return "simple-cicd-operator"
}

// getDefaultIdentity returns a unique name for this replica. It prefers
// the pod name (injected via the downward API as POD_NAME) and falls back to
// hostname.
func getDefaultIdentity() string {
	if name := os.Getenv("POD_NAME"); name != "" {
		return name
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "simple-cicd-operator"
}

// Run starts the leader-election loop and blocks until ctx is cancelled.
//
// When this replica acquires the lease, onStartLeading is called in a new
// goroutine with a child context. That context is cancelled if the lease is
// lost, signalling the callback to stop work. Acquisition is re-attempted
// every RetryPeriod.
func Run(ctx context.Context, cfg Config, onStartLeading func(context.Context)) {
	cfg.applyDefaults()
	e := &elector{cfg: cfg}

	ticker := time.NewTicker(cfg.RetryPeriod)
	defer ticker.Stop()

	var (
		leading     bool
		stopLeading context.CancelFunc
		lastRenew   time.Time
	)

	for {
		select {
		case <-ctx.Done():
			if stopLeading != nil {
				stopLeading()
			}
			log.Printf("[leader-election] context cancelled, stepping down")
			return

		case <-ticker.C:
			ok := e.tryAcquireOrRenew()

			switch {
			case ok && !leading:
				// Newly elected.
				leading = true
				lastRenew = time.Now()
				var leadCtx context.Context
				leadCtx, stopLeading = context.WithCancel(ctx)
				log.Printf("[leader-election] %s: acquired lease, leading", cfg.Identity)
				go onStartLeading(leadCtx)

			case ok && leading:
				// Successful renewal.
				lastRenew = time.Now()

			case !ok && leading:
				// Check if we've exceeded the renew deadline.
				if time.Since(lastRenew) >= cfg.RenewDeadline {
					leading = false
					stopLeading()
					stopLeading = nil
					log.Printf("[leader-election] %s: failed to renew within deadline, stepping down", cfg.Identity)
				}
				// If we're still within the deadline, stay leading and retry next tick.

			case !ok && !leading:
				// Normal standby; someone else is leading.
			}
		}
	}
}

// elector holds the mutable state for one election participant.
type elector struct {
	cfg Config
}

// tryAcquireOrRenew attempts to create or update the Lease so that this
// replica is recorded as the holder. Returns true on success.
func (e *elector) tryAcquireOrRenew() bool {
	now := time.Now()

	existing, err := e.cfg.Client.GetLease(e.cfg.Namespace, e.cfg.LeaseName)
	if err != nil {
		if k8s.IsNotFound(err) {
			return e.createLease(now)
		}
		log.Printf("[leader-election] error fetching lease %s/%s: %v",
			e.cfg.Namespace, e.cfg.LeaseName, err)
		return false
	}

	return e.updateLease(existing, now)
}

// createLease POSTs a new Lease claiming leadership for this replica.
func (e *elector) createLease(now time.Time) bool {
	dur := int32(e.cfg.LeaseDuration.Seconds())
	transitions := int32(0)
	lease := &types.Lease{
		APIVersion: "coordination.k8s.io/v1",
		Kind:       "Lease",
		Metadata: types.ObjectMeta{
			Name:      e.cfg.LeaseName,
			Namespace: e.cfg.Namespace,
		},
		Spec: types.LeaseSpec{
			HolderIdentity:       &e.cfg.Identity,
			LeaseDurationSeconds: &dur,
			AcquireTime:          types.NewMicroTime(now),
			RenewTime:            types.NewMicroTime(now),
			LeaseTransitions:     &transitions,
		},
	}

	if _, err := e.cfg.Client.CreateLease(e.cfg.Namespace, lease); err != nil {
		if k8s.IsConflict(err) {
			// Another replica created it concurrently; will retry next tick.
			return false
		}
		log.Printf("[leader-election] error creating lease: %v", err)
		return false
	}
	return true
}

// updateLease tries to take over or renew an existing Lease.
// Uses optimistic concurrency via resourceVersion; a 409 means a concurrent
// update won and we simply return false to retry next tick.
func (e *elector) updateLease(existing *types.Lease, now time.Time) bool {
	holder := ""
	if existing.Spec.HolderIdentity != nil {
		holder = *existing.Spec.HolderIdentity
	}
	weAreHolder := holder == e.cfg.Identity

	if !weAreHolder && !e.isExpired(existing, now) {
		// Another replica holds a valid lease; stay in standby.
		return false
	}
	if !weAreHolder {
		log.Printf("[leader-election] lease held by %q has expired, attempting takeover", holder)
	}

	// Preserve acquireTime when renewing; reset it on takeover.
	acquireTime := now
	if weAreHolder && existing.Spec.AcquireTime != nil {
		acquireTime = existing.Spec.AcquireTime.ToTime()
	}

	transitions := int32(0)
	if existing.Spec.LeaseTransitions != nil {
		transitions = *existing.Spec.LeaseTransitions
	}
	if !weAreHolder {
		transitions++
	}

	dur := int32(e.cfg.LeaseDuration.Seconds())
	updated := *existing // copy preserves Metadata.ResourceVersion for optimistic lock
	updated.Spec = types.LeaseSpec{
		HolderIdentity:       &e.cfg.Identity,
		LeaseDurationSeconds: &dur,
		AcquireTime:          types.NewMicroTime(acquireTime),
		RenewTime:            types.NewMicroTime(now),
		LeaseTransitions:     &transitions,
	}

	if _, err := e.cfg.Client.UpdateLease(&updated); err != nil {
		if k8s.IsConflict(err) {
			return false
		}
		log.Printf("[leader-election] error updating lease: %v", err)
		return false
	}
	return true
}

// isExpired returns true when the lease's renewTime plus leaseDuration is in
// the past, meaning the current holder has not renewed in time.
func (e *elector) isExpired(lease *types.Lease, now time.Time) bool {
	if lease.Spec.RenewTime == nil || lease.Spec.LeaseDurationSeconds == nil {
		return true // treat incomplete leases as expired
	}
	expiry := lease.Spec.RenewTime.ToTime().
		Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
	return now.After(expiry)
}

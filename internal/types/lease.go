package types

// --------------------------------------------------------------------------
// Lease (coordination.k8s.io/v1)
// --------------------------------------------------------------------------

type Lease struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       LeaseSpec  `json:"spec"`
}

type LeaseSpec struct {
	HolderIdentity       *string    `json:"holderIdentity,omitempty"`
	LeaseDurationSeconds *int32     `json:"leaseDurationSeconds,omitempty"`
	AcquireTime          *MicroTime `json:"acquireTime,omitempty"`
	RenewTime            *MicroTime `json:"renewTime,omitempty"`
	LeaseTransitions     *int32     `json:"leaseTransitions,omitempty"`
}

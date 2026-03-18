package k8s

import (
	"fmt"

	"github.com/jlsalvador/simple-cicd/internal/types"
)

// --------------------------------------------------------------------------
// Leases (coordination.k8s.io/v1) — used for leader election
// --------------------------------------------------------------------------

const leaseAPIPath = "/apis/coordination.k8s.io/v1"

func (c *Client) GetLease(namespace, name string) (*types.Lease, error) {
	path := fmt.Sprintf("%s/namespaces/%s/leases/%s", leaseAPIPath, namespace, name)
	var obj types.Lease
	return &obj, c.get(path, &obj)
}

func (c *Client) CreateLease(namespace string, lease *types.Lease) (*types.Lease, error) {
	path := fmt.Sprintf("%s/namespaces/%s/leases", leaseAPIPath, namespace)
	var created types.Lease
	return &created, c.post(path, lease, &created)
}

// UpdateLease replaces the Lease using PUT. The caller must set
// lease.Metadata.ResourceVersion (read from GetLease) so that the API server
// can detect concurrent updates and return 409 Conflict.
func (c *Client) UpdateLease(lease *types.Lease) (*types.Lease, error) {
	path := fmt.Sprintf("%s/namespaces/%s/leases/%s",
		leaseAPIPath, lease.Metadata.Namespace, lease.Metadata.Name)
	var updated types.Lease
	return &updated, c.put(path, lease, &updated)
}

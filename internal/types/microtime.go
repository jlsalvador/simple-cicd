package types

import (
	"encoding/json"
	"fmt"
	"time"
)

// MicroTime wraps time.Time with Kubernetes MicroTime JSON serialization
// (RFC3339 with microsecond precision). Used in coordination.k8s.io/v1 Lease.
type MicroTime time.Time

func (m MicroTime) MarshalJSON() ([]byte, error) {
	t := time.Time(m)
	if t.IsZero() {
		return []byte("null"), nil
	}
	return fmt.Appendf(nil, `"%s"`, t.UTC().Format("2006-01-02T15:04:05.000000Z")), nil
}

func (m *MicroTime) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.000000Z",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			*m = MicroTime(t)
			return nil
		}
	}
	return fmt.Errorf("cannot parse MicroTime %q", s)
}

func (m MicroTime) ToTime() time.Time { return time.Time(m) }

func NewMicroTime(t time.Time) *MicroTime { return new(MicroTime(t)) }

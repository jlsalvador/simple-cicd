package common

func DefaultString(v *string, def string) string {
	if v != nil {
		return *v
	}
	return def
}

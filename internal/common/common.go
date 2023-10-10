package common

import "fmt"

func JoinAsString[T fmt.Stringer](s []T, sep string) string {
	r := ""
	for _, j := range s {
		if r != "" {
			r += sep
		}
		r += j.String()
	}
	return r
}

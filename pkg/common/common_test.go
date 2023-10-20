package common

import "testing"

func TestDefaultString(t *testing.T) {
	aValue := "value"
	byDefault := "default"
	var emptyString string

	type args struct {
		v   *string
		def string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "ok but not necessary",
			args: args{&aValue, byDefault},
			want: aValue,
		},
		{
			name: "ok fallback to default",
			args: args{nil, byDefault},
			want: byDefault,
		},
		{
			name: "must returns an empty string",
			args: args{nil, emptyString},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DefaultString(tt.args.v, tt.args.def); got != tt.want {
				t.Errorf("DefaultString() = %v, want %v", got, tt.want)
			}
		})
	}
}

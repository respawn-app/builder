package sessionenv

import "testing"

func TestLookupBuilderSessionID(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
		ok   bool
	}{
		{name: "missing"},
		{name: "blank", env: map[string]string{BuilderSessionID: " \t\n"}},
		{name: "trims", env: map[string]string{BuilderSessionID: " session-1 \n"}, want: "session-1", ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := LookupBuilderSessionID(func(key string) (string, bool) {
				value, exists := tt.env[key]
				return value, exists
			})
			if got != tt.want || ok != tt.ok {
				t.Fatalf("LookupBuilderSessionID = (%q, %t), want (%q, %t)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestLookupBuilderSessionIDNilLookup(t *testing.T) {
	if got, ok := LookupBuilderSessionID(nil); got != "" || ok {
		t.Fatalf("LookupBuilderSessionID(nil) = (%q, %t), want empty false", got, ok)
	}
}

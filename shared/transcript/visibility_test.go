package transcript

import "testing"

func TestNormalizeEntryVisibility(t *testing.T) {
	tests := []struct {
		name       string
		visibility EntryVisibility
		want       EntryVisibility
	}{
		{name: "blank defaults to auto", visibility: "", want: EntryVisibilityAuto},
		{name: "auto normalizes to auto", visibility: "auto", want: EntryVisibilityAuto},
		{name: "auto is case-insensitive", visibility: " AUTO ", want: EntryVisibilityAuto},
		{name: "all preserved", visibility: "all", want: EntryVisibilityAll},
		{name: "all is case-insensitive", visibility: "ALL", want: EntryVisibilityAll},
		{name: "detail only preserved", visibility: "detail_only", want: EntryVisibilityDetailOnly},
		{name: "detail only is case-insensitive", visibility: " Detail_Only ", want: EntryVisibilityDetailOnly},
		{name: "unknown trimmed", visibility: "  custom  ", want: EntryVisibility("custom")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeEntryVisibility(tt.visibility); got != tt.want {
				t.Fatalf("NormalizeEntryVisibility(%q) = %q, want %q", tt.visibility, got, tt.want)
			}
		})
	}
}

package render

import "testing"

func TestPrefixWithChapter(t *testing.T) {
	tests := []struct {
		name string
		vars fieldVars
		page string
		want string
	}{
		{
			name: "no_chap_style",
			vars: fieldVars{},
			page: "5",
			want: "5",
		},
		{
			name: "hyphen_default",
			vars: fieldVars{chapStyle: 1, chapNumber: "3"},
			page: "5",
			want: "3-5",
		},
		{
			name: "period_sep",
			vars: fieldVars{chapStyle: 1, chapNumber: "2", chapSep: "period"},
			page: "9",
			want: "2.9",
		},
		{
			name: "colon_sep",
			vars: fieldVars{chapStyle: 1, chapNumber: "1", chapSep: "colon"},
			page: "1",
			want: "1:1",
		},
		{
			name: "emdash",
			vars: fieldVars{chapStyle: 1, chapNumber: "4", chapSep: "emDash"},
			page: "12",
			want: "4—12",
		},
		{
			name: "endash",
			vars: fieldVars{chapStyle: 2, chapNumber: "5", chapSep: "enDash"},
			page: "12",
			want: "5–12",
		},
		{
			name: "no_chap_resolved",
			vars: fieldVars{chapStyle: 1, chapNumber: ""},
			page: "7",
			want: "7",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := prefixWithChapter(tt.page, tt.vars); got != tt.want {
				t.Errorf("prefixWithChapter(%q, %+v) = %q, want %q",
					tt.page, tt.vars, got, tt.want)
			}
		})
	}
}

func TestLeadingHeadingNumber(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"3 Background", "3"},
		{"12. Implementation", "12"},
		{"Chapter 7 — Notes", "7"},
		{"Part 4: Overview", "4"},
		{"Section 2 Discussion", "2"},
		{"Introduction", ""},
		{"  5 Title with leading space", "5"},
	}
	for _, tt := range tests {
		if got := leadingHeadingNumber(tt.in); got != tt.want {
			t.Errorf("leadingHeadingNumber(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

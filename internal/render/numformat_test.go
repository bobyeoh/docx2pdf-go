package render

import "testing"

func TestFormatNumber_NewFormats(t *testing.T) {
	cases := []struct {
		n    int
		f    string
		want string
	}{
		{1, "decimalFullWidth", "１"},
		{42, "decimalFullWidth", "４２"},
		{3, "chineseLegalSimplified", "叁"},
		{15, "chineseLegalSimplified", "拾伍"},
		{25, "chineseLegalSimplified", "贰拾伍"},
		{7, "decimalEnclosedFullstop", "7."},
		{2, "numberInDash", "- 2 -"},
		{3, "thaiNumbers", "๓"},
		{27, "thaiNumbers", "๒๗"},
		{4, "hindiNumbers", "४"},
		{5, "koreanCounting", "오"},
		{15, "koreanCounting", "십오"},
		{1, "ganada", "가"},
		{4, "chosung", "ㄹ"},
		{1, "arabicAlpha", "ا"},
		{3, "arabicAlpha", "ت"},
		{4, "arabicAbjad", "د"},
		{1, "hindiVowels", "अ"},
		{1, "hindiConsonants", "क"},
		{10, "decimalEnclosedCircle", "⑩"},
		{1, "ideographZodiac", "鼠"},
		{12, "ideographZodiac", "豬"},
		{1, "thaiLetters", "ก"},
		{4, "thaiLetters", "ค"},
	}
	for _, c := range cases {
		got := formatNumber(c.n, c.f)
		if got != c.want {
			t.Errorf("formatNumber(%d,%q) = %q, want %q", c.n, c.f, got, c.want)
		}
	}
}

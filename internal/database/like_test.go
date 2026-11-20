package database_test

import (
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/database"
)

func TestEscapeLikeNeutralisesWildcards(t *testing.T) {
	cases := map[string]string{
		"粤B88888": `粤B88888`,
		"%":       `\%`,
		"_":       `\_`,
		`a\%b`:    `a\\\%b`,
	}
	for in, want := range cases {
		if got := database.EscapeLike(in); got != want {
			t.Errorf("EscapeLike(%q) = %q, want %q", in, got, want)
		}
	}
}

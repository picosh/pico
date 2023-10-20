package pgs

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

type Fixture struct {
	name   string
	input  string
	expect []*RedirectRule
}

func TestParseRedirectText(t *testing.T) {
	empty := map[string]string{}
	spa := Fixture{
		name:  "spa",
		input: "/*   /index.html   200",
		expect: []*RedirectRule{
			{
				From:       "/*",
				To:         "/index.html",
				Status:     200,
				Query:      empty,
				Conditions: empty,
			},
		},
	}

	fixtures := []Fixture{
		spa,
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			results, err := parseRedirectText(fixture.input)
			if err != nil {
				t.Error(err)
			}
			if cmp.Equal(results, fixture.expect) == false {
				t.Fatalf(cmp.Diff(fixture.expect, results))
			}
		})
	}
}

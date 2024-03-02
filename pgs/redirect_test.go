package pgs

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

type RedirectFixture struct {
	name   string
	input  string
	expect []*RedirectRule
}

func TestParseRedirectText(t *testing.T) {
	empty := map[string]string{}
	spa := RedirectFixture{
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

	withStatus := RedirectFixture{
		name:  "with-status",
		input: "/wow     /index.html     301",
		expect: []*RedirectRule{
			{
				From:       "/wow",
				To:         "/index.html",
				Status:     301,
				Query:      empty,
				Conditions: empty,
			},
		},
	}

	noStatus := RedirectFixture{
		name:  "no-status",
		input: "/wow     /index.html",
		expect: []*RedirectRule{
			{
				From:       "/wow",
				To:         "/index.html",
				Status:     200,
				Query:      empty,
				Conditions: empty,
			},
		},
	}

	fixtures := []RedirectFixture{
		spa,
		withStatus,
		noStatus,
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

package pgs

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

type RedirectFixture struct {
	name        string
	input       string
	expect      []*RedirectRule
	shouldError bool
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

	rss := RedirectFixture{
		name:  "rss",
		input: "/rss /rss.atom 200",
		expect: []*RedirectRule{
			{
				From:       "/rss",
				To:         "/rss.atom",
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
				Status:     301,
				Query:      empty,
				Conditions: empty,
			},
		},
	}

	absoluteUriNoProto := RedirectFixture{
		name:        "absolute-uri-no-proto",
		input:       "/*  www.example.com  301",
		expect:      []*RedirectRule{},
		shouldError: true,
	}

	absoluteUriWithProto := RedirectFixture{
		name:  "absolute-uri-no-proto",
		input: "/*  https://www.example.com  301",
		expect: []*RedirectRule{
			{
				From:       "/*",
				To:         "https://www.example.com",
				Status:     301,
				Query:      empty,
				Conditions: empty,
			},
		},
	}

	selfReferentialExact := RedirectFixture{
		name:        "self-referential-exact",
		input:       "/page /page 301",
		expect:      []*RedirectRule{},
		shouldError: true,
	}

	selfReferentialWildcard := RedirectFixture{
		name:        "self-referential-wildcard",
		input:       "/* /* 301",
		expect:      []*RedirectRule{},
		shouldError: true,
	}

	selfReferentialWithVariables := RedirectFixture{
		name:        "self-referential-with-variables",
		input:       "/:path /:path 301",
		expect:      []*RedirectRule{},
		shouldError: true,
	}

	externalUrlNotSelfRef := RedirectFixture{
		name:  "external-url-not-self-referential",
		input: "/* https://example.com 301",
		expect: []*RedirectRule{
			{
				From:       "/*",
				To:         "https://example.com",
				Status:     301,
				Query:      empty,
				Conditions: empty,
			},
		},
	}

	validPathRedirect := RedirectFixture{
		name:  "valid-path-redirect",
		input: "/old-path /new-path 301",
		expect: []*RedirectRule{
			{
				From:       "/old-path",
				To:         "/new-path",
				Status:     301,
				Query:      empty,
				Conditions: empty,
			},
		},
	}

	fixtures := []RedirectFixture{
		spa,
		rss,
		withStatus,
		noStatus,
		absoluteUriNoProto,
		absoluteUriWithProto,
		selfReferentialExact,
		selfReferentialWildcard,
		selfReferentialWithVariables,
		externalUrlNotSelfRef,
		validPathRedirect,
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			results, err := parseRedirectText(fixture.input)
			if err != nil && !fixture.shouldError {
				t.Error(err)
			}
			if cmp.Equal(results, fixture.expect) == false {
				//nolint
				t.Fatal(cmp.Diff(fixture.expect, results))
			}
		})
	}
}

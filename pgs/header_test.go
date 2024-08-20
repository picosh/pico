package pgs

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type HeaderFixture struct {
	name   string
	input  string
	expect []*HeaderRule
}

func TestParseHeaderText(t *testing.T) {
	success := HeaderFixture{
		name:  "success",
		input: "/path\n\ttest: one",
		expect: []*HeaderRule{
			{
				Path: "/path",
				Headers: []*HeaderLine{
					{Name: "test", Value: "one"},
				},
			},
		},
	}

	successIndex := HeaderFixture{
		name:  "successIndex",
		input: "/index.html\n\tX-Frame-Options: DENY",
		expect: []*HeaderRule{
			{
				Path: "/index.html",
				Headers: []*HeaderLine{
					{Name: "x-frame-options", Value: "DENY"},
				},
			},
		},
	}

	compileList := ""
	for _, deny := range headerDenyList {
		compileList += fmt.Sprintf("\n\t%s: value", deny)
	}

	denyList := HeaderFixture{
		name:  "denyList",
		input: fmt.Sprintf("/\n\tX-Frame-Options: DENY%s", compileList),
		expect: []*HeaderRule{
			{
				Path: "/",
				Headers: []*HeaderLine{
					{Name: "x-frame-options", Value: "DENY"},
				},
			},
		},
	}

	multiValue := HeaderFixture{
		name:  "multiValue",
		input: "/*\n\tcache-control: max-age=0\n\tcache-control: no-cache\n\tcache-control: no-store\n\tcache-control: must-revalidate",
		expect: []*HeaderRule{
			{
				Path: "/*",
				Headers: []*HeaderLine{
					{Name: "cache-control", Value: "max-age=0"},
					{Name: "cache-control", Value: "no-cache"},
					{Name: "cache-control", Value: "no-store"},
					{Name: "cache-control", Value: "must-revalidate"},
				},
			},
		},
	}

	comment := HeaderFixture{
		name:  "comment",
		input: "/path\n\t# comment\n\ttest: one",
		expect: []*HeaderRule{
			{
				Path: "/path",
				Headers: []*HeaderLine{
					{Name: "test", Value: "one"},
				},
			},
		},
	}

	invalidName := HeaderFixture{
		name:   "invalidName",
		input:  "/path\n\t: value",
		expect: []*HeaderRule{},
	}

	invalidValue := HeaderFixture{
		name:   "invalidValue",
		input:  "/path\n\ttest:",
		expect: []*HeaderRule{},
	}

	invalidForOrder := HeaderFixture{
		name:   "invalidForOrder",
		input:  "\ttest: one\n/path",
		expect: []*HeaderRule{},
	}

	empty := HeaderFixture{
		name:   "empty",
		input:  "",
		expect: []*HeaderRule{},
	}

	emptyLine := HeaderFixture{
		name:  "emptyLine",
		input: "/path\n\n\ttest: one",
		expect: []*HeaderRule{
			{
				Path: "/path",
				Headers: []*HeaderLine{
					{Name: "test", Value: "one"},
				},
			},
		},
	}

	duplicate := HeaderFixture{
		name:  "duplicate",
		input: "/path\n\ttest: one\n/path\n\ttest: two",
		expect: []*HeaderRule{
			{
				Path: "/path",
				Headers: []*HeaderLine{
					{Name: "test", Value: "one"},
				},
			},
			{
				Path: "/path",
				Headers: []*HeaderLine{
					{Name: "test", Value: "two"},
				},
			},
		},
	}

	noColon := HeaderFixture{
		name:   "noColon",
		input:  "/path\n\ttest = one",
		expect: []*HeaderRule{},
	}

	fixtures := []HeaderFixture{
		success,
		successIndex,
		denyList,
		multiValue,
		comment,
		invalidName,
		invalidValue,
		invalidForOrder,
		empty,
		emptyLine,
		duplicate,
		noColon,
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			results, err := parseHeaderText(fixture.input)
			if err != nil {
				t.Error(err)
			}
			fmt.Println(results)
			if cmp.Equal(results, fixture.expect) == false {
				//nolint
				t.Fatalf(cmp.Diff(fixture.expect, results))
			}
		})
	}
}

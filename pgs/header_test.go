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

	fixtures := []HeaderFixture{
		success,
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			results, err := parseHeaderText(fixture.input)
			if err != nil {
				t.Error(err)
			}
			fmt.Println(results)
			if cmp.Equal(results, fixture.expect) == false {
				t.Fatalf(cmp.Diff(fixture.expect, results))
			}
		})
	}
}

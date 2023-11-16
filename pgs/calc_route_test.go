package pgs

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

type RouteFixture struct {
	Name     string
	Actual   []*HttpReply
	Expected []*HttpReply
}

func TestCalcPossibleRoutes(t *testing.T) {
	fixtures := []RouteFixture{
		{
			Name:   "basic-index",
			Actual: calcPossibleRoutes("test", "index.html", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/index.html", Status: 200},
				{Filepath: "test/index.html/index.html", Status: 200},
			},
		},
		{
			Name:   "basic-named",
			Actual: calcPossibleRoutes("test", "wow.html", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/wow.html", Status: 200},
				{Filepath: "test/wow.html/index.html", Status: 200},
			},
		},
		{
			Name:   "subdirectory-index",
			Actual: calcPossibleRoutes("test", "nice/index.html", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/nice/index.html", Status: 200},
				{Filepath: "test/nice/index.html/index.html", Status: 200},
			},
		},
		{
			Name:   "subdirectory-named",
			Actual: calcPossibleRoutes("test", "nice/wow.html", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/nice/wow.html", Status: 200},
				{Filepath: "test/nice/wow.html/index.html", Status: 200},
			},
		},
		{
			Name:   "subdirectory-bare",
			Actual: calcPossibleRoutes("test", "nice", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/nice/index.html", Status: 200},
				{Filepath: "test/nice.html", Status: 200},
			},
		},
		{
			Name: "spa",
			Actual: calcPossibleRoutes("test", "nice", []*RedirectRule{
				{
					From:   "/*",
					To:     "/index.html",
					Status: 200,
				},
			}),
			Expected: []*HttpReply{
				{Filepath: "test/nice/index.html", Status: 200},
				{Filepath: "test/nice.html", Status: 200},
				{Filepath: "test/index.html", Status: 200},
			},
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			if cmp.Equal(fixture.Actual, fixture.Expected) == false {
				t.Fatalf(cmp.Diff(fixture.Expected, fixture.Actual))
			}
		})
	}
}

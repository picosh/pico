package pgs

import (
	"fmt"
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
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "basic-txt",
			Actual: calcPossibleRoutes("test", "index.txt", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/index.txt", Status: 200},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "basic-named",
			Actual: calcPossibleRoutes("test", "wow.html", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/wow.html", Status: 200},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "subdirectory-index",
			Actual: calcPossibleRoutes("test", "nice/index.html", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/nice/index.html", Status: 200},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "subdirectory-named",
			Actual: calcPossibleRoutes("test", "nice/wow.html", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/nice/wow.html", Status: 200},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "subdirectory-bare",
			Actual: calcPossibleRoutes("test", "nice", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/nice.html", Status: 200},
				{Filepath: "test/nice/index.html", Status: 200},
				{Filepath: "test/404.html", Status: 404},
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
				{Filepath: "test/index.html", Status: 200},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name: "xml",
			Actual: calcPossibleRoutes("test", "index.xml", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/index.xml", Status: 200},
				{Filepath: "test/404.html", Status: 404},
			},
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			fmt.Println(fixture.Actual[0].Filepath)
			if cmp.Equal(fixture.Actual, fixture.Expected) == false {
				t.Fatalf(cmp.Diff(fixture.Expected, fixture.Actual))
			}
		})
	}
}

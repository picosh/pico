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

func TestCalcRoutes(t *testing.T) {
	fixtures := []RouteFixture{
		{
			Name:   "basic-index",
			Actual: calcRoutes("test", "/index.html", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/index.html", Status: 200},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "basic-txt",
			Actual: calcRoutes("test", "/index.txt", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/index.txt", Status: 200},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "basic-named",
			Actual: calcRoutes("test", "/wow.html", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/wow.html", Status: 200},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "subdirectory-index",
			Actual: calcRoutes("test", "/nice/index.html", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/nice/index.html", Status: 200},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "subdirectory-named",
			Actual: calcRoutes("test", "/nice/wow.html", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/nice/wow.html", Status: 200},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "subdirectory-bare",
			Actual: calcRoutes("test", "/nice/", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/nice/index.html", Status: 200},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "trailing-slash",
			Actual: calcRoutes("test", "/folder", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/folder.html", Status: 200},
				{Filepath: "/folder/", Status: 301},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name: "spa",
			Actual: calcRoutes("test", "/nice.html", []*RedirectRule{
				{
					From:   "/*",
					To:     "/index.html",
					Status: 200,
				},
			}),
			Expected: []*HttpReply{
				{Filepath: "test/nice.html", Status: 200},
				{Filepath: "test/index.html", Status: 200},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "xml",
			Actual: calcRoutes("test", "/index.xml", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/index.xml", Status: 200},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name: "redirectRule",
			Actual: calcRoutes(
				"test",
				"/wow",
				[]*RedirectRule{
					{
						From:   "/wow",
						To:     "index.html",
						Status: 301,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "test/wow.html", Status: 200},
				{Filepath: "test/index.html", Status: 301},
				{Filepath: "/wow/", Status: 301},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name: "redirectToPico",
			Actual: calcRoutes(
				"test",
				"/tester1",
				[]*RedirectRule{
					{
						From:   "/tester1",
						To:     "https://pico.sh",
						Status: 301,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "test/tester1.html", Status: 200},
				{Filepath: "https://pico.sh", Status: 301},
				{Filepath: "/tester1/", Status: 301},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name: "root",
			Actual: calcRoutes(
				"test",
				"",
				[]*RedirectRule{},
			),
			Expected: []*HttpReply{
				{Filepath: "test/index.html", Status: 200},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name: "redirectToRoot",
			Actual: calcRoutes(
				"test",
				"/wow/",
				[]*RedirectRule{
					{
						From:   "/wow/",
						To:     "/",
						Status: 301,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "test/wow/index.html", Status: 200},
				{Filepath: "test/index.html", Status: 301},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name: "force",
			Actual: calcRoutes(
				"test",
				"/wow",
				[]*RedirectRule{
					{
						From:   "/wow",
						To:     "/",
						Status: 301,
						Force:  true,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "test/index.html", Status: 301},
				{Filepath: "/wow/", Status: 301},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name: "redirectFullUrl",
			Actual: calcRoutes(
				"test",
				"/wow.html",
				[]*RedirectRule{
					{
						From:   "/wow",
						To:     "https://pico.sh",
						Status: 301,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "test/wow.html", Status: 200},
				{Filepath: "https://pico.sh", Status: 301},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name: "redirectFullUrlDirectory",
			Actual: calcRoutes(
				"test",
				"/wow",
				[]*RedirectRule{
					{
						From:   "/wow",
						To:     "https://pico.sh",
						Status: 301,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "test/wow.html", Status: 200},
				{Filepath: "https://pico.sh", Status: 301},
				{Filepath: "/wow/", Status: 301},
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

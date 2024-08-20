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

func TestCalcRoutes(t *testing.T) {
	fixtures := []RouteFixture{
		{
			Name:   "basic-index",
			Actual: calcRoutes("test", "/index.html", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/index.html", Status: 200},
				{Filepath: "/index.html/", Status: 301},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "basic-txt",
			Actual: calcRoutes("test", "/index.txt", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/index.txt", Status: 200},
				{Filepath: "/index.txt/", Status: 301},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "basic-named",
			Actual: calcRoutes("test", "/wow.html", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/wow.html", Status: 200},
				{Filepath: "/wow.html/", Status: 301},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "subdirectory-index",
			Actual: calcRoutes("test", "/nice/index.html", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/nice/index.html", Status: 200},
				{Filepath: "/nice/index.html/", Status: 301},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "subdirectory-named",
			Actual: calcRoutes("test", "/nice/wow.html", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/nice/wow.html", Status: 200},
				{Filepath: "/nice/wow.html/", Status: 301},
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
				{Filepath: "test/folder", Status: 200},
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
				{Filepath: "/index.html/", Status: 301},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name:   "xml",
			Actual: calcRoutes("test", "/index.xml", []*RedirectRule{}),
			Expected: []*HttpReply{
				{Filepath: "test/index.xml", Status: 200},
				{Filepath: "/index.xml/", Status: 301},
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
						To:     "/index.html",
						Status: 301,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "test/wow", Status: 200},
				{Filepath: "test/wow.html", Status: 200},
				{Filepath: "/index.html", Status: 301},
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
				{Filepath: "test/tester1", Status: 200},
				{Filepath: "test/tester1.html", Status: 200},
				{Filepath: "https://pico.sh", Status: 301},
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
				{Filepath: "/", Status: 301},
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
				{Filepath: "/", Status: 301},
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
				{Filepath: "test/wow", Status: 200},
				{Filepath: "test/wow.html", Status: 200},
				{Filepath: "https://pico.sh", Status: 301},
			},
		},
		{
			Name: "redirectDirectory",
			Actual: calcRoutes(
				"public",
				"/xyz",
				[]*RedirectRule{
					{
						From:   "/xyz",
						To:     "/wrk-xyz",
						Status: 301,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "public/xyz", Status: 200},
				{Filepath: "public/xyz.html", Status: 200},
				{Filepath: "/wrk-xyz", Status: 301},
				{Filepath: "/xyz/", Status: 301},
				{Filepath: "public/404.html", Status: 404},
			},
		},
		{
			Name: "redirectSubDirectory",
			Actual: calcRoutes(
				"public",
				"/folder2",
				[]*RedirectRule{
					{
						From:   "/folder2",
						To:     "/folder",
						Status: 200,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "public/folder2", Status: 200},
				{Filepath: "public/folder2.html", Status: 200},
				{Filepath: "public/folder", Status: 200},
				{Filepath: "public/folder.html", Status: 200},
				{Filepath: "/folder/", Status: 301},
				{Filepath: "public/404.html", Status: 404},
			},
		},
		{
			Name: "redirectFromAndToSame",
			Actual: calcRoutes(
				"public",
				"/folder2",
				[]*RedirectRule{
					{
						From:   "/folder2",
						To:     "/folder2",
						Status: 200,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "public/folder2", Status: 200},
				{Filepath: "public/folder2.html", Status: 200},
				{Filepath: "/folder2/", Status: 301},
				{Filepath: "public/404.html", Status: 404},
			},
		},
		{
			Name: "redirectNoTrailingSlash",
			Actual: calcRoutes(
				"public",
				"/space/",
				[]*RedirectRule{
					{
						From:   "/space",
						To:     "/frontier/",
						Status: 301,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "public/space/index.html", Status: 200},
				{Filepath: "/frontier/", Status: 301},
				{Filepath: "public/404.html", Status: 404},
			},
		},
		{
			Name: "redirectWithTrailingSlash",
			Actual: calcRoutes(
				"public",
				"/space",
				[]*RedirectRule{
					{
						From:   "/space/",
						To:     "/frontier/",
						Status: 301,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "public/space", Status: 200},
				{Filepath: "public/space.html", Status: 200},
				{Filepath: "/frontier/", Status: 301},
				{Filepath: "/space/", Status: 301},
				{Filepath: "public/404.html", Status: 404},
			},
		},
		{
			Name: "directoryWithExtension",
			Actual: calcRoutes(
				"public",
				"/space.nvim",
				[]*RedirectRule{},
			),
			Expected: []*HttpReply{
				{Filepath: "public/space.nvim", Status: 200},
				{Filepath: "public/space.nvim.html", Status: 200},
				{Filepath: "/space.nvim/", Status: 301},
				{Filepath: "public/404.html", Status: 404},
			},
		},
		{
			Name: "rewrite-to-site",
			Actual: calcRoutes(
				"public",
				"/",
				[]*RedirectRule{
					{
						From:   "/*",
						To:     "https://my-other-site.pgs.sh/:splat",
						Status: 200,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "public/index.html", Status: 200},
				{Filepath: "https://my-other-site.pgs.sh/", Status: 200},
			},
		},
		{
			Name: "rewrite-to-site-subdir",
			Actual: calcRoutes(
				"public",
				"/plugin/nice/",
				[]*RedirectRule{
					{
						From:   "/*",
						To:     "https://my-other-site.pgs.sh/:splat",
						Status: 200,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "public/plugin/nice/index.html", Status: 200},
				{Filepath: "https://my-other-site.pgs.sh/plugin/nice/", Status: 200},
			},
		},
		{
			Name: "rewrite-to-another-pgs-site",
			Actual: calcRoutes(
				"public",
				"/my-site/index.html",
				[]*RedirectRule{
					{
						From:   "/my-site/*",
						To:     "https://my-other-site.pgs.sh/:splat",
						Status: 200,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "public/my-site/index.html", Status: 200},
				{Filepath: "https://my-other-site.pgs.sh/index.html", Status: 200},
			},
		},
		{
			Name: "rewrite-placeholders",
			Actual: calcRoutes(
				"public",
				"/news/02/12/2004/my-story",
				[]*RedirectRule{
					{
						From:   "/news/:month/:date/:year/*",
						To:     "/blog/:year/:month/:date/:splat",
						Status: 200,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "public/news/02/12/2004/my-story", Status: 200},
				{Filepath: "public/news/02/12/2004/my-story.html", Status: 200},
				{Filepath: "public/blog/2004/02/12/my-story", Status: 200},
				{Filepath: "public/blog/2004/02/12/my-story.html", Status: 200},
				{Filepath: "/blog/2004/02/12/my-story/", Status: 301},
				{Filepath: "public/404.html", Status: 404},
			},
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			if cmp.Equal(fixture.Actual, fixture.Expected) == false {
				//nolint
				t.Fatalf(cmp.Diff(fixture.Expected, fixture.Actual))
			}
		})
	}
}

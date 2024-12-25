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
			Name: "redirect-rule",
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
			Name: "redirect-to-pico",
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
			Name: "redirect-to-root",
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
			Name: "redirect-full-url",
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
				{Filepath: "/wow.html/", Status: 301},
				{Filepath: "test/404.html", Status: 404},
			},
		},
		{
			Name: "redirect-full-url-directory",
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
			Name: "redirect-directory",
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
			Name: "redirect-sub-directory",
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
			Name: "redirect-from-and-to-same",
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
			Name: "redirect-no-trailing-slash",
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
			Name: "redirect-with-trailing-slash",
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
			Name: "directory-with-extension",
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
		{
			Name: "302-redirect",
			Actual: calcRoutes(
				"public",
				"/pages/chem351.html",
				[]*RedirectRule{
					{
						From:   "/pages/chem351.html",
						To:     "/pages/chem351",
						Status: 302,
						Force:  true,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "/pages/chem351", Status: 302},
				{Filepath: "/pages/chem351.html/", Status: 301},
				{Filepath: "public/404.html", Status: 404},
			},
		},
		{
			Name: "302-redirect-non-match",
			Actual: calcRoutes(
				"public",
				"/pages/chem351",
				[]*RedirectRule{
					{
						From:   "/pages/chem351.html",
						To:     "/pages/chem351",
						Status: 302,
						Force:  true,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "public/pages/chem351", Status: 200},
				{Filepath: "public/pages/chem351.html", Status: 200},
				{Filepath: "/pages/chem351/", Status: 301},
				{Filepath: "public/404.html", Status: 404},
			},
		},
		{
			Name: "wildcard-with-word",
			Actual: calcRoutes(
				"public",
				"/pictures/soup",
				[]*RedirectRule{
					{
						From:   "/pictures*",
						To:     "https://super.fly.sh/:splat",
						Status: 200,
					},
					{
						From:   "/*",
						To:     "https://super.fly.sh/:splat",
						Status: 302,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "public/pictures/soup", Status: 200},
				{Filepath: "public/pictures/soup.html", Status: 200},
				{Filepath: "https://super.fly.sh/soup", Status: 200},
			},
		},
		{
			Name: "wildcard-multiple",
			Actual: calcRoutes(
				"public",
				"/super/ficial.html",
				[]*RedirectRule{
					{
						From:   "/pictures*",
						To:     "https://super.fly.sh/:splat",
						Status: 200,
					},
					{
						From:   "/*",
						To:     "https://super.fly.sh/:splat",
						Status: 302,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "public/super/ficial.html", Status: 200},
				{Filepath: "https://super.fly.sh/super/ficial.html", Status: 302},
			},
		},
		{
			Name: "well-known-splat-suffix",
			Actual: calcRoutes(
				"public",
				"/.well-known/nodeinfo",
				[]*RedirectRule{
					{
						From:   "/.well-known/nodeinfo*",
						To:     "https://some.dev/.well-known/nodeinfo:splat",
						Status: 301,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "public/.well-known/nodeinfo", Status: 200},
				{Filepath: "public/.well-known/nodeinfo.html", Status: 200},
				{Filepath: "https://some.dev/.well-known/nodeinfo", Status: 301},
			},
		},
		{
			Name: "wildcard-query-param",
			Actual: calcRoutes(
				"public",
				"/.well-known/webfinger?query=nice",
				[]*RedirectRule{
					{
						From:   "/.well-known/webfinger*",
						To:     "https://some.dev/.well-known/webfinger:splat",
						Status: 301,
					},
				},
			),
			Expected: []*HttpReply{
				{Filepath: "public/.well-known/webfinger?query=nice", Status: 200},
				{Filepath: "public/.well-known/webfinger?query=nice.html", Status: 200},
				{Filepath: "https://some.dev/.well-known/webfinger?query=nice", Status: 301},
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

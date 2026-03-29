package storage

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

type Fixture struct {
	name   string
	input  string
	expect *ImgProcessOpts
}

func TestUriToImgProcessOpts(t *testing.T) {
	fixtures := []Fixture{
		{
			name:  "imgs_api_height",
			input: "/x500",
			expect: &ImgProcessOpts{
				Ratio: &Ratio{
					Width:  0,
					Height: 500,
				},
			},
		},
		{
			name:  "imgs_api_width",
			input: "/500x",
			expect: &ImgProcessOpts{
				Ratio: &Ratio{
					Width:  500,
					Height: 0,
				},
			},
		},
		{
			name:  "imgs_api_both",
			input: "/500x600",
			expect: &ImgProcessOpts{
				Ratio: &Ratio{
					Width:  500,
					Height: 600,
				},
			},
		},
		{
			name:  "imgproxy_height",
			input: "/s::500",
			expect: &ImgProcessOpts{
				Ratio: &Ratio{
					Width:  0,
					Height: 500,
				},
			},
		},
		{
			name:  "imgproxy_width",
			input: "/s:500",
			expect: &ImgProcessOpts{
				Ratio: &Ratio{
					Width:  500,
					Height: 0,
				},
			},
		},
		{
			name:  "imgproxy_both",
			input: "/s:500:600",
			expect: &ImgProcessOpts{
				Ratio: &Ratio{
					Width:  500,
					Height: 600,
				},
			},
		},
		{
			name:  "imgproxy_quality",
			input: "/q:80",
			expect: &ImgProcessOpts{
				Quality: 80,
			},
		},
		{
			name:  "imgproxy_rotate",
			input: "/rt:90",
			expect: &ImgProcessOpts{
				Rotate: 90,
			},
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			results, err := UriToImgProcessOpts(fixture.input)
			if err != nil {
				t.Error(err)
			}
			if cmp.Equal(results, fixture.expect) == false {
				//nolint
				t.Fatal(cmp.Diff(fixture.expect, results))
			}
		})
	}
}

package storage

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

// imgproxy decodes the source URL, so a path has to survive that unchanged.
func TestImgProxySourceIsEscaped(t *testing.T) {
	var requested string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { requested = r.URL.Path }))
	defer srv.Close()
	t.Setenv("IMGPROXY_URL", srv.URL)

	for _, fp := range []string{
		"bucket/blog/a.png",
		"bucket/blog/a b.png",
		"bucket/blog/100%.png",
		"bucket/blog/a?b#c.png",
		"bucket/blog/%2e%2e/%2e%2e/other/secret.png",
	} {
		img := NewImgProxy(fp, &ImgProcessOpts{})
		img.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

		encoded := requested[strings.LastIndex(requested, "/")+1:]
		source, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("%s: %v in %q", fp, err, requested)
		}
		u, err := url.Parse(string(source))
		if err != nil || u.Scheme != "local" || u.Host != "" || u.Path != "/"+fp || u.RawQuery != "" || u.Fragment != "" {
			t.Errorf("%s: source = %q, %v", fp, source, err)
		}
	}
}

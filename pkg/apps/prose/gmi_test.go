package prose

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/postfs"
)

func TestGemtextPublishDate(t *testing.T) {
	published := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	explicit := time.Date(2024, 4, 5, 0, 0, 0, 0, time.UTC)
	uploader := &postUploader{Cfg: &Config{}}
	for _, tt := range []struct {
		name string
		text string
		new  bool
		date *time.Time
		err  bool
	}{
		{name: "new post without date", text: "# fixture\n", new: true},
		{name: "existing post without date", text: "# fixture\n"},
		{name: "explicit date", text: "=: date 2024-04-05\n# fixture\n", date: &explicit},
		{name: "invalid date", text: "=: date invalid\n# fixture\n", err: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			post := postfs.NewPost("fixture.gmi", []byte(tt.text))
			if !tt.new {
				post.PublishAt = &published
			}
			want := *post.PublishAt
			if tt.date != nil {
				want = *tt.date
			}
			_, err := uploader.setMeta(post)
			if (err != nil) != tt.err {
				t.Fatalf("metadata error = %v, want error %v", err, tt.err)
			}
			if post.PublishAt == nil || !post.PublishAt.Equal(want) {
				t.Fatalf("publish date = %v, want %v", post.PublishAt, want)
			}
		})
	}
}

func TestFilenameMeta(t *testing.T) {
	wantDate := time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		filename string
		date     *time.Time
		title    string
	}{
		{filename: "2024-01-05-my-post.gmi", date: &wantDate, title: "my post"},
		{filename: "dir/2024-01-05-a-b.md", date: &wantDate, title: "a b"},
		{filename: "my-post.gmi"},
		{filename: "2024-01-05.gmi"},
		{filename: "2024-13-01-x.gmi"},
		{filename: "2024-02-30-x.gmi"},
	} {
		t.Run(tt.filename, func(t *testing.T) {
			date, title := filenameMeta(tt.filename)
			if tt.date == nil {
				if date != nil || title != "" {
					t.Errorf("got date %v title %q, want none", date, title)
				}
				return
			}
			if date == nil || !date.Equal(*tt.date) || title != tt.title {
				t.Errorf("got date %v title %q, want %v %q", date, title, *tt.date, tt.title)
			}
		})
	}
}

func TestSetMetaFilenameDateAndTitle(t *testing.T) {
	uploader := &postUploader{Cfg: &Config{}}
	fnameDate := time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)
	metaDate := time.Date(2020, 2, 3, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name     string
		filename string
		text     string
		title    string
		date     *time.Time
	}{
		{name: "gmi filename only", filename: "2024-01-05-my-post.gmi", text: "# hi\n", title: "my post", date: &fnameDate},
		{name: "gmi frontmatter trumps", filename: "2024-01-05-my-post.gmi", text: "=: title From Meta\n=: date 2020-02-03\n# hi\n", title: "From Meta", date: &metaDate},
		{name: "gmi frontmatter title only", filename: "2024-01-05-my-post.gmi", text: "=: title From Meta\n# hi\n", title: "From Meta", date: &fnameDate},
		{name: "gmi frontmatter date only", filename: "2024-01-05-my-post.gmi", text: "=: date 2020-02-03\n# hi\n", title: "my post", date: &metaDate},
		// md falls back to its first H1 for the title, so use text without one.
		{name: "md filename only", filename: "2024-01-05-my-post.md", text: "some text\n", title: "my post", date: &fnameDate},
		{name: "md frontmatter trumps", filename: "2024-01-05-my-post.md", text: "---\ntitle: From Meta\ndate: 2020-02-03\n---\n# hi\n", title: "From Meta", date: &metaDate},
		{name: "no filename date keeps default", filename: "my-post.gmi", text: "# hi\n", title: "my post"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			post := postfs.NewPost(tt.filename, []byte(tt.text))
			if tt.date == nil {
				tt.date = post.PublishAt
			}
			if _, err := uploader.setMeta(post); err != nil {
				t.Fatal(err)
			}
			if post.Title != tt.title {
				t.Errorf("title = %q, want %q", post.Title, tt.title)
			}
			if post.PublishAt == nil || !post.PublishAt.Equal(*tt.date) {
				t.Errorf("publish date = %v, want %v", post.PublishAt, *tt.date)
			}
		})
	}
}

func TestGemtextAliases(t *testing.T) {
	for _, tt := range []struct {
		name string
		text string
		want []string
	}{
		{name: "plain", text: "=: aliases about contact\n", want: []string{"about", "contact"}},
		{name: "leading slashes", text: "=: aliases /about /contact\n", want: []string{"about", "contact"}},
		{name: "extra spaces", text: "=: aliases  about   contact \n", want: []string{"about", "contact"}},
		{name: "none", text: "# fixture\n", want: nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gem, err := parseGemText(tt.text)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(gem.Aliases, tt.want) {
				t.Errorf("aliases = %q, want %q", gem.Aliases, tt.want)
			}
		})
	}
}

func TestGemtextTags(t *testing.T) {
	gem, err := parseGemText("=: tags  a  b \n# fixture\n")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(gem.Tags, []string{"a", "b"}) {
		t.Errorf("tags = %q, want [a b]", gem.Tags)
	}
}

func TestGemtextLinkSchemes(t *testing.T) {
	text := strings.Join([]string{
		"=> https://pico.sh pico",
		"=> gemini://example.test/ capsule",
		"=> /posts/other relative",
		"=> javascript:alert(1) click",
		"=> JaVaScRiPt:alert(1) mixed",
		"=> data:text/html,<script>x</script> data",
	}, "\n")
	gem, err := parseGemText(text)
	if err != nil {
		t.Fatal(err)
	}
	for _, href := range []string{"href='https://pico.sh'", "href='gemini://example.test/'", "href='/posts/other'"} {
		if !strings.Contains(gem.Html, href) {
			t.Errorf("missing %s in %s", href, gem.Html)
		}
	}
	lower := strings.ToLower(gem.Html)
	if strings.Contains(lower, "href='javascript") || strings.Contains(lower, "href='data:") {
		t.Errorf("unsafe link rendered: %s", gem.Html)
	}
	if !strings.Contains(gem.Html, "<p>click</p>") {
		t.Errorf("unsafe link text missing: %s", gem.Html)
	}
}

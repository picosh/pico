package prose

import (
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

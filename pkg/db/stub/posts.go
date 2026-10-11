package stub

import (
	"database/sql"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/picosh/pico/pkg/db"
)

// PostsDB keeps one user's posts in memory, with the unique filename and
// slug constraints of the posts table.
type PostsDB struct {
	*StubDB
	User   *db.User
	Pubkey string

	mu      sync.Mutex
	posts   []*db.Post
	aliases map[string][]string
	n       int
}

func NewPostsDB(logger *slog.Logger, user *db.User) *PostsDB {
	return &PostsDB{StubDB: NewStubDB(logger), User: user}
}

func (m *PostsDB) FindUserByPubkey(key string) (*db.User, error) {
	if key != m.Pubkey {
		return nil, sql.ErrNoRows
	}
	return m.User, nil
}

func (m *PostsDB) ReplaceTagsByPost(tags []string, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i := slices.IndexFunc(m.posts, func(p *db.Post) bool { return p.ID == id }); i >= 0 {
		m.posts[i].Tags = slices.Clone(tags)
	}
	return nil
}

func (m *PostsDB) ReplaceAliasesByPost(aliases []string, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setAliases(id, aliases)
	return nil
}

// Aliases returns the aliases of the post with id.
func (m *PostsDB) Aliases(id string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.aliases[id])
}

// setAliases stores a post's aliases. The caller holds m.mu.
func (m *PostsDB) setAliases(id string, aliases []string) {
	if m.aliases == nil {
		m.aliases = map[string][]string{}
	}
	m.aliases[id] = slices.Clone(aliases)
}

func (m *PostsDB) FindUser(string) (*db.User, error)       { return m.User, nil }
func (m *PostsDB) FindUserByName(string) (*db.User, error) { return m.User, nil }
func (m *PostsDB) InsertAccessLog(*db.AccessLog) error     { return nil }

func (m *PostsDB) FindFeature(userID, name string) (*db.FeatureFlag, error) {
	if name != "plus" {
		return nil, sql.ErrNoRows
	}
	expires := time.Now().Add(time.Hour)
	return &db.FeatureFlag{UserID: userID, Name: name, ExpiresAt: &expires}, nil
}

func (m *PostsDB) find(match func(*db.Post) bool) (*db.Post, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := slices.IndexFunc(m.posts, match)
	if i < 0 {
		return nil, sql.ErrNoRows
	}
	p := *m.posts[i]
	return &p, nil
}

func (m *PostsDB) FindPost(id string) (*db.Post, error) {
	return m.find(func(p *db.Post) bool { return p.ID == id })
}

func (m *PostsDB) FindPostWithFilename(filename, userID, space string) (*db.Post, error) {
	return m.find(func(p *db.Post) bool { return p.UserID == userID && p.Space == space && p.Filename == filename })
}

func (m *PostsDB) FindPostWithSlug(slug, userID, space string) (*db.Post, error) {
	return m.find(func(p *db.Post) bool { return p.UserID == userID && p.Space == space && p.Slug == slug })
}

func (m *PostsDB) FindAllPostsByUser(userID, space string) ([]*db.Post, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*db.Post
	for _, p := range m.posts {
		if p.UserID == userID && p.Space == space {
			c := *p
			out = append(out, &c)
		}
	}
	return out, nil
}

// conflict reports a post other than the ones in skip that p's filename or
// slug would collide with. The caller holds m.mu.
func (m *PostsDB) conflict(p *db.Post, skip ...string) error {
	for _, o := range m.posts {
		if slices.Contains(skip, o.ID) || o.UserID != p.UserID || o.Space != p.Space {
			continue
		}
		if o.Filename == p.Filename || o.Slug == p.Slug {
			return fmt.Errorf("duplicate key value violates unique constraint: %s", o.Filename)
		}
	}
	return nil
}

func (m *PostsDB) InsertPost(post *db.Post) (*db.Post, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.conflict(post); err != nil {
		return nil, err
	}
	m.n++
	p := *post
	p.ID = fmt.Sprintf("post-%d", m.n)
	now := time.Now()
	p.CreatedAt = &now
	m.posts = append(m.posts, &p)
	c := p
	return &c, nil
}

func (m *PostsDB) update(post *db.Post, filename bool, skip ...string) (*db.Post, error) {
	i := slices.IndexFunc(m.posts, func(p *db.Post) bool { return p.ID == post.ID })
	if i < 0 {
		return nil, sql.ErrNoRows
	}
	next := *m.posts[i]
	if filename {
		next.Filename, next.MimeType = post.Filename, post.MimeType
	}
	next.Slug, next.Title, next.Text, next.Description = post.Slug, post.Title, post.Text, post.Description
	next.UpdatedAt, next.PublishAt, next.FileSize, next.Shasum = post.UpdatedAt, post.PublishAt, post.FileSize, post.Shasum
	next.Data, next.Hidden, next.ExpiresAt = post.Data, post.Hidden, post.ExpiresAt
	if err := m.conflict(&next, append(skip, post.ID)...); err != nil {
		return nil, err
	}
	m.posts[i] = &next
	c := next
	return &c, nil
}

func (m *PostsDB) UpdatePost(post *db.Post) (*db.Post, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.update(post, false)
}

func (m *PostsDB) RenamePost(cur, next, replaced *db.Post, tags, aliases []string) (*db.Post, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	unchanged := func(read *db.Post, all bool) bool {
		i := slices.IndexFunc(m.posts, func(p *db.Post) bool { return p.ID == read.ID })
		if i < 0 {
			return false
		}
		p := m.posts[i]
		same := p.Filename == read.Filename && p.Text == read.Text && equalTime(p.UpdatedAt, read.UpdatedAt)
		if all {
			same = same && p.Slug == read.Slug && p.Title == read.Title && p.Description == read.Description &&
				equalTime(p.PublishAt, read.PublishAt) && p.Hidden == read.Hidden && equalTime(p.ExpiresAt, read.ExpiresAt)
		}
		return same
	}
	if !unchanged(cur, true) || (replaced != nil && !unchanged(replaced, false)) {
		return nil, db.ErrPostChanged
	}
	// Only what a new name can change is written.
	update := *m.posts[slices.IndexFunc(m.posts, func(p *db.Post) bool { return p.ID == cur.ID })]
	update.Filename, update.Slug, update.Title, update.Description = next.Filename, next.Slug, next.Title, next.Description
	update.PublishAt, update.Hidden, update.ExpiresAt, update.MimeType = next.PublishAt, next.Hidden, next.ExpiresAt, next.MimeType
	var skip []string
	if replaced != nil {
		skip = append(skip, replaced.ID)
	}
	p, err := m.update(&update, true, skip...)
	if err != nil {
		return nil, err
	}
	if replaced != nil {
		m.posts = slices.DeleteFunc(m.posts, func(p *db.Post) bool { return p.ID == replaced.ID })
	}
	i := slices.IndexFunc(m.posts, func(p *db.Post) bool { return p.ID == cur.ID })
	m.posts[i].Tags = slices.Clone(tags)
	m.setAliases(cur.ID, aliases)
	p.Tags = slices.Clone(tags)
	return p, nil
}

func equalTime(a, b *time.Time) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Equal(*b))
}

func (m *PostsDB) RemovePosts(ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.posts = slices.DeleteFunc(m.posts, func(p *db.Post) bool { return slices.Contains(ids, p.ID) })
	return nil
}

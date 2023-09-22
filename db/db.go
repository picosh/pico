package db

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/golang-jwt/jwt"
)

var ErrNameTaken = errors.New("username has already been claimed")
var ErrNameDenied = errors.New("username is on the denylist")
var ErrNameInvalid = errors.New("username has invalid characters in it")

type PublicKey struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	Key       string     `json:"key"`
	CreatedAt *time.Time `json:"created_at"`
}

type User struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	PublicKey *PublicKey `json:"public_key,omitempty"`
	CreatedAt *time.Time `json:"created_at"`
}

type PostData struct {
	ImgPath    string     `json:"img_path"`
	LastDigest *time.Time `json:"last_digest"`
}

type Project struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Name       string     `json:"name"`
	ProjectDir string     `json:"project_dir"`
	Username   string     `json:"username"`
	CreatedAt  *time.Time `json:"created_at"`
}

// Make the Attrs struct implement the driver.Valuer interface. This method
// simply returns the JSON-encoded representation of the struct.
func (p PostData) Value() (driver.Value, error) {
	return json.Marshal(p)
}

// Make the Attrs struct implement the sql.Scanner interface. This method
// simply decodes a JSON-encoded value into the struct fields.
func (p *PostData) Scan(value interface{}) error {
	b, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}

	return json.Unmarshal(b, &p)
}

type FeedItemData struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Content     string     `json:"content"`
	Link        string     `json:"link"`
	PublishedAt *time.Time `json:"published_at"`
}

// Make the Attrs struct implement the driver.Valuer interface. This method
// simply returns the JSON-encoded representation of the struct.
func (p FeedItemData) Value() (driver.Value, error) {
	return json.Marshal(p)
}

// Make the Attrs struct implement the sql.Scanner interface. This method
// simply decodes a JSON-encoded value into the struct fields.
func (p *FeedItemData) Scan(value interface{}) error {
	b, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}

	return json.Unmarshal(b, &p)
}

type Post struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	Filename    string     `json:"filename"`
	Slug        string     `json:"slug"`
	Title       string     `json:"title"`
	Text        string     `json:"text"`
	Description string     `json:"description"`
	CreatedAt   *time.Time `json:"created_at"`
	PublishAt   *time.Time `json:"publish_at"`
	Username    string     `json:"username"`
	UpdatedAt   *time.Time `json:"updated_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	Hidden      bool       `json:"hidden"`
	Views       int        `json:"views"`
	Space       string     `json:"space"`
	Score       string     `json:"score"`
	Shasum      string     `json:"shasum"`
	FileSize    int        `json:"file_size"`
	MimeType    string     `json:"mime_type"`
	Data        PostData   `json:"data"`
	Tags        []string   `json:"tags"`
}

type Paginate[T any] struct {
	Data  []T
	Total int
}

type Analytics struct {
	TotalUsers     int
	UsersLastMonth int
	TotalPosts     int
	PostsLastMonth int
	UsersWithPost  int
}

type PostAnalytics struct {
	ID       string
	PostID   string
	Views    int
	UpdateAt *time.Time
}

type Pager struct {
	Num  int
	Page int
}

type FeedItem struct {
	ID        string
	PostID    string
	GUID      string
	Data      FeedItemData
	CreatedAt *time.Time
}

type ErrMultiplePublicKeys struct{}

func (m *ErrMultiplePublicKeys) Error() string {
	return "there are multiple users with this public key, you must provide the username when using SSH: `ssh <user>@<domain>`\n"
}

var NameValidator = regexp.MustCompile("^[a-zA-Z0-9]{1,50}$")
var DenyList = []string{
	"admin",
	"abuse",
	"cgi",
	"ops",
	"help",
	"spec",
	"root",
	"new",
	"create",
	"www",
}

func GenerateToken(secret string, user *User) (string, error) {
	// Create a new token object, specifying signing method and the claims
	// you would like it to contain.
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":  user.ID,
		"username": user.Name,
	})

	// Sign and get the complete encoded token as a string using the secret
	tokenString, err := token.SignedString([]byte(secret))
	return tokenString, err
}

type DB interface {
	AddUser() (string, error)
	RemoveUsers(userIDs []string) error
	LinkUserKey(userID string, key string) error
	FindPublicKeyForKey(key string) (*PublicKey, error)
	FindKeysForUser(user *User) ([]*PublicKey, error)
	RemoveKeys(keyIDs []string) error

	FindSiteAnalytics(space string) (*Analytics, error)

	FindUsers() ([]*User, error)
	FindUserForName(name string) (*User, error)
	FindUserForNameAndKey(name string, key string) (*User, error)
	FindUserForKey(name string, key string) (*User, error)
	FindUser(userID string) (*User, error)
	ValidateName(name string) (bool, error)
	SetUserName(userID string, name string) error

	FindUserForToken(token string) (*User, error)
	InsertToken(userID, token string, expiresAt *time.Time) (string, error)

	FindPosts() ([]*Post, error)
	FindPost(postID string) (*Post, error)
	FindPostsForUser(pager *Pager, userID string, space string) (*Paginate[*Post], error)
	FindAllPostsForUser(userID string, space string) ([]*Post, error)
	FindPostsBeforeDate(date *time.Time, space string) ([]*Post, error)
	FindExpiredPosts(space string) ([]*Post, error)
	FindUpdatedPostsForUser(userID string, space string) ([]*Post, error)
	FindPostWithFilename(filename string, userID string, space string) (*Post, error)
	FindPostWithSlug(slug string, userID string, space string) (*Post, error)
	FindAllPosts(pager *Pager, space string) (*Paginate[*Post], error)
	FindAllUpdatedPosts(pager *Pager, space string) (*Paginate[*Post], error)
	InsertPost(post *Post) (*Post, error)
	UpdatePost(post *Post) (*Post, error)
	RemovePosts(postIDs []string) error

	ReplaceTagsForPost(tags []string, postID string) error
	FindUserPostsByTag(pager *Pager, tag, userID, space string) (*Paginate[*Post], error)
	FindPostsByTag(pager *Pager, tag, space string) (*Paginate[*Post], error)
	FindPopularTags(space string) ([]string, error)
	FindTagsForPost(postID string) ([]string, error)

	ReplaceAliasesForPost(aliases []string, postID string) error

	AddViewCount(postID string) (int, error)

	HasFeatureForUser(userID string, feature string) bool
	FindTotalSizeForUser(userID string) (int, error)

	InsertFeedItems(postID string, items []*FeedItem) error
	FindFeedItemsByPostID(postID string) ([]*FeedItem, error)

	InsertProject(userID, name, projectDir string) (string, error)
	UpdateProject(userID, name string) error
	LinkToProject(userID, projectID, projectDir string, commit bool) error
	RemoveProject(projectID string) error
	FindProjectByName(userID, name string) (*Project, error)
	FindProjectLinks(userID, name string) ([]*Project, error)
	FindProjectsByUser(userID string) ([]*Project, error)
	FindProjectsByPrefix(userID, name string) ([]*Project, error)
	FindAllProjects(page *Pager) (*Paginate[*Project], error)

	Close() error
}

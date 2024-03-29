package db

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

var ErrNameTaken = errors.New("username has already been claimed")
var ErrNameDenied = errors.New("username is on the denylist")
var ErrNameInvalid = errors.New("username has invalid characters in it")
var ErrPublicKeyTaken = errors.New("public key is already associated with another user")

type PublicKey struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	Name      string     `json:"name"`
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

type Project struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Name       string     `json:"name"`
	ProjectDir string     `json:"project_dir"`
	Username   string     `json:"username"`
	Acl        ProjectAcl `json:"acl"`
	CreatedAt  *time.Time `json:"created_at"`
	UpdatedAt  *time.Time `json:"updated_at"`
}

type ProjectAcl struct {
	Type string   `json:"type"`
	Data []string `json:"data"`
}

// Make the Attrs struct implement the driver.Valuer interface. This method
// simply returns the JSON-encoded representation of the struct.
func (p ProjectAcl) Value() (driver.Value, error) {
	return json.Marshal(p)
}

// Make the Attrs struct implement the sql.Scanner interface. This method
// simply decodes a JSON-encoded value into the struct fields.
func (p *ProjectAcl) Scan(value interface{}) error {
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

type SummaryOpts struct {
	FkID     string
	By       string
	Interval string
	Origin   time.Time
	Where    string
}

type PostAnalytics struct {
	ID       string
	PostID   string
	Views    int
	UpdateAt *time.Time
}

type AnalyticsVisits struct {
	ID        string
	UserID    string
	ProjectID string
	PostID    string
	Host      string
	Path      string
	IpAddress string
	UserAgent string
	Referer   string
	Status    int
}

type VisitInterval struct {
	PostID    string     `json:"post_id"`
	ProjectID string     `json:"project_id"`
	Interval  *time.Time `json:"interval"`
	Visitors  int        `json:"visitors"`
}

type VisitUrl struct {
	PostID    string `json:"post_id"`
	ProjectID string `json:"project_id"`
	Url       string `json:"url"`
	Count     int    `json:"count"`
}

type SummaryVisits struct {
	Intervals   []*VisitInterval `json:"intervals"`
	TopUrls     []*VisitUrl      `json:"top_urls"`
	TopReferers []*VisitUrl      `json:"top_referers"`
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

type Token struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	Name      string     `json:"name"`
	CreatedAt *time.Time `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type FeatureFlag struct {
	ID               string          `json:"id"`
	UserID           string          `json:"user_id"`
	PaymentHistoryID string          `json:"payment_history_id"`
	Name             string          `json:"name"`
	CreatedAt        *time.Time      `json:"created_at"`
	ExpiresAt        *time.Time      `json:"expires_at"`
	Data             FeatureFlagData `json:"data"`
}

func NewFeatureFlag(userID, name string, storageMax uint64, fileMax int64) *FeatureFlag {
	return &FeatureFlag{
		UserID: userID,
		Name:   name,
		Data: FeatureFlagData{
			StorageMax: storageMax,
			FileMax:    fileMax,
		},
	}
}

func (ff *FeatureFlag) FindStorageMax(defaultSize uint64) uint64 {
	if ff.Data.StorageMax == 0 {
		return defaultSize
	}
	return ff.Data.StorageMax
}

func (ff *FeatureFlag) FindFileMax(defaultSize int64) int64 {
	if ff.Data.FileMax == 0 {
		return defaultSize
	}
	return ff.Data.FileMax
}

func (ff *FeatureFlag) IsValid() bool {
	if ff.ExpiresAt.IsZero() {
		return false
	}
	return ff.ExpiresAt.After(time.Now())
}

type FeatureFlagData struct {
	StorageMax uint64 `json:"storage_max"`
	FileMax    int64  `json:"file_max"`
}

// Make the Attrs struct implement the driver.Valuer interface. This method
// simply returns the JSON-encoded representation of the struct.
func (p FeatureFlagData) Value() (driver.Value, error) {
	return json.Marshal(p)
}

// Make the Attrs struct implement the sql.Scanner interface. This method
// simply decodes a JSON-encoded value into the struct fields.
func (p *FeatureFlagData) Scan(value interface{}) error {
	b, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(b, &p)
}

type PaymentHistoryData struct {
	Notes string `json:"notes"`
	TxID  string `json:"tx_id"`
}

// Make the Attrs struct implement the driver.Valuer interface. This method
// simply returns the JSON-encoded representation of the struct.
func (p PaymentHistoryData) Value() (driver.Value, error) {
	return json.Marshal(p)
}

// Make the Attrs struct implement the sql.Scanner interface. This method
// simply decodes a JSON-encoded value into the struct fields.
func (p *PaymentHistoryData) Scan(value interface{}) error {
	b, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(b, &p)
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

type DB interface {
	RegisterUser(name, pubkey string) (*User, error)
	RemoveUsers(userIDs []string) error
	LinkUserKey(userID string, pubkey string, tx *sql.Tx) error
	UpdatePublicKey(pubkeyID, name string) (*PublicKey, error)
	InsertPublicKey(userID, pubkey, name string, tx *sql.Tx) (*PublicKey, error)
	FindPublicKeyForKey(pubkey string) (*PublicKey, error)
	FindPublicKey(pubkeyID string) (*PublicKey, error)
	FindKeysForUser(user *User) ([]*PublicKey, error)
	RemoveKeys(pubkeyIDs []string) error

	FindSiteAnalytics(space string) (*Analytics, error)

	FindUsers() ([]*User, error)
	FindUserForName(name string) (*User, error)
	FindUserForNameAndKey(name string, pubkey string) (*User, error)
	FindUserForKey(name string, pubkey string) (*User, error)
	FindUser(userID string) (*User, error)
	ValidateName(name string) (bool, error)
	SetUserName(userID string, name string) error

	FindUserForToken(token string) (*User, error)
	FindTokensForUser(userID string) ([]*Token, error)
	InsertToken(userID, name string) (string, error)
	FindRssToken(userID string) (string, error)
	RemoveToken(tokenID string) error

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

	InsertVisit(view *AnalyticsVisits) error
	VisitSummary(opts *SummaryOpts) (*SummaryVisits, error)

	AddPicoPlusUser(username string, paymentType, txId string) error
	FindFeatureForUser(userID string, feature string) (*FeatureFlag, error)
	FindFeaturesForUser(userID string) ([]*FeatureFlag, error)
	HasFeatureForUser(userID string, feature string) bool
	FindTotalSizeForUser(userID string) (int, error)

	InsertFeedItems(postID string, items []*FeedItem) error
	FindFeedItemsByPostID(postID string) ([]*FeedItem, error)

	InsertProject(userID, name, projectDir string) (string, error)
	UpdateProject(userID, name string) error
	UpdateProjectAcl(userID, name string, acl ProjectAcl) error
	LinkToProject(userID, projectID, projectDir string, commit bool) error
	RemoveProject(projectID string) error
	FindProjectByName(userID, name string) (*Project, error)
	FindProjectLinks(userID, name string) ([]*Project, error)
	FindProjectsByUser(userID string) ([]*Project, error)
	FindProjectsByPrefix(userID, name string) ([]*Project, error)
	FindAllProjects(page *Pager, by string) (*Paginate[*Project], error)

	Close() error
}

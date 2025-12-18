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

// sqlite uses string to BLOB type and postgres uses []uint8 for JSONB.
func tcast(value any) ([]byte, error) {
	switch val := value.(type) {
	// sqlite3 BLOB
	case string:
		return []byte(val), nil
	// postgres JSONB: []uint8
	default:
		b, ok := val.([]byte)
		if !ok {
			return []byte{}, errors.New("type assertion to []byte failed")
		}
		return b, nil
	}
}

type PublicKey struct {
	ID        string     `json:"id" db:"id"`
	UserID    string     `json:"user_id" db:"user_id"`
	Name      string     `json:"name" db:"name"`
	Key       string     `json:"public_key" db:"public_key"`
	CreatedAt *time.Time `json:"created_at" db:"created_at"`
}

type User struct {
	ID        string     `json:"id" db:"id"`
	Name      string     `json:"name" db:"name"`
	PublicKey *PublicKey `json:"public_key,omitempty" db:"public_key,omitempty"`
	CreatedAt *time.Time `json:"created_at" db:"created_at"`
}

type PostData struct {
	ImgPath    string     `json:"img_path"`
	LastDigest *time.Time `json:"last_digest"`
	Attempts   int        `json:"attempts"`
}

// Make the Attrs struct implement the driver.Valuer interface. This method
// simply returns the JSON-encoded representation of the struct.
func (p PostData) Value() (driver.Value, error) {
	return json.Marshal(p)
}

// Make the Attrs struct implement the sql.Scanner interface. This method
// simply decodes a JSON-encoded value into the struct fields.
func (p *PostData) Scan(value any) error {
	b, err := tcast(value)
	if err != nil {
		return err
	}

	return json.Unmarshal(b, &p)
}

type Project struct {
	ID         string     `json:"id" db:"id"`
	UserID     string     `json:"user_id" db:"user_id"`
	Name       string     `json:"name" db:"name"`
	ProjectDir string     `json:"project_dir" db:"project_dir"`
	Username   string     `json:"username" db:"username"`
	Acl        ProjectAcl `json:"acl" db:"acl"`
	Blocked    string     `json:"blocked" db:"blocked"`
	CreatedAt  *time.Time `json:"created_at" db:"created_at"`
	UpdatedAt  *time.Time `json:"updated_at" db:"updated_at"`
}

type ProjectAcl struct {
	Type string   `json:"type" db:"type"`
	Data []string `json:"data" db:"data"`
}

// Make the Attrs struct implement the driver.Valuer interface. This method
// simply returns the JSON-encoded representation of the struct.
func (p ProjectAcl) Value() (driver.Value, error) {
	return json.Marshal(p)
}

// Make the Attrs struct implement the sql.Scanner interface. This method
// simply decodes a JSON-encoded value into the struct fields.
func (p *ProjectAcl) Scan(value any) error {
	b, err := tcast(value)
	if err != nil {
		return err
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
func (p *FeedItemData) Scan(value any) error {
	b, err := tcast(value)
	if err != nil {
		return err
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
	Shasum      string     `json:"shasum"`
	FileSize    int        `json:"file_size"`
	MimeType    string     `json:"mime_type"`
	Data        PostData   `json:"data"`
	Tags        []string   `json:"tags"`

	// computed
	IsVirtual bool
}

type Paginate[T any] struct {
	Data  []T
	Total int
}

type VisitInterval struct {
	Interval *time.Time `json:"interval"`
	Visitors int        `json:"visitors"`
}

type VisitUrl struct {
	Url   string `json:"url"`
	Count int    `json:"count"`
}

type SummaryOpts struct {
	Interval string
	Origin   time.Time
	Host     string
	Path     string
	UserID   string
	Limit    int
}

type SummaryVisits struct {
	Intervals    []*VisitInterval `json:"intervals"`
	TopUrls      []*VisitUrl      `json:"top_urls"`
	NotFoundUrls []*VisitUrl      `json:"not_found_urls"`
	TopReferers  []*VisitUrl      `json:"top_referers"`
}

type PostAnalytics struct {
	ID       string
	PostID   string
	Views    int
	UpdateAt *time.Time
}

type AnalyticsVisits struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	ProjectID   string `json:"project_id"`
	PostID      string `json:"post_id"`
	Namespace   string `json:"namespace"`
	Host        string `json:"host"`
	Path        string `json:"path"`
	IpAddress   string `json:"ip_address"`
	UserAgent   string `json:"user_agent"`
	Referer     string `json:"referer"`
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
}

type AccessLog struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	Service   string     `json:"service"`
	Pubkey    string     `json:"pubkey"`
	Identity  string     `json:"identity"`
	CreatedAt *time.Time `json:"created_at"`
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
	ID               string          `json:"id" db:"id"`
	UserID           string          `json:"user_id" db:"user_id"`
	PaymentHistoryID sql.NullString  `json:"payment_history_id" db:"payment_history_id"`
	Name             string          `json:"name" db:"name"`
	CreatedAt        *time.Time      `json:"created_at" db:"created_at"`
	ExpiresAt        *time.Time      `json:"expires_at" db:"expires_at"`
	Data             FeatureFlagData `json:"data" db:"data"`
}

func NewFeatureFlag(userID, name string, storageMax uint64, fileMax int64, specialFileMax int64) *FeatureFlag {
	return &FeatureFlag{
		UserID: userID,
		Name:   name,
		Data: FeatureFlagData{
			StorageMax:     storageMax,
			FileMax:        fileMax,
			SpecialFileMax: specialFileMax,
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

func (ff *FeatureFlag) FindSpecialFileMax(defaultSize int64) int64 {
	if ff.Data.SpecialFileMax == 0 {
		return defaultSize
	}
	return ff.Data.SpecialFileMax
}

func (ff *FeatureFlag) IsValid() bool {
	if ff.ExpiresAt.IsZero() {
		return false
	}
	return ff.ExpiresAt.After(time.Now())
}

type FeatureFlagData struct {
	StorageMax     uint64 `json:"storage_max" db:"storage_max"`
	FileMax        int64  `json:"file_max" db:"file_max"`
	SpecialFileMax int64  `json:"special_file_max" db:"special_file_max"`
}

// Make the Attrs struct implement the driver.Valuer interface. This method
// simply returns the JSON-encoded representation of the struct.
func (p FeatureFlagData) Value() (driver.Value, error) {
	return json.Marshal(p)
}

// Make the Attrs struct implement the sql.Scanner interface. This method
// simply decodes a JSON-encoded value into the struct fields.
func (p *FeatureFlagData) Scan(value any) error {
	b, err := tcast(value)
	if err != nil {
		return err
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
func (p *PaymentHistoryData) Scan(value any) error {
	b, err := tcast(value)
	if err != nil {
		return err
	}

	return json.Unmarshal(b, &p)
}

type ErrMultiplePublicKeys struct{}

func (m *ErrMultiplePublicKeys) Error() string {
	return "there are multiple users with this public key, you must provide the username when using SSH: `ssh <user>@<domain>`\n"
}

type UserStats struct {
	Prose  UserServiceStats
	Pastes UserServiceStats
	Feeds  UserServiceStats
	Pages  UserServiceStats
}

type UserServiceStats struct {
	Service          string
	Num              int
	FirstCreatedAt   time.Time
	LastestCreatedAt time.Time
	LatestUpdatedAt  time.Time
}

type TunsEventLog struct {
	ID             string     `json:"id"`
	ServerID       string     `json:"server_id"`
	Time           *time.Time `json:"time"`
	User           string     `json:"user"`
	UserId         string     `json:"user_id"`
	RemoteAddr     string     `json:"remote_addr"`
	EventType      string     `json:"event_type"`
	TunnelID       string     `json:"tunnel_id"`
	TunnelType     string     `json:"tunnel_type"`
	ConnectionType string     `json:"connection_type"`
	CreatedAt      *time.Time `json:"created_at"`
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
	"public",
	"global",
	"g",
	"root",
	"localhost",
	"ams",
	"ash",
	"nue",
}

type DB interface {
	RegisterUser(name, pubkey, comment string) (*User, error)
	RemoveUsers(userIDs []string) error
	UpdatePublicKey(pubkeyID, name string) (*PublicKey, error)
	InsertPublicKey(userID, pubkey, name string, tx *sql.Tx) error
	FindPublicKeyForKey(pubkey string) (*PublicKey, error)
	FindPublicKey(pubkeyID string) (*PublicKey, error)
	FindKeysForUser(user *User) ([]*PublicKey, error)
	RemoveKeys(pubkeyIDs []string) error

	FindUsers() ([]*User, error)
	FindUserByName(name string) (*User, error)
	FindUserForNameAndKey(name string, pubkey string) (*User, error)
	FindUserForKey(name string, pubkey string) (*User, error)
	FindUserByPubkey(pubkey string) (*User, error)
	FindUser(userID string) (*User, error)
	ValidateName(name string) (bool, error)
	SetUserName(userID string, name string) error

	FindUserForToken(token string) (*User, error)
	FindTokensForUser(userID string) ([]*Token, error)
	InsertToken(userID, name string) (string, error)
	UpsertToken(userID, name string) (string, error)
	FindTokenByName(userID, name string) (string, error)
	RemoveToken(tokenID string) error

	FindPosts() ([]*Post, error)
	FindPost(postID string) (*Post, error)
	FindPostsForUser(pager *Pager, userID string, space string) (*Paginate[*Post], error)
	FindAllPostsForUser(userID string, space string) ([]*Post, error)
	FindUsersWithPost(space string) ([]*User, error)
	FindPostsBeforeDate(date *time.Time, space string) ([]*Post, error)
	FindExpiredPosts(space string) ([]*Post, error)
	FindUpdatedPostsForUser(userID string, space string) ([]*Post, error)
	FindPostWithFilename(filename string, userID string, space string) (*Post, error)
	FindPostWithSlug(slug string, userID string, space string) (*Post, error)
	FindPostsForFeed(pager *Pager, space string) (*Paginate[*Post], error)
	FindAllUpdatedPosts(pager *Pager, space string) (*Paginate[*Post], error)
	InsertPost(post *Post) (*Post, error)
	UpdatePost(post *Post) (*Post, error)
	RemovePosts(postIDs []string) error

	ReplaceTagsForPost(tags []string, postID string) error
	FindUserPostsByTag(pager *Pager, tag, userID, space string) (*Paginate[*Post], error)
	FindPostsByTag(pager *Pager, tag, space string) (*Paginate[*Post], error)
	FindPopularTags(space string) ([]string, error)
	FindTagsForPost(postID string) ([]string, error)
	FindTagsForUser(userID string, space string) ([]string, error)

	ReplaceAliasesForPost(aliases []string, postID string) error

	InsertVisit(view *AnalyticsVisits) error
	VisitSummary(opts *SummaryOpts) (*SummaryVisits, error)
	FindVisitSiteList(opts *SummaryOpts) ([]*VisitUrl, error)
	VisitUrlNotFound(opts *SummaryOpts) ([]*VisitUrl, error)

	AddPicoPlusUser(username, email, paymentType, txId string) error
	FindFeature(userID string, feature string) (*FeatureFlag, error)
	FindFeaturesForUser(userID string) ([]*FeatureFlag, error)
	HasFeatureForUser(userID string, feature string) bool
	FindTotalSizeForUser(userID string) (int, error)
	InsertFeature(userID, name string, expiresAt time.Time) (*FeatureFlag, error)
	RemoveFeature(userID, names string) error

	InsertFeedItems(postID string, items []*FeedItem) error
	FindFeedItemsByPostID(postID string) ([]*FeedItem, error)

	UpsertProject(userID, name, projectDir string) (*Project, error)
	FindProjectByName(userID, name string) (*Project, error)

	FindUserStats(userID string) (*UserStats, error)

	InsertTunsEventLog(log *TunsEventLog) error
	FindTunsEventLogs(userID string) ([]*TunsEventLog, error)
	FindTunsEventLogsByAddr(userID, addr string) ([]*TunsEventLog, error)

	InsertAccessLog(log *AccessLog) error
	FindAccessLogs(userID string, fromDate *time.Time) ([]*AccessLog, error)
	FindPubkeysInAccessLogs(userID string) ([]string, error)
	FindAccessLogsByPubkey(pubkey string, fromDate *time.Time) ([]*AccessLog, error)

	Close() error
}

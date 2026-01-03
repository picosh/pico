package db

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
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
	ID          string     `json:"id" db:"id"`
	UserID      string     `json:"user_id" db:"user_id"`
	Filename    string     `json:"filename" db:"filename"`
	Slug        string     `json:"slug" db:"slug"`
	Title       string     `json:"title" db:"title"`
	Text        string     `json:"text" db:"text"`
	Description string     `json:"description" db:"description"`
	CreatedAt   *time.Time `json:"created_at" db:"created_at"`
	PublishAt   *time.Time `json:"publish_at" db:"publish_at"`
	Username    string     `json:"username" db:"name"`
	UpdatedAt   *time.Time `json:"updated_at" db:"updated_at"`
	ExpiresAt   *time.Time `json:"expires_at" db:"expires_at"`
	Hidden      bool       `json:"hidden" db:"hidden"`
	Views       int        `json:"views" db:"views"`
	Space       string     `json:"space" db:"cur_space"`
	Shasum      string     `json:"shasum" db:"shasum"`
	FileSize    int        `json:"file_size" db:"file_size"`
	MimeType    string     `json:"mime_type" db:"mime_type"`
	Data        PostData   `json:"data" db:"data"`
	Tags        []string   `json:"tags" db:"-"`

	// computed
	IsVirtual bool `db:"-"`
}

type Paginate[T any] struct {
	Data  []T
	Total int
}

type VisitInterval struct {
	Interval *time.Time `json:"interval" db:"interval"`
	Visitors int        `json:"visitors" db:"visitors"`
}

type VisitUrl struct {
	Url   string `json:"url" db:"url"`
	Count int    `json:"count" db:"count"`
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
	ID       string     `json:"id" db:"id"`
	PostID   string     `json:"post_id" db:"post_id"`
	Views    int        `json:"views" db:"views"`
	UpdateAt *time.Time `json:"updated_at" db:"updated_at"`
}

type AnalyticsVisits struct {
	ID          string `json:"id" db:"id"`
	UserID      string `json:"user_id" db:"user_id"`
	ProjectID   string `json:"project_id" db:"project_id"`
	PostID      string `json:"post_id" db:"post_id"`
	Namespace   string `json:"namespace" db:"namespace"`
	Host        string `json:"host" db:"host"`
	Path        string `json:"path" db:"path"`
	IpAddress   string `json:"ip_address" db:"ip_address"`
	UserAgent   string `json:"user_agent" db:"user_agent"`
	Referer     string `json:"referer" db:"referer"`
	Status      int    `json:"status" db:"status"`
	ContentType string `json:"content_type" db:"content_type"`
}

type AccessLogData struct{}

func (p *AccessLogData) Scan(value any) error {
	b, err := tcast(value)
	if err != nil {
		return err
	}

	return json.Unmarshal(b, &p)
}

type AccessLog struct {
	ID        string        `json:"id" db:"id"`
	UserID    string        `json:"user_id" db:"user_id"`
	Service   string        `json:"service" db:"service"`
	Pubkey    string        `json:"pubkey" db:"pubkey"`
	Identity  string        `json:"identity" db:"identity"`
	Data      AccessLogData `json:"data" db:"data"`
	CreatedAt *time.Time    `json:"created_at" db:"created_at"`
}

type Pager struct {
	Num  int
	Page int
}

type FeedItem struct {
	ID        string       `json:"id" db:"id"`
	PostID    string       `json:"post_id" db:"post_id"`
	GUID      string       `json:"guid" db:"guid"`
	Data      FeedItemData `json:"data" db:"data"`
	CreatedAt *time.Time   `json:"created_at" db:"created_at"`
}

type Token struct {
	ID        string     `json:"id" db:"id"`
	UserID    string     `json:"user_id" db:"user_id"`
	Name      string     `json:"name" db:"name"`
	Token     string     `json:"token" db:"token"`
	CreatedAt *time.Time `json:"created_at" db:"created_at"`
	ExpiresAt *time.Time `json:"expires_at" db:"expires_at"`
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
	ID             string     `json:"id" db:"id"`
	ServerID       string     `json:"server_id" db:"server_id"`
	Time           *time.Time `json:"time" db:"time"`
	User           string     `json:"user" db:"user"`
	UserId         string     `json:"user_id" db:"user_id"`
	RemoteAddr     string     `json:"remote_addr" db:"remote_addr"`
	EventType      string     `json:"event_type" db:"event_type"`
	TunnelID       string     `json:"tunnel_id" db:"tunnel_id"`
	TunnelType     string     `json:"tunnel_type" db:"tunnel_type"`
	ConnectionType string     `json:"connection_type" db:"connection_type"`
	CreatedAt      *time.Time `json:"created_at" db:"created_at"`
}

type PipeMonitor struct {
	ID        string        `json:"id" db:"id"`
	UserId    string        `json:"user_id" db:"user_id"`
	Topic     string        `json:"topic" db:"topic"`
	WindowDur time.Duration `json:"window_dur" db:"window_dur"`
	WindowEnd *time.Time    `json:"window_end" db:"window_end"`
	LastPing  *time.Time    `json:"last_ping" db:"last_ping"`
	CreatedAt *time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt *time.Time    `json:"updated_at" db:"updated_at"`
}

func (m *PipeMonitor) Status() error {
	if m.LastPing == nil {
		return fmt.Errorf("no ping received yet")
	}
	if m.WindowEnd == nil {
		return fmt.Errorf("window end not set")
	}
	now := time.Now().UTC()
	if now.After(*m.WindowEnd) {
		return fmt.Errorf(
			"window expired at %s",
			m.WindowEnd.UTC().Format("2006-01-02 15:04:05Z"),
		)
	}
	windowStart := m.WindowEnd.Add(-m.WindowDur)
	lastPingAfterStart := !m.LastPing.Before(windowStart)
	if !lastPingAfterStart {
		return fmt.Errorf(
			"last ping before window start: %s",
			windowStart.UTC().Format("2006-01-02 15:04:05Z"),
		)
	}
	return nil
}

func (m *PipeMonitor) GetNextWindow() *time.Time {
	win := m.WindowEnd.Add(m.WindowDur)
	return &win
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
	UpdatePublicKey(pubkeyID, name string) (*PublicKey, error)
	InsertPublicKey(userID, pubkey, name string) error
	FindKeysByUser(user *User) ([]*PublicKey, error)
	RemoveKeys(pubkeyIDs []string) error

	FindUsers() ([]*User, error)
	FindUserByName(name string) (*User, error)
	FindUserByKey(name string, pubkey string) (*User, error)
	FindUserByPubkey(pubkey string) (*User, error)
	FindUser(userID string) (*User, error)

	FindUserByToken(token string) (*User, error)
	FindTokensByUser(userID string) ([]*Token, error)
	InsertToken(userID, name string) (string, error)
	UpsertToken(userID, name string) (string, error)
	RemoveToken(tokenID string) error

	FindPosts() ([]*Post, error)
	FindPost(postID string) (*Post, error)
	FindPostsByUser(pager *Pager, userID string, space string) (*Paginate[*Post], error)
	FindAllPostsByUser(userID string, space string) ([]*Post, error)
	FindUsersWithPost(space string) ([]*User, error)
	FindExpiredPosts(space string) ([]*Post, error)
	FindPostWithFilename(filename string, userID string, space string) (*Post, error)
	FindPostWithSlug(slug string, userID string, space string) (*Post, error)
	FindPostsByFeed(pager *Pager, space string) (*Paginate[*Post], error)
	InsertPost(post *Post) (*Post, error)
	UpdatePost(post *Post) (*Post, error)
	RemovePosts(postIDs []string) error

	ReplaceTagsByPost(tags []string, postID string) error
	FindUserPostsByTag(pager *Pager, tag, userID, space string) (*Paginate[*Post], error)
	FindPostsByTag(pager *Pager, tag, space string) (*Paginate[*Post], error)
	FindPopularTags(space string) ([]string, error)
	ReplaceAliasesByPost(aliases []string, postID string) error

	InsertVisit(view *AnalyticsVisits) error
	VisitSummary(opts *SummaryOpts) (*SummaryVisits, error)
	FindVisitSiteList(opts *SummaryOpts) ([]*VisitUrl, error)
	VisitUrlNotFound(opts *SummaryOpts) ([]*VisitUrl, error)

	AddPicoPlusUser(username, email, paymentType, txId string) error
	FindFeature(userID string, feature string) (*FeatureFlag, error)
	FindFeaturesByUser(userID string) ([]*FeatureFlag, error)
	HasFeatureByUser(userID string, feature string) bool

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

	UpsertPipeMonitor(userID, topic string, dur time.Duration, winEnd *time.Time) error
	UpdatePipeMonitorLastPing(userID, topic string, lastPing *time.Time) error
	RemovePipeMonitor(userID, topic string) error
	FindPipeMonitorByTopic(userID, topic string) (*PipeMonitor, error)
	FindPipeMonitorsByUser(userID string) ([]*PipeMonitor, error)

	Close() error
}

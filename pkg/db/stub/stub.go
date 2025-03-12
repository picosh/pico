package stub

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/picosh/pico/pkg/db"
)

type StubDB struct {
	Logger *slog.Logger
}

var _ db.DB = (*StubDB)(nil)

func NewStubDB(logger *slog.Logger) *StubDB {
	d := &StubDB{
		Logger: logger,
	}
	d.Logger.Info("Connecting to test database")
	return d
}

var notImpl = fmt.Errorf("not implemented")

func (me *StubDB) RegisterUser(username, pubkey, comment string) (*db.User, error) {
	return nil, notImpl
}

func (me *StubDB) RemoveUsers(userIDs []string) error {
	return notImpl
}

func (me *StubDB) InsertPublicKey(userID, key, name string, tx *sql.Tx) error {
	return notImpl
}

func (me *StubDB) UpdatePublicKey(pubkeyID, name string) (*db.PublicKey, error) {
	return nil, notImpl
}

func (me *StubDB) FindPublicKeyForKey(key string) (*db.PublicKey, error) {
	return nil, notImpl
}

func (me *StubDB) FindPublicKey(pubkeyID string) (*db.PublicKey, error) {
	return nil, notImpl
}

func (me *StubDB) FindKeysForUser(user *db.User) ([]*db.PublicKey, error) {
	return []*db.PublicKey{}, notImpl
}

func (me *StubDB) RemoveKeys(keyIDs []string) error {
	return notImpl
}

func (me *StubDB) FindPostsBeforeDate(date *time.Time, space string) ([]*db.Post, error) {
	return []*db.Post{}, notImpl
}

func (me *StubDB) FindUserForKey(username string, key string) (*db.User, error) {
	return nil, notImpl
}

func (me *StubDB) FindUserByPubkey(key string) (*db.User, error) {
	return nil, notImpl
}

func (me *StubDB) FindUser(userID string) (*db.User, error) {
	return nil, notImpl
}

func (me *StubDB) ValidateName(name string) (bool, error) {
	return false, notImpl
}

func (me *StubDB) FindUserForName(name string) (*db.User, error) {
	return nil, notImpl
}

func (me *StubDB) FindUserForNameAndKey(name string, key string) (*db.User, error) {
	return nil, notImpl
}

func (me *StubDB) FindUserForToken(token string) (*db.User, error) {
	return nil, notImpl
}

func (me *StubDB) SetUserName(userID string, name string) error {
	return notImpl
}

func (me *StubDB) FindPostWithFilename(filename string, persona_id string, space string) (*db.Post, error) {
	return nil, notImpl
}

func (me *StubDB) FindPostWithSlug(slug string, user_id string, space string) (*db.Post, error) {
	return nil, notImpl
}

func (me *StubDB) FindPost(postID string) (*db.Post, error) {
	return nil, notImpl
}

func (me *StubDB) FindAllPosts(page *db.Pager, space string) (*db.Paginate[*db.Post], error) {
	return &db.Paginate[*db.Post]{}, notImpl
}

func (me *StubDB) FindAllUpdatedPosts(page *db.Pager, space string) (*db.Paginate[*db.Post], error) {
	return &db.Paginate[*db.Post]{}, notImpl
}

func (me *StubDB) InsertPost(post *db.Post) (*db.Post, error) {
	return nil, notImpl
}

func (me *StubDB) UpdatePost(post *db.Post) (*db.Post, error) {
	return nil, notImpl
}

func (me *StubDB) RemovePosts(postIDs []string) error {
	return notImpl
}

func (me *StubDB) FindPostsForUser(page *db.Pager, userID string, space string) (*db.Paginate[*db.Post], error) {
	return &db.Paginate[*db.Post]{}, notImpl
}

func (me *StubDB) FindAllPostsForUser(userID string, space string) ([]*db.Post, error) {
	return []*db.Post{}, notImpl
}

func (me *StubDB) FindPosts() ([]*db.Post, error) {
	return []*db.Post{}, notImpl
}

func (me *StubDB) FindExpiredPosts(space string) ([]*db.Post, error) {
	return []*db.Post{}, notImpl
}

func (me *StubDB) FindUpdatedPostsForUser(userID string, space string) ([]*db.Post, error) {
	return []*db.Post{}, notImpl
}

func (me *StubDB) Close() error {
	return notImpl
}

func (me *StubDB) InsertVisit(view *db.AnalyticsVisits) error {
	return notImpl
}

func (me *StubDB) VisitSummary(opts *db.SummaryOpts) (*db.SummaryVisits, error) {
	return &db.SummaryVisits{}, notImpl
}

func (me *StubDB) FindVisitSiteList(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	return []*db.VisitUrl{}, notImpl
}

func (me *StubDB) FindUsers() ([]*db.User, error) {
	return []*db.User{}, notImpl
}

func (me *StubDB) ReplaceTagsForPost(tags []string, postID string) error {
	return notImpl
}

func (me *StubDB) ReplaceAliasesForPost(aliases []string, postID string) error {
	return notImpl
}

func (me *StubDB) FindUserPostsByTag(page *db.Pager, tag, userID, space string) (*db.Paginate[*db.Post], error) {
	return &db.Paginate[*db.Post]{}, notImpl
}

func (me *StubDB) FindPostsByTag(pager *db.Pager, tag, space string) (*db.Paginate[*db.Post], error) {
	return &db.Paginate[*db.Post]{}, notImpl
}

func (me *StubDB) FindPopularTags(space string) ([]string, error) {
	return []string{}, notImpl
}

func (me *StubDB) FindTagsForPost(postID string) ([]string, error) {
	return []string{}, notImpl
}

func (me *StubDB) FindFeatureForUser(userID string, feature string) (*db.FeatureFlag, error) {
	return nil, notImpl
}

func (me *StubDB) FindFeaturesForUser(userID string) ([]*db.FeatureFlag, error) {
	return []*db.FeatureFlag{}, notImpl
}

func (me *StubDB) HasFeatureForUser(userID string, feature string) bool {
	return false
}

func (me *StubDB) FindTotalSizeForUser(userID string) (int, error) {
	return 0, notImpl
}

func (me *StubDB) InsertFeedItems(postID string, items []*db.FeedItem) error {
	return notImpl
}

func (me *StubDB) FindFeedItemsByPostID(postID string) ([]*db.FeedItem, error) {
	return []*db.FeedItem{}, notImpl
}

func (me *StubDB) UpsertProject(userID, name, projectDir string) (*db.Project, error) {
	return nil, notImpl
}

func (me *StubDB) InsertProject(userID, name, projectDir string) (string, error) {
	return "", notImpl
}

func (me *StubDB) UpdateProject(userID, name string) error {
	return notImpl
}

func (me *StubDB) FindProjectByName(userID, name string) (*db.Project, error) {
	return &db.Project{}, notImpl
}

func (me *StubDB) InsertToken(userID, name string) (string, error) {
	return "", notImpl
}

func (me *StubDB) UpsertToken(userID, name string) (string, error) {
	return "", notImpl
}

func (me *StubDB) FindTokenByName(userID, name string) (string, error) {
	return "", notImpl
}

func (me *StubDB) RemoveToken(tokenID string) error {
	return notImpl
}

func (me *StubDB) FindTokensForUser(userID string) ([]*db.Token, error) {
	return []*db.Token{}, notImpl
}

func (me *StubDB) InsertFeature(userID, name string, expiresAt time.Time) (*db.FeatureFlag, error) {
	return nil, notImpl
}

func (me *StubDB) RemoveFeature(userID string, name string) error {
	return notImpl
}

func (me *StubDB) AddPicoPlusUser(username, email, paymentType, txId string) error {
	return notImpl
}

func (me *StubDB) FindTagsForUser(userID string, tag string) ([]string, error) {
	return []string{}, notImpl
}

func (me *StubDB) FindUserStats(userID string) (*db.UserStats, error) {
	return nil, notImpl
}

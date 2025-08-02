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

var errNotImpl = fmt.Errorf("not implemented")

func (me *StubDB) RegisterUser(username, pubkey, comment string) (*db.User, error) {
	return nil, errNotImpl
}

func (me *StubDB) RemoveUsers(userIDs []string) error {
	return errNotImpl
}

func (me *StubDB) InsertPublicKey(userID, key, name string, tx *sql.Tx) error {
	return errNotImpl
}

func (me *StubDB) UpdatePublicKey(pubkeyID, name string) (*db.PublicKey, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindPublicKeyForKey(key string) (*db.PublicKey, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindPublicKey(pubkeyID string) (*db.PublicKey, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindKeysForUser(user *db.User) ([]*db.PublicKey, error) {
	return []*db.PublicKey{}, errNotImpl
}

func (me *StubDB) RemoveKeys(keyIDs []string) error {
	return errNotImpl
}

func (me *StubDB) FindPostsBeforeDate(date *time.Time, space string) ([]*db.Post, error) {
	return []*db.Post{}, errNotImpl
}

func (me *StubDB) FindUserForKey(username string, key string) (*db.User, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindUserByPubkey(key string) (*db.User, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindUser(userID string) (*db.User, error) {
	return nil, errNotImpl
}

func (me *StubDB) ValidateName(name string) (bool, error) {
	return false, errNotImpl
}

func (me *StubDB) FindUserByName(name string) (*db.User, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindUserForNameAndKey(name string, key string) (*db.User, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindUserForToken(token string) (*db.User, error) {
	return nil, errNotImpl
}

func (me *StubDB) SetUserName(userID string, name string) error {
	return errNotImpl
}

func (me *StubDB) FindPostWithFilename(filename string, persona_id string, space string) (*db.Post, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindPostWithSlug(slug string, user_id string, space string) (*db.Post, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindPost(postID string) (*db.Post, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindPostsForFeed(page *db.Pager, space string) (*db.Paginate[*db.Post], error) {
	return &db.Paginate[*db.Post]{}, errNotImpl
}

func (me *StubDB) FindAllUpdatedPosts(page *db.Pager, space string) (*db.Paginate[*db.Post], error) {
	return &db.Paginate[*db.Post]{}, errNotImpl
}

func (me *StubDB) InsertPost(post *db.Post) (*db.Post, error) {
	return nil, errNotImpl
}

func (me *StubDB) UpdatePost(post *db.Post) (*db.Post, error) {
	return nil, errNotImpl
}

func (me *StubDB) RemovePosts(postIDs []string) error {
	return errNotImpl
}

func (me *StubDB) FindPostsForUser(page *db.Pager, userID string, space string) (*db.Paginate[*db.Post], error) {
	return &db.Paginate[*db.Post]{}, errNotImpl
}

func (me *StubDB) FindAllPostsForUser(userID string, space string) ([]*db.Post, error) {
	return []*db.Post{}, errNotImpl
}

func (me *StubDB) FindPosts() ([]*db.Post, error) {
	return []*db.Post{}, errNotImpl
}

func (me *StubDB) FindExpiredPosts(space string) ([]*db.Post, error) {
	return []*db.Post{}, errNotImpl
}

func (me *StubDB) FindUpdatedPostsForUser(userID string, space string) ([]*db.Post, error) {
	return []*db.Post{}, errNotImpl
}

func (me *StubDB) Close() error {
	return errNotImpl
}

func (me *StubDB) InsertVisit(view *db.AnalyticsVisits) error {
	return errNotImpl
}

func (me *StubDB) VisitSummary(opts *db.SummaryOpts) (*db.SummaryVisits, error) {
	return &db.SummaryVisits{}, errNotImpl
}

func (me *StubDB) FindVisitSiteList(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	return []*db.VisitUrl{}, errNotImpl
}

func (me *StubDB) FindUsers() ([]*db.User, error) {
	return []*db.User{}, errNotImpl
}

func (me *StubDB) ReplaceTagsForPost(tags []string, postID string) error {
	return errNotImpl
}

func (me *StubDB) ReplaceAliasesForPost(aliases []string, postID string) error {
	return errNotImpl
}

func (me *StubDB) FindUserPostsByTag(page *db.Pager, tag, userID, space string) (*db.Paginate[*db.Post], error) {
	return &db.Paginate[*db.Post]{}, errNotImpl
}

func (me *StubDB) FindPostsByTag(pager *db.Pager, tag, space string) (*db.Paginate[*db.Post], error) {
	return &db.Paginate[*db.Post]{}, errNotImpl
}

func (me *StubDB) FindPopularTags(space string) ([]string, error) {
	return []string{}, errNotImpl
}

func (me *StubDB) FindTagsForPost(postID string) ([]string, error) {
	return []string{}, errNotImpl
}

func (me *StubDB) FindFeature(userID string, feature string) (*db.FeatureFlag, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindFeaturesForUser(userID string) ([]*db.FeatureFlag, error) {
	return []*db.FeatureFlag{}, errNotImpl
}

func (me *StubDB) HasFeatureForUser(userID string, feature string) bool {
	return false
}

func (me *StubDB) FindTotalSizeForUser(userID string) (int, error) {
	return 0, errNotImpl
}

func (me *StubDB) InsertFeedItems(postID string, items []*db.FeedItem) error {
	return errNotImpl
}

func (me *StubDB) FindFeedItemsByPostID(postID string) ([]*db.FeedItem, error) {
	return []*db.FeedItem{}, errNotImpl
}

func (me *StubDB) UpsertProject(userID, name, projectDir string) (*db.Project, error) {
	return nil, errNotImpl
}

func (me *StubDB) InsertProject(userID, name, projectDir string) (string, error) {
	return "", errNotImpl
}

func (me *StubDB) UpdateProject(userID, name string) error {
	return errNotImpl
}

func (me *StubDB) FindProjectByName(userID, name string) (*db.Project, error) {
	return &db.Project{}, errNotImpl
}

func (me *StubDB) InsertToken(userID, name string) (string, error) {
	return "", errNotImpl
}

func (me *StubDB) UpsertToken(userID, name string) (string, error) {
	return "", errNotImpl
}

func (me *StubDB) FindTokenByName(userID, name string) (string, error) {
	return "", errNotImpl
}

func (me *StubDB) RemoveToken(tokenID string) error {
	return errNotImpl
}

func (me *StubDB) FindTokensForUser(userID string) ([]*db.Token, error) {
	return []*db.Token{}, errNotImpl
}

func (me *StubDB) InsertFeature(userID, name string, expiresAt time.Time) (*db.FeatureFlag, error) {
	return nil, errNotImpl
}

func (me *StubDB) RemoveFeature(userID string, name string) error {
	return errNotImpl
}

func (me *StubDB) AddPicoPlusUser(username, email, paymentType, txId string) error {
	return errNotImpl
}

func (me *StubDB) FindTagsForUser(userID string, tag string) ([]string, error) {
	return []string{}, errNotImpl
}

func (me *StubDB) FindUserStats(userID string) (*db.UserStats, error) {
	return nil, errNotImpl
}

func (me *StubDB) InsertTunsEventLog(log *db.TunsEventLog) error {
	return errNotImpl
}

func (me *StubDB) FindTunsEventLogsByAddr(userID, addr string) ([]*db.TunsEventLog, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindTunsEventLogs(userID string) ([]*db.TunsEventLog, error) {
	return nil, errNotImpl
}

func (me *StubDB) VisitUrlNotFound(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	return nil, errNotImpl
}

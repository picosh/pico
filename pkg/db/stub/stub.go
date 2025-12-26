package stub

import (
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

func (me *StubDB) UpdatePublicKey(pubkeyID, name string) (*db.PublicKey, error) {
	return nil, errNotImpl
}

func (me *StubDB) InsertPublicKey(userID, key, name string) error {
	return errNotImpl
}

func (me *StubDB) FindKeysByUser(user *db.User) ([]*db.PublicKey, error) {
	return []*db.PublicKey{}, errNotImpl
}

func (me *StubDB) RemoveKeys(keyIDs []string) error {
	return errNotImpl
}

func (me *StubDB) FindUserByKey(username string, key string) (*db.User, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindUserByPubkey(key string) (*db.User, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindUser(userID string) (*db.User, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindUserByName(name string) (*db.User, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindUserByToken(token string) (*db.User, error) {
	return nil, errNotImpl
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

func (me *StubDB) FindPostsByFeed(page *db.Pager, space string) (*db.Paginate[*db.Post], error) {
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

func (me *StubDB) FindPostsByUser(page *db.Pager, userID string, space string) (*db.Paginate[*db.Post], error) {
	return &db.Paginate[*db.Post]{}, errNotImpl
}

func (me *StubDB) FindAllPostsByUser(userID string, space string) ([]*db.Post, error) {
	return []*db.Post{}, errNotImpl
}

func (me *StubDB) FindPosts() ([]*db.Post, error) {
	return []*db.Post{}, errNotImpl
}

func (me *StubDB) FindExpiredPosts(space string) ([]*db.Post, error) {
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

func (me *StubDB) ReplaceTagsByPost(tags []string, postID string) error {
	return errNotImpl
}

func (me *StubDB) ReplaceAliasesByPost(aliases []string, postID string) error {
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

func (me *StubDB) FindFeature(userID string, feature string) (*db.FeatureFlag, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindFeaturesByUser(userID string) ([]*db.FeatureFlag, error) {
	return []*db.FeatureFlag{}, errNotImpl
}

func (me *StubDB) HasFeatureByUser(userID string, feature string) bool {
	return false
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

func (me *StubDB) FindProjectByName(userID, name string) (*db.Project, error) {
	return &db.Project{}, errNotImpl
}

func (me *StubDB) InsertToken(userID, name string) (string, error) {
	return "", errNotImpl
}

func (me *StubDB) UpsertToken(userID, name string) (string, error) {
	return "", errNotImpl
}

func (me *StubDB) RemoveToken(tokenID string) error {
	return errNotImpl
}

func (me *StubDB) FindTokensByUser(userID string) ([]*db.Token, error) {
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

func (me *StubDB) FindUsersWithPost(space string) ([]*db.User, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindAccessLogs(userID string, fromDate *time.Time) ([]*db.AccessLog, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindAccessLogsByPubkey(pubkey string, fromDate *time.Time) ([]*db.AccessLog, error) {
	return nil, errNotImpl
}

func (me *StubDB) FindPubkeysInAccessLogs(userID string) ([]string, error) {
	return []string{}, errNotImpl
}

func (me *StubDB) InsertAccessLog(log *db.AccessLog) error {
	return errNotImpl
}

func (me *StubDB) UpsertPipeMonitor(userID, topic string, dur time.Duration, winEnd *time.Time) error {
	return errNotImpl
}

func (me *StubDB) UpdatePipeMonitorLastPing(userID, topic string, lastPing *time.Time) error {
	return errNotImpl
}

func (me *StubDB) RemovePipeMonitor(userID, topic string) error {
	return errNotImpl
}

func (me *StubDB) FindPipeMonitorByTopic(userID, topic string) (*db.PipeMonitor, error) {
	return nil, errNotImpl
}

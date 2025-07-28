package links

import (
	"database/sql"
	"fmt"
	"log/slog"
	"math/rand"
	"net/url"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared"
)

type DB interface {
	Close() error

	FindUser(userID string) (*db.User, error)
	FindUserByPubkey(pubkey string) (*db.User, error)
	FindUserByName(name string) (*db.User, error)

	FindFeature(userID string, name string) (*db.FeatureFlag, error)

	CreateLink(*LinkTree) (*LinkTree, error)
	EditLink(*LinkTree, *db.User) error
	DeleteLink(linkID string, user *db.User) error
	FindLink(linkID string) (*LinkTree, error)
	FindLinkByShortID(shortID string) (*LinkTree, error)

	Vote(linkID string, userID string) (int, error)

	FindTopics(pager *db.Pager) (db.Paginate[*LinkTree], error)
	FindLinks(path string, pager *db.Pager) (db.Paginate[*LinkTree], error)
	// FindComments(path string, pager *db.Pager) (db.Paginate[*LinkTree], error)
	// FindHotPosts(linkID string, pager *db.Pager) (db.Paginate[*LinkTree], error)
	// FindNewPosts(linkID string, pager *db.Pager) (db.Paginate[*LinkTree], error)

	FindMod(modID string) (*Mod, error)
	CreateMod(*Mod, *db.User) (*Mod, error)
	EditMod(*Mod, *db.User) error
	DeleteMod(modID string, user *db.User) error
	FindModsByLinkID(linkID string) ([]*Mod, error)
}

type LinkTree struct {
	// PK and FKs
	ID      string `db:"id"`
	UserID  string `db:"user_id"`
	ShortID string `db:"short_id"`
	// ltree extension
	Path string `db:"path"`
	// data
	Text   string         `db:"text"`
	URL    sql.NullString `db:"url"`
	Title  sql.NullString `db:"title"`
	ImgURL sql.NullString `db:"img_url"`
	Perm   string         `db:"perm"` // write, read, hide
	// timestamps
	CreatedAt *time.Time `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`
	// computed
	Votes    int    `db:"votes"`
	Score    int    `db:"score"`
	Username string `db:"username"`
}

type Mod struct {
	// PK and FKs
	ID     string `db:"id"`
	LinkID string `db:"link_id"`
	UserID string `db:"user_id"`
	// data
	Perm   string `db:"perm"` // apex, writer, read
	Reason string `db:"reason"`
	// timestamps
	CreatedAt *time.Time `db:"created_at"`
}

type Vote struct {
	// PK and FKs
	ID     string `db:"id"`
	UserID string `db:"user_id"`
	LinkID string `db:"link_id"`
	// timestamps
	CreatedAt *time.Time `db:"created_at"`
}

type PsqlDB struct {
	Logger *slog.Logger
	Db     *sqlx.DB
}

var _ DB = (*PsqlDB)(nil)

func NewDB(databaseUrl string, logger *slog.Logger) (*PsqlDB, error) {
	var err error
	d := &PsqlDB{
		Logger: logger,
	}
	d.Logger.Info("connecting to postgres", "databaseUrl", databaseUrl)

	db, err := sqlx.Connect("postgres", databaseUrl)
	if err != nil {
		return nil, err
	}

	d.Db = db
	return d, nil
}

func (me *PsqlDB) Close() error {
	return me.Db.Close()
}

func (me *PsqlDB) FindUserByPubkey(key string) (*db.User, error) {
	pk := []db.PublicKey{}
	err := me.Db.Select(&pk, "SELECT * FROM public_keys WHERE public_key=$1", key)
	if err != nil {
		return nil, err
	}
	if len(pk) == 0 {
		return nil, fmt.Errorf("pubkey not found in our database: [%s]", key)
	}
	// When we run PublicKeyForKey and there are multiple public keys returned from the database
	// that should mean that we don't have the correct username for this public key.
	// When that happens we need to reject the authentication and ask the user to provide the correct
	// username when using ssh.  So instead of `ssh <domain>` it should be `ssh user@<domain>`
	if len(pk) > 1 {
		return nil, &db.ErrMultiplePublicKeys{}
	}

	return me.FindUser(pk[0].UserID)
}

func (me *PsqlDB) FindUser(userID string) (*db.User, error) {
	user := db.User{}
	err := me.Db.Get(&user, "SELECT * FROM app_users WHERE id=$1", userID)
	return &user, err
}

func (me *PsqlDB) FindUserByName(name string) (*db.User, error) {
	user := db.User{}
	err := me.Db.Get(&user, "SELECT * FROM app_users WHERE name=$1", name)
	return &user, err
}

func (me *PsqlDB) FindFeature(userID string, name string) (*db.FeatureFlag, error) {
	ff := db.FeatureFlag{}
	err := me.Db.Get(&ff, "SELECT * FROM feature_flags WHERE user_id=$1 AND name=$2 ORDER BY expires_at DESC LIMIT 1", userID, name)
	return &ff, err
}

func (me *PsqlDB) FindLink(linkID string) (*LinkTree, error) {
	link := LinkTree{}
	query := `SELECT
		lt.id, lt.user_id, lt.short_id, lt.path, lt.text, lt.url,
		lt.title, lt.img_url, lt.perm, lt.created_at, lt.updated_at,
		u.name as username,
		coalesce((select sum(1) from votes where link_id=lt.id), 0) as votes
	FROM link_tree as lt
	LEFT JOIN app_users as u ON u.id=lt.user_id
	WHERE lt.id=$1`
	err := me.Db.Get(&link, query, linkID)
	return &link, err
}

func (me *PsqlDB) FindLinkByShortID(shortID string) (*LinkTree, error) {
	link := LinkTree{}
	query := `SELECT
		lt.id, lt.user_id, lt.short_id, lt.path, lt.text, lt.url,
		lt.title, lt.img_url, lt.perm, lt.created_at, lt.updated_at,
		u.name as username,
		coalesce((select sum(1) from votes where link_id=lt.id), 0) as votes
	FROM link_tree as lt
	LEFT JOIN app_users as u ON u.id=lt.user_id
	WHERE lt.short_id=$1`
	err := me.Db.Get(&link, query, shortID)
	return &link, err
}

func (me *PsqlDB) FindTopics(pager *db.Pager) (db.Paginate[*LinkTree], error) {
	links := []*LinkTree{}
	query := `SELECT
		lt.id, lt.user_id, lt.short_id, lt.path, lt.text, lt.url,
		lt.title, lt.img_url, lt.perm, lt.created_at, lt.updated_at,
		u.name as username,
		coalesce((select sum(1) from votes where link_id=lt.id), 0) as votes
	FROM link_tree as lt
	LEFT JOIN app_users as u ON u.id=lt.user_id
	WHERE NLEVEL(path)=2 ORDER BY votes DESC, lt.created_at DESC`
	err := me.Db.Select(&links, query)
	page := db.Paginate[*LinkTree]{
		Data:  links,
		Total: len(links),
	}
	return page, err
}

func (me *PsqlDB) FindLinks(path string, pager *db.Pager) (db.Paginate[*LinkTree], error) {
	links := []*LinkTree{}
	err := me.Db.Select(
		&links,
		`SELECT
			lt.id, lt.user_id, lt.short_id, lt.path, lt.text, lt.url,
			lt.title, lt.img_url, lt.perm, lt.created_at, lt.updated_at,
			u.name as username,
			coalesce((select sum(1) from votes where link_id=lt.id), 0) as votes
		FROM link_tree as lt
		LEFT JOIN app_users as u ON u.id=lt.user_id
		WHERE path <@ $1
		ORDER BY lt.path ASC, votes DESC, lt.created_at DESC`,
		path,
	)
	page := db.Paginate[*LinkTree]{
		Data:  links,
		Total: len(links),
	}
	return page, err
}

func urlFromText(txt string) (string, error) {
	parsed := shared.ListParseText(txt)
	if len(parsed.Items) == 0 {
		return "", fmt.Errorf("no parsed items")
	}
	rurl := parsed.Items[0]
	if !rurl.IsURL {
		return "", fmt.Errorf("no url detected")
	}
	furl, err := url.Parse(rurl.Value)
	if err != nil {
		return "", err
	}
	return furl.String(), nil
}

func (me *PsqlDB) CreateLink(link *LinkTree) (*LinkTree, error) {
	shortID, err := me.generateShortID(0)
	if err != nil {
		return nil, err
	}
	url, err := urlFromText(link.Text)
	if err != nil {
		me.Logger.Info("no url detected, assuming text post")
	}
	row := me.Db.QueryRow(
		"INSERT INTO link_tree (user_id, short_id, path, text, url) VALUES ($1, $2, $3, $4, $5) RETURNING id",
		link.UserID,
		shortID,
		link.Path,
		link.Text,
		url,
	)
	var linkID string
	err = row.Scan(&linkID)
	if err != nil {
		return nil, err
	}

	return me.FindLink(linkID)
}

func (me *PsqlDB) EditLink(link *LinkTree, user *db.User) error {
	_, err := me.Db.Exec(
		"UPDATE link_tree SET text=$1, updated_at=$2 WHERE id=$3",
		link.Text, time.Now(), link.ID,
	)
	return err
}

func (me *PsqlDB) DeleteLink(linkID string, user *db.User) error {
	_, err := me.Db.Exec(
		"DELETE FROM link_tree WHERE id=$1",
		linkID,
	)
	return err
}

func (me *PsqlDB) hasVote(linkID, userID string) bool {
	vote := Vote{}
	err := me.Db.Get(&vote, "SELECT * FROM votes WHERE link_id=$1 AND user_id=$2", linkID, userID)
	return err == nil
}

func (me *PsqlDB) Vote(linkID, userID string) (int, error) {
	var err error
	if me.hasVote(linkID, userID) {
		_, err = me.Db.Exec(
			"DELETE FROM votes WHERE link_id=$1 AND user_id=$2",
			linkID,
			userID,
		)
	} else {
		_, err = me.Db.Exec(
			"INSERT INTO votes (link_id, user_id) VALUES ($1, $2)",
			linkID,
			userID,
		)
	}

	if err != nil {
		return 0, err
	}

	link, err := me.FindLink(linkID)
	if err != nil {
		return 0, err
	}

	return link.Votes, err
}

/* func (me *PsqlDB) FindHotPosts(linkID string, pager *db.Pager) (db.Paginate[*LinkTree], error) {
	links := []*LinkTree{}
	query := `SELECT
		lt.id, lt.user_id, lt.short_id, lt.path, lt.text, lt.url,
		lt.title, lt.img_url, lt.access, lt.created_at, lt.updated_at,
		sum(1) as votes
	FROM link_tree as lt
	LEFT JOIN votes as v ON v.link_id=lt.id
	GROUP BY v.link_id
	WHERE NLEVEL(path)=3
	ORDER BY votes`
	err := me.Db.Select(&links, query)
	page := db.Paginate[*LinkTree]{
		Data:  links,
		Total: len(links),
	}
	return page, err
}

func (me *PsqlDB) FindNewPosts(linkID string, pager *db.Pager) (db.Paginate[*LinkTree], error) {
	links := []*LinkTree{}
	err := me.Db.Select(&links, "SELECT * FROM link_tree WHERE NLEVEL(path)=3 ORDER BY created_at DESC")
	page := db.Paginate[*LinkTree]{
		Data:  links,
		Total: len(links),
	}
	return page, err
} */

func (me *PsqlDB) FindModsByLinkID(linkID string) ([]*Mod, error) {
	mods := []*Mod{}
	err := me.Db.Select(&mods, "SELECT * FROM mods WHERE link_id=$1", linkID)
	return mods, err
}

func (me *PsqlDB) FindMod(modID string) (*Mod, error) {
	mod := Mod{}
	err := me.Db.Get(&mod, "SELECT * FROM mods WHERE id=$1", modID)
	return &mod, err
}

func (me *PsqlDB) CreateMod(mod *Mod, user *db.User) (*Mod, error) {
	row := me.Db.QueryRow(
		"INSERT INTO mods (link_id, user_id, perm, reason) VALUES ($1, $2, $3, $4) RETURNING id",
		mod.LinkID,
		mod.UserID,
		mod.Perm,
		mod.Reason,
	)
	var modID string
	err := row.Scan(&modID)
	if err != nil {
		return nil, err
	}
	return me.FindMod(modID)
}

func (me *PsqlDB) EditMod(mod *Mod, user *db.User) error {
	_, err := me.Db.Exec(
		"UPDATE mods SET perm=$1, reason=$2, updated_at=$3 WHERE id=$4",
		mod.Perm, mod.Reason, time.Now(), mod.ID,
	)
	return err
}

func (me *PsqlDB) DeleteMod(modID string, user *db.User) error {
	_, err := me.Db.Exec(
		"DELETE FROM mods WHERE id=$1",
		modID,
	)
	return err
}

func generateRandomString(length int) string {
	chars := []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789")
	str := make([]rune, length)
	for i := range str {
		str[i] = chars[rand.Intn(len(chars))]
	}
	return string(str)
}

func (me *PsqlDB) generateShortID(attempt int) (string, error) {
	if attempt >= 15 {
		return "", fmt.Errorf("could not generate short id for link")
	}
	shortID := generateRandomString(6)
	link, _ := me.FindLinkByShortID(shortID)
	if link == nil {
		return me.generateShortID(attempt + 1)
	}
	return shortID, nil
}

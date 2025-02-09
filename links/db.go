package links

import (
	"fmt"
	"log/slog"
	"math/rand"
	"net/url"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
)

type DB interface {
	Close() error

	FindUser(userID string) (*db.User, error)
	FindUserByPubkey(pubkey string) (*db.User, error)

	CreateLink(*LinkTree) (*LinkTree, error)
	EditLink(*LinkTree, *db.User) error
	DeleteLink(linkID string, user *db.User) error
	FindLink(linkID string) (*LinkTree, error)
	FindLinkByShortID(shortID string) (*LinkTree, error)

	Vote(linkID string, userID string) (int, error)

	FindTopics(pager *db.Pager) (db.Paginate[*LinkTree], error)
	FindComments(linkID string, pager *db.Pager) (db.Paginate[*LinkTree], error)
	FindHotPosts(linkID string, pager *db.Pager) (db.Paginate[*LinkTree], error)
	FindNewPosts(linkID string, pager *db.Pager) (db.Paginate[*LinkTree], error)

	FindMod(modID string) (*Mod, error)
	CreateMod(*Mod, *db.User) (*Mod, error)
	EditMod(*Mod, *db.User) error
	DeleteMod(modID string, user *db.User) error
	FindModsByLinkID(linkID string) ([]*Mod, error)
}

type LinkTree struct {
	// PK and FKs
	ID      string
	UserID  string
	ShortID string
	// ltree extension
	Path string
	// data
	Text   string
	URL    string
	Title  string
	ImgURL string
	Access string // write, read, hide
	// timestamps
	CreatedAt *time.Time
	UpdatedAt *time.Time
	// computed
	Votes int
	Score int
}

type Mod struct {
	// PK and FKs
	ID     string
	LinkID string
	UserID string
	// data
	Perm   string // apex, writer, read
	Reason string
	// timestamps
	CreatedAt *time.Time
}

type Vote struct {
	// PK and FKs
	ID     string
	UserID string
	LinkID string
	// timestamps
	CreatedAt *time.Time
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
	err := me.Db.Select(&pk, "SELECT * FROM public_keys WHERE public_key=?", key)
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
	err := me.Db.Get(&user, "SELECT * FROM app_users WHERE id=?", userID)
	return &user, err
}

func (me *PsqlDB) FindLink(linkID string) (*LinkTree, error) {
	link := LinkTree{}
	err := me.Db.Get(&link, "SELECT * FROM link_tree WHERE id=?", linkID)
	return &link, err
}

func (me *PsqlDB) FindLinkByShortID(shortID string) (*LinkTree, error) {
	link := LinkTree{}
	err := me.Db.Get(&link, "SELECT * FROM link_tree WHERE short_id=?", shortID)
	return &link, err
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
		"INSERT INTO link_tree (user_id, short_id, path, text, url) VALUES (?, ?, ?, ?, ?) RETURNING id",
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
		"UPDATE link_tree SET text=?, updated_at=? WHERE id=?",
		link.Text, time.Now(), link.ID,
	)
	return err
}

func (me *PsqlDB) DeleteLink(linkID string, user *db.User) error {
	_, err := me.Db.Exec(
		"DELETE FROM link_tree WHERE id=?",
		linkID,
	)
	return err
}

func (me *PsqlDB) hasVote(linkID, userID string) bool {
	vote := Vote{}
	err := me.Db.Get(&vote, "SELECT * FROM votes WHERE link_id=? AND user_id=?", linkID, userID)
	return err == nil
}

func (me *PsqlDB) Vote(linkID, userID string) (int, error) {
	var err error
	if me.hasVote(linkID, userID) {
		_, err = me.Db.Exec(
			"INSERT INTO votes (link_id, user_id) VALUES (?, ?)",
			linkID,
			userID,
		)
	} else {
		_, err = me.Db.Exec(
			"DELETE FROM votes WHERE link_id=? AND user_id=?",
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

func (me *PsqlDB) FindTopics(pager *db.Pager) (db.Paginate[*LinkTree], error) {
	links := []*LinkTree{}
	err := me.Db.Select(&links, "SELECT * FROM link_tree WHERE NLEVEL(path)=2 ORDER BY text DESC")
	page := db.Paginate[*LinkTree]{
		Data:  links,
		Total: len(links),
	}
	return page, err
}

func (me *PsqlDB) FindComments(path string, pager *db.Pager) (db.Paginate[*LinkTree], error) {
	links := []*LinkTree{}
	err := me.Db.Select(
		&links,
		"SELECT * FROM link_tree WHERE path <@ ? AND path != ? ORDER BY created_at DESC",
		path, path,
	)
	page := db.Paginate[*LinkTree]{
		Data:  links,
		Total: len(links),
	}
	return page, err
}

func (me *PsqlDB) FindHotPosts(linkID string, pager *db.Pager) (db.Paginate[*LinkTree], error) {
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
}

func (me *PsqlDB) FindModsByLinkID(linkID string) ([]*Mod, error) {
	mods := []*Mod{}
	err := me.Db.Select(&mods, "SELECT * FROM mods WHERE link_id=?", linkID)
	return mods, err
}

func (me *PsqlDB) FindMod(modID string) (*Mod, error) {
	mod := Mod{}
	err := me.Db.Get(&mod, "SELECT * FROM mods WHERE id=?", modID)
	return &mod, err
}

func (me *PsqlDB) CreateMod(mod *Mod, user *db.User) (*Mod, error) {
	row := me.Db.QueryRow(
		"INSERT INTO mods (link_id, user_id, perm, reason) VALUES (?, ?, ?, ?) RETURNING id",
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
		"UPDATE mods SET perm=?, reason=?, updated_at=? WHERE id=?",
		mod.Perm, mod.Reason, time.Now(), mod.ID,
	)
	return err
}

func (me *PsqlDB) DeleteMod(modID string, user *db.User) error {
	_, err := me.Db.Exec(
		"DELETE FROM mods WHERE id=?",
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
	shortID := generateRandomString(5)
	link, _ := me.FindLinkByShortID(shortID)
	if link != nil {
		return me.generateShortID(attempt + 1)
	}
	return shortID, nil
}

package core

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/golem-mc/golem/plugin"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

// SQLDialect identifies the SQL database engine.
type SQLDialect string

const (
	DialectSQLite     SQLDialect = "SQLite"
	DialectMySQL      SQLDialect = "MySQL"
	DialectMariaDB    SQLDialect = "MariaDB"
	DialectPostgreSQL SQLDialect = "PostgreSQL"
)

// SQLStorage implements Storage over SQLite, MySQL, MariaDB, or PostgreSQL
// using LuckPerms's exact 6-table schema.
type SQLStorage struct {
	lp      *LuckPerms
	dialect SQLDialect
	prefix  string
	dbPath  string // for SQLite
	db      *sql.DB
	dsn     string
}

// NewSQLStorage constructs a SQLStorage backend for the given dialect.
func NewSQLStorage(lp *LuckPerms, dialect SQLDialect) *SQLStorage {
	cfg := lp.Config().Data
	prefix := cfg.TablePrefix
	if prefix == "" {
		prefix = "luckperms_"
	}
	return &SQLStorage{
		lp:      lp,
		dialect: dialect,
		prefix:  prefix,
		dbPath:  filepath.Join(lp.DataDir(), "luckperms-sqlite.db"),
	}
}

func (s *SQLStorage) Name() string        { return string(s.dialect) }
func (s *SQLStorage) Dialect() SQLDialect { return s.dialect }
func (s *SQLStorage) TablePrefix() string { return s.prefix }
func (s *SQLStorage) DB() *sql.DB         { return s.db }
func (s *SQLStorage) DSN() string         { return s.dsn }
func (s *SQLStorage) DataDir() string     { return "" }

func (s *SQLStorage) q(query string) string {
	query = strings.ReplaceAll(query, "{prefix}", s.prefix)
	if s.dialect == DialectPostgreSQL {
		// Replace backticks with double quotes and ? with $1, $2, ...
		query = strings.ReplaceAll(query, "`", `"`)
		var sb strings.Builder
		argN := 1
		for i := 0; i < len(query); i++ {
			if query[i] == '?' {
				fmt.Fprintf(&sb, "$%d", argN)
				argN++
			} else {
				sb.WriteByte(query[i])
			}
		}
		return sb.String()
	}
	return query
}

func (s *SQLStorage) Init() error {
	cfg := s.lp.Config().Data
	var driver, dsn string

	switch s.dialect {
	case DialectSQLite:
		driver = "sqlite"
		dsn = s.dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	case DialectMySQL, DialectMariaDB:
		driver = "mysql"
		addr := defaultHostPort(cfg.Address, "3306")
		params := url.Values{}
		params.Set("parseTime", "true")
		params.Set("charset", "utf8mb4")
		for k, v := range cfg.Properties {
			params.Set(k, v)
		}
		dsn = fmt.Sprintf("%s:%s@tcp(%s)/%s?%s", cfg.Username, cfg.Password, addr, cfg.Database, params.Encode())
	case DialectPostgreSQL:
		driver = "postgres"
		addr := defaultHostPort(cfg.Address, "5432")
		host, port, _ := net.SplitHostPort(addr)
		u := &url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(cfg.Username, cfg.Password),
			Host:   net.JoinHostPort(host, port),
			Path:   "/" + cfg.Database,
		}
		q := url.Values{}
		if _, ok := cfg.Properties["sslmode"]; !ok {
			q.Set("sslmode", "disable")
		}
		for k, v := range cfg.Properties {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		dsn = u.String()
	default:
		return fmt.Errorf("unsupported SQL dialect: %s", s.dialect)
	}

	s.dsn = dsn
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return err
	}
	if s.dialect == DialectSQLite {
		db.SetMaxOpenConns(1)
	} else {
		if cfg.MaxPoolSize > 0 {
			db.SetMaxOpenConns(cfg.MaxPoolSize)
		}
		if cfg.MinIdle > 0 {
			db.SetMaxIdleConns(cfg.MinIdle)
		}
		if cfg.MaxLifetimeMillis > 0 {
			db.SetConnMaxLifetime(time.Duration(cfg.MaxLifetimeMillis) * time.Millisecond)
		}
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return err
	}
	s.db = db
	return s.applySchema()
}

func (s *SQLStorage) applySchema() error {
	var stmts []string
	switch s.dialect {
	case DialectSQLite:
		stmts = []string{
			"CREATE TABLE IF NOT EXISTS `{prefix}user_permissions` (`id` INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, `uuid` VARCHAR(36) NOT NULL, `permission` VARCHAR(200) NOT NULL, `value` BOOL NOT NULL, `server` VARCHAR(36) NOT NULL, `world` VARCHAR(64) NOT NULL, `expiry` BIGINT NOT NULL, `contexts` VARCHAR(200) NOT NULL)",
			"CREATE INDEX IF NOT EXISTS `{prefix}user_permissions_uuid` ON `{prefix}user_permissions` (`uuid`)",
			"CREATE TABLE IF NOT EXISTS `{prefix}group_permissions` (`id` INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, `name` VARCHAR(36) NOT NULL, `permission` VARCHAR(200) NOT NULL, `value` BOOL NOT NULL, `server` VARCHAR(36) NOT NULL, `world` VARCHAR(64) NOT NULL, `expiry` BIGINT NOT NULL, `contexts` VARCHAR(200) NOT NULL)",
			"CREATE INDEX IF NOT EXISTS `{prefix}group_permissions_name` ON `{prefix}group_permissions` (`name`)",
			"CREATE TABLE IF NOT EXISTS `{prefix}players` (`uuid` VARCHAR(36) NOT NULL PRIMARY KEY, `username` VARCHAR(16) NOT NULL, `primary_group` VARCHAR(36) NOT NULL)",
			"CREATE INDEX IF NOT EXISTS `{prefix}players_username` ON `{prefix}players` (`username`)",
			"CREATE TABLE IF NOT EXISTS `{prefix}groups` (`name` VARCHAR(36) NOT NULL PRIMARY KEY)",
			"CREATE TABLE IF NOT EXISTS `{prefix}actions` (`id` INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, `time` BIGINT NOT NULL, `actor_uuid` VARCHAR(36) NOT NULL, `actor_name` VARCHAR(100) NOT NULL, `type` CHAR(1) NOT NULL, `acted_uuid` VARCHAR(36) NOT NULL, `acted_name` VARCHAR(36) NOT NULL, `action` VARCHAR(300) NOT NULL)",
			"CREATE TABLE IF NOT EXISTS `{prefix}tracks` (`name` VARCHAR(36) NOT NULL PRIMARY KEY, `groups` TEXT NOT NULL)",
		}
	case DialectPostgreSQL:
		stmts = []string{
			`CREATE TABLE IF NOT EXISTS "{prefix}user_permissions" ("id" SERIAL PRIMARY KEY NOT NULL, "uuid" VARCHAR(36) NOT NULL, "permission" VARCHAR(200) NOT NULL, "value" BOOL NOT NULL, "server" VARCHAR(36) NOT NULL, "world" VARCHAR(64) NOT NULL, "expiry" BIGINT NOT NULL, "contexts" VARCHAR(200) NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS "{prefix}user_permissions_uuid" ON "{prefix}user_permissions" ("uuid")`,
			`CREATE TABLE IF NOT EXISTS "{prefix}group_permissions" ("id" SERIAL PRIMARY KEY NOT NULL, "name" VARCHAR(36) NOT NULL, "permission" VARCHAR(200) NOT NULL, "value" BOOL NOT NULL, "server" VARCHAR(36) NOT NULL, "world" VARCHAR(64) NOT NULL, "expiry" BIGINT NOT NULL, "contexts" VARCHAR(200) NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS "{prefix}group_permissions_name" ON "{prefix}group_permissions" ("name")`,
			`CREATE TABLE IF NOT EXISTS "{prefix}players" ("uuid" VARCHAR(36) PRIMARY KEY NOT NULL, "username" VARCHAR(16) NOT NULL, "primary_group" VARCHAR(36) NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS "{prefix}players_username" ON "{prefix}players" ("username")`,
			`CREATE TABLE IF NOT EXISTS "{prefix}groups" ("name" VARCHAR(36) PRIMARY KEY NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS "{prefix}actions" ("id" SERIAL PRIMARY KEY NOT NULL, "time" BIGINT NOT NULL, "actor_uuid" VARCHAR(36) NOT NULL, "actor_name" VARCHAR(100) NOT NULL, "type" CHAR(1) NOT NULL, "acted_uuid" VARCHAR(36) NOT NULL, "acted_name" VARCHAR(36) NOT NULL, "action" VARCHAR(300) NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS "{prefix}tracks" ("name" VARCHAR(36) PRIMARY KEY NOT NULL, "groups" TEXT NOT NULL)`,
		}
	default: // MySQL / MariaDB
		stmts = []string{
			"CREATE TABLE IF NOT EXISTS `{prefix}user_permissions` (`id` INT AUTO_INCREMENT NOT NULL, `uuid` VARCHAR(36) NOT NULL, `permission` VARCHAR(200) NOT NULL, `value` BOOL NOT NULL, `server` VARCHAR(36) NOT NULL, `world` VARCHAR(64) NOT NULL, `expiry` BIGINT NOT NULL, `contexts` VARCHAR(200) NOT NULL, PRIMARY KEY (`id`), INDEX `{prefix}user_permissions_uuid` (`uuid`)) DEFAULT CHARSET = utf8mb4",
			"CREATE TABLE IF NOT EXISTS `{prefix}group_permissions` (`id` INT AUTO_INCREMENT NOT NULL, `name` VARCHAR(36) NOT NULL, `permission` VARCHAR(200) NOT NULL, `value` BOOL NOT NULL, `server` VARCHAR(36) NOT NULL, `world` VARCHAR(64) NOT NULL, `expiry` BIGINT NOT NULL, `contexts` VARCHAR(200) NOT NULL, PRIMARY KEY (`id`), INDEX `{prefix}group_permissions_name` (`name`)) DEFAULT CHARSET = utf8mb4",
			"CREATE TABLE IF NOT EXISTS `{prefix}players` (`uuid` VARCHAR(36) NOT NULL, `username` VARCHAR(16) NOT NULL, `primary_group` VARCHAR(36) NOT NULL, PRIMARY KEY (`uuid`), INDEX `{prefix}players_username` (`username`)) DEFAULT CHARSET = utf8mb4",
			"CREATE TABLE IF NOT EXISTS `{prefix}groups` (`name` VARCHAR(36) NOT NULL, PRIMARY KEY (`name`)) DEFAULT CHARSET = utf8mb4",
			"CREATE TABLE IF NOT EXISTS `{prefix}actions` (`id` INT AUTO_INCREMENT NOT NULL, `time` BIGINT NOT NULL, `actor_uuid` VARCHAR(36) NOT NULL, `actor_name` VARCHAR(100) NOT NULL, `type` CHAR(1) NOT NULL, `acted_uuid` VARCHAR(36) NOT NULL, `acted_name` VARCHAR(36) NOT NULL, `action` VARCHAR(300) NOT NULL, PRIMARY KEY (`id`)) DEFAULT CHARSET = utf8mb4",
			"CREATE TABLE IF NOT EXISTS `{prefix}tracks` (`name` VARCHAR(36) NOT NULL, `groups` TEXT NOT NULL, PRIMARY KEY (`name`)) DEFAULT CHARSET = utf8mb4",
		}
	}
	for _, st := range stmts {
		if _, err := s.db.Exec(s.q(st)); err != nil {
			return fmt.Errorf("apply schema (%s): %w", s.dialect, err)
		}
	}
	return nil
}

func (s *SQLStorage) Shutdown() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// --- Node <-> SQL row conversion (matches LuckPerms SqlStorage) ---

func encodeNodeSQL(n Node) (perm string, val bool, server, world string, expiry int64, contextsJSON string) {
	perm = n.Key
	val = n.Value
	server = GlobalContextValue
	world = GlobalContextValue
	m := n.Contexts.MutableCopy()
	if sv := n.Contexts.Values(ContextKeyServer); len(sv) == 1 {
		server = sv[0]
		m.Remove(ContextKeyServer, sv[0])
	}
	if wv := n.Contexts.Values(ContextKeyWorld); len(wv) == 1 {
		world = wv[0]
		m.Remove(ContextKeyWorld, wv[0])
	}
	if n.HasExpiry() {
		expiry = n.Expiry.Unix()
	}
	rem := m.Immutable()
	if rem.IsEmpty() {
		contextsJSON = "{}"
	} else {
		b, _ := json.Marshal(ContextSetToJSONMap(rem))
		contextsJSON = string(b)
	}
	return
}

func decodeNodeSQL(perm string, val bool, server, world string, expiry int64, contextsJSON string) Node {
	n := ParseNode(perm, val)
	if expiry > 0 {
		n = n.WithExpiry(time.Unix(expiry, 0).UTC())
	}
	m := NewMutableContextSet()
	if contextsJSON != "" && contextsJSON != "{}" {
		var cm map[string]any
		if json.Unmarshal([]byte(contextsJSON), &cm) == nil {
			m.AddAll(ContextSetFromJSONMap(cm))
		}
	}
	if server != "" && server != GlobalContextValue {
		m.Add(ContextKeyServer, server)
	}
	if world != "" && world != GlobalContextValue {
		m.Add(ContextKeyWorld, world)
	}
	if ctx := m.Immutable(); !ctx.IsEmpty() {
		n = n.WithContexts(ctx)
	}
	return n
}

// --- Users ---

func (s *SQLStorage) LoadUser(id plugin.UUID, username string) (*User, error) {
	u := s.lp.Users.GetOrMake(id, username)

	var storedName, primaryGroup string
	err := s.db.QueryRow(s.q("SELECT `username`, `primary_group` FROM `{prefix}players` WHERE `uuid`=? LIMIT 1"), id.String()).Scan(&storedName, &primaryGroup)
	hasPlayerRow := err == nil
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if hasPlayerRow {
		if storedName != "" && storedName != "null" && u.Username() == "" {
			u.SetUsername(storedName)
		}
		if primaryGroup != "" {
			u.SetStoredPrimaryGroup(primaryGroup)
		}
	} else {
		u.SetStoredPrimaryGroup(DefaultGroupName)
	}

	rows, err := s.db.Query(s.q("SELECT `permission`, `value`, `server`, `world`, `expiry`, `contexts` FROM `{prefix}user_permissions` WHERE `uuid`=?"), id.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var perm, srv, world, ctxJSON string
		var val bool
		var exp int64
		if err := rows.Scan(&perm, &val, &srv, &world, &exp, &ctxJSON); err != nil {
			return nil, err
		}
		nodes = append(nodes, decodeNodeSQL(perm, val, srv, world, exp, ctxJSON))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	u.Data(DataNormal).SetContent(nodes)
	u.GiveDefaultIfNeeded()
	u.InvalidateCache()

	if u.AuditTemporaryNodes() {
		_ = s.SaveUser(u)
	}
	return u, nil
}

func (s *SQLStorage) SaveUser(u *User) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(s.q("DELETE FROM `{prefix}user_permissions` WHERE `uuid`=?"), u.UUID().String()); err != nil {
		return err
	}
	for _, n := range u.NormalNodes() {
		perm, val, srv, world, exp, ctxJSON := encodeNodeSQL(n)
		if _, err := tx.Exec(s.q("INSERT INTO `{prefix}user_permissions` (`uuid`, `permission`, `value`, `server`, `world`, `expiry`, `contexts`) VALUES(?, ?, ?, ?, ?, ?, ?)"),
			u.UUID().String(), perm, val, srv, world, exp, ctxJSON); err != nil {
			return err
		}
	}

	name := strings.ToLower(u.Username())
	if name == "" {
		name = "null"
	}
	pg := u.StoredPrimaryGroup()
	if err := s.upsertPlayerTx(tx, u.UUID().String(), name, pg); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLStorage) upsertPlayerTx(tx *sql.Tx, idStr, username, primaryGroup string) error {
	res, err := tx.Exec(s.q("UPDATE `{prefix}players` SET `username`=?, `primary_group`=? WHERE `uuid`=?"), username, primaryGroup, idStr)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	_, err = tx.Exec(s.q("INSERT INTO `{prefix}players` (`uuid`, `username`, `primary_group`) VALUES(?, ?, ?)"), idStr, username, primaryGroup)
	return err
}

func (s *SQLStorage) GetUniqueUsers() ([]plugin.UUID, error) {
	rows, err := s.db.Query(s.q("SELECT DISTINCT `uuid` FROM `{prefix}user_permissions`"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []plugin.UUID
	for rows.Next() {
		var str string
		if err := rows.Scan(&str); err == nil {
			if id, err := uuid.Parse(str); err == nil {
				ids = append(ids, id)
			}
		}
	}
	return ids, rows.Err()
}

func (s *SQLStorage) SearchUserNodes(match func(Node) bool) ([]NodeSearchResult, error) {
	rows, err := s.db.Query(s.q("SELECT p.`uuid`, COALESCE(pl.`username`, p.`uuid`), p.`permission`, p.`value`, p.`server`, p.`world`, p.`expiry`, p.`contexts` FROM `{prefix}user_permissions` p LEFT JOIN `{prefix}players` pl ON p.`uuid` = pl.`uuid`"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeSearchResult
	for rows.Next() {
		var idStr, uname, perm, srv, world, ctxJSON string
		var val bool
		var exp int64
		if err := rows.Scan(&idStr, &uname, &perm, &val, &srv, &world, &exp, &ctxJSON); err != nil {
			return nil, err
		}
		n := decodeNodeSQL(perm, val, srv, world, exp, ctxJSON)
		if match(n) {
			id, _ := uuid.Parse(idStr)
			if uname == "" || uname == "null" {
				uname = idStr
			}
			out = append(out, NodeSearchResult{HolderType: HolderUser, HolderName: uname, HolderUUID: id, Node: n})
		}
	}
	return out, rows.Err()
}

// --- Groups ---

func (s *SQLStorage) CreateAndLoadGroup(name string) (*Group, error) {
	key := strings.ToLower(name)
	var insertSQL string
	switch s.dialect {
	case DialectSQLite:
		insertSQL = "INSERT OR IGNORE INTO `{prefix}groups` (`name`) VALUES(?)"
	case DialectPostgreSQL:
		insertSQL = `INSERT INTO "{prefix}groups" ("name") VALUES($1) ON CONFLICT ("name") DO NOTHING`
	default:
		insertSQL = "INSERT INTO `{prefix}groups` (`name`) VALUES(?) ON DUPLICATE KEY UPDATE `name`=`name`"
	}
	if _, err := s.db.Exec(s.q(insertSQL), key); err != nil {
		return nil, err
	}
	g, _, err := s.LoadGroup(key)
	if err != nil {
		return nil, err
	}
	if g == nil {
		g = s.lp.Groups.GetOrMake(key)
	}
	return g, nil
}

func (s *SQLStorage) LoadGroup(name string) (*Group, bool, error) {
	key := strings.ToLower(name)
	var exists string
	err := s.db.QueryRow(s.q("SELECT `name` FROM `{prefix}groups` WHERE `name`=?"), key).Scan(&exists)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	rows, err := s.db.Query(s.q("SELECT `permission`, `value`, `server`, `world`, `expiry`, `contexts` FROM `{prefix}group_permissions` WHERE `name`=?"), key)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var nodes []Node
	for rows.Next() {
		var perm, srv, world, ctxJSON string
		var val bool
		var exp int64
		if err := rows.Scan(&perm, &val, &srv, &world, &exp, &ctxJSON); err != nil {
			return nil, false, err
		}
		nodes = append(nodes, decodeNodeSQL(perm, val, srv, world, exp, ctxJSON))
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	g := s.lp.Groups.GetOrMake(key)
	g.Data(DataNormal).SetContent(nodes)
	g.InvalidateCache()
	return g, true, nil
}

func (s *SQLStorage) LoadAllGroups() error {
	rows, err := s.db.Query(s.q("SELECT `name` FROM `{prefix}groups`"))
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			names = append(names, strings.ToLower(name))
		}
	}
	rows.Close()

	nodeRows, err := s.db.Query(s.q("SELECT `name`, `permission`, `value`, `server`, `world`, `expiry`, `contexts` FROM `{prefix}group_permissions`"))
	if err != nil {
		return err
	}
	defer nodeRows.Close()
	byGroup := make(map[string][]Node)
	for nodeRows.Next() {
		var gname, perm, srv, world, ctxJSON string
		var val bool
		var exp int64
		if err := nodeRows.Scan(&gname, &perm, &val, &srv, &world, &exp, &ctxJSON); err != nil {
			return err
		}
		key := strings.ToLower(gname)
		byGroup[key] = append(byGroup[key], decodeNodeSQL(perm, val, srv, world, exp, ctxJSON))
	}

	loaded := make(map[string]struct{}, len(names))
	for _, key := range names {
		g := s.lp.Groups.GetOrMake(key)
		g.Data(DataNormal).SetContent(byGroup[key])
		loaded[key] = struct{}{}
	}
	for _, g := range s.lp.Groups.All() {
		if _, ok := loaded[g.Name()]; !ok && g.Name() != DefaultGroupName {
			s.lp.Groups.Unload(g.Name())
		}
	}
	if _, ok := loaded[DefaultGroupName]; !ok {
		if _, err := s.CreateAndLoadGroup(DefaultGroupName); err != nil {
			return err
		}
	}
	s.lp.InvalidateAllCaches()
	return nil
}

func (s *SQLStorage) SaveGroup(g *Group) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(s.q("DELETE FROM `{prefix}group_permissions` WHERE `name`=?"), g.Name()); err != nil {
		return err
	}
	for _, n := range g.NormalNodes() {
		perm, val, srv, world, exp, ctxJSON := encodeNodeSQL(n)
		if _, err := tx.Exec(s.q("INSERT INTO `{prefix}group_permissions` (`name`, `permission`, `value`, `server`, `world`, `expiry`, `contexts`) VALUES(?, ?, ?, ?, ?, ?, ?)"),
			g.Name(), perm, val, srv, world, exp, ctxJSON); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLStorage) DeleteGroup(g *Group) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(s.q("DELETE FROM `{prefix}group_permissions` WHERE `name`=?"), g.Name()); err != nil {
		return err
	}
	if _, err := tx.Exec(s.q("DELETE FROM `{prefix}groups` WHERE `name`=?"), g.Name()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.lp.Groups.Unload(g.Name())
	s.lp.InvalidateAllCaches()
	return nil
}

func (s *SQLStorage) SearchGroupNodes(match func(Node) bool) ([]NodeSearchResult, error) {
	rows, err := s.db.Query(s.q("SELECT `name`, `permission`, `value`, `server`, `world`, `expiry`, `contexts` FROM `{prefix}group_permissions`"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeSearchResult
	for rows.Next() {
		var gname, perm, srv, world, ctxJSON string
		var val bool
		var exp int64
		if err := rows.Scan(&gname, &perm, &val, &srv, &world, &exp, &ctxJSON); err != nil {
			return nil, err
		}
		n := decodeNodeSQL(perm, val, srv, world, exp, ctxJSON)
		if match(n) {
			out = append(out, NodeSearchResult{HolderType: HolderGroup, HolderName: strings.ToLower(gname), Node: n})
		}
	}
	return out, rows.Err()
}

// --- Tracks ---

func (s *SQLStorage) CreateAndLoadTrack(name string) (*Track, error) {
	key := strings.ToLower(name)
	t, ok, err := s.LoadTrack(key)
	if err != nil {
		return nil, err
	}
	if ok {
		return t, nil
	}
	t = s.lp.Tracks.GetOrMake(key)
	if _, err := s.db.Exec(s.q("INSERT INTO `{prefix}tracks` (`name`, `groups`) VALUES(?, ?)"), key, "[]"); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *SQLStorage) LoadTrack(name string) (*Track, bool, error) {
	key := strings.ToLower(name)
	var groupsJSON string
	err := s.db.QueryRow(s.q("SELECT `groups` FROM `{prefix}tracks` WHERE `name`=?"), key).Scan(&groupsJSON)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var groups []string
	_ = json.Unmarshal([]byte(groupsJSON), &groups)
	t := s.lp.Tracks.GetOrMake(key)
	t.SetGroups(groups)
	return t, true, nil
}

func (s *SQLStorage) LoadAllTracks() error {
	rows, err := s.db.Query(s.q("SELECT `name`, `groups` FROM `{prefix}tracks`"))
	if err != nil {
		return err
	}
	defer rows.Close()
	loaded := make(map[string]struct{})
	for rows.Next() {
		var name, groupsJSON string
		if err := rows.Scan(&name, &groupsJSON); err != nil {
			return err
		}
		key := strings.ToLower(name)
		var groups []string
		_ = json.Unmarshal([]byte(groupsJSON), &groups)
		t := s.lp.Tracks.GetOrMake(key)
		t.SetGroups(groups)
		loaded[key] = struct{}{}
	}
	for _, t := range s.lp.Tracks.All() {
		if _, ok := loaded[t.Name()]; !ok {
			s.lp.Tracks.Unload(t.Name())
		}
	}
	return rows.Err()
}

func (s *SQLStorage) SaveTrack(t *Track) error {
	groups := t.Groups()
	if groups == nil {
		groups = []string{}
	}
	b, _ := json.Marshal(groups)
	res, err := s.db.Exec(s.q("UPDATE `{prefix}tracks` SET `groups`=? WHERE `name`=?"), string(b), t.Name())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_, err = s.db.Exec(s.q("INSERT INTO `{prefix}tracks` (`name`, `groups`) VALUES(?, ?)"), t.Name(), string(b))
	}
	return err
}

func (s *SQLStorage) DeleteTrack(t *Track) error {
	if _, err := s.db.Exec(s.q("DELETE FROM `{prefix}tracks` WHERE `name`=?"), t.Name()); err != nil {
		return err
	}
	s.lp.Tracks.Unload(t.Name())
	return nil
}

// --- Player UUID cache ---

func (s *SQLStorage) SavePlayerData(id plugin.UUID, username string) error {
	if username == "" {
		return nil
	}
	lower := strings.ToLower(username)
	res, err := s.db.Exec(s.q("UPDATE `{prefix}players` SET `username`=? WHERE `uuid`=?"), lower, id.String())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_, err = s.db.Exec(s.q("INSERT INTO `{prefix}players` (`uuid`, `username`, `primary_group`) VALUES(?, ?, ?)"), id.String(), lower, DefaultGroupName)
		return err
	}
	return nil
}

func (s *SQLStorage) DeletePlayerData(id plugin.UUID) error {
	_, err := s.db.Exec(s.q("DELETE FROM `{prefix}players` WHERE `uuid`=?"), id.String())
	return err
}

func (s *SQLStorage) GetPlayerUniqueID(username string) (plugin.UUID, bool) {
	var idStr string
	err := s.db.QueryRow(s.q("SELECT `uuid` FROM `{prefix}players` WHERE `username`=? LIMIT 1"), strings.ToLower(username)).Scan(&idStr)
	if err != nil {
		return plugin.UUID{}, false
	}
	id, err := uuid.Parse(idStr)
	return id, err == nil
}

func (s *SQLStorage) GetPlayerName(id plugin.UUID) (string, bool) {
	var name string
	err := s.db.QueryRow(s.q("SELECT `username` FROM `{prefix}players` WHERE `uuid`=? LIMIT 1"), id.String()).Scan(&name)
	if err != nil || name == "" || name == "null" {
		return "", false
	}
	return name, true
}

// --- Action Log ---

func (s *SQLStorage) LogAction(a Action) error {
	actedUUID := "null"
	if a.TargetUUID != nil {
		actedUUID = a.TargetUUID.String()
	}
	typ := "U"
	if len(a.TargetType) > 0 {
		typ = string(a.TargetType[0])
	}
	_, err := s.db.Exec(s.q("INSERT INTO `{prefix}actions` (`time`, `actor_uuid`, `actor_name`, `type`, `acted_uuid`, `acted_name`, `action`) VALUES(?, ?, ?, ?, ?, ?, ?)"),
		a.Timestamp.Unix(), a.SourceUUID.String(), a.SourceName, typ, actedUUID, a.TargetName, a.Description)
	return err
}

func (s *SQLStorage) GetLogEntries() ([]Action, error) {
	rows, err := s.db.Query(s.q("SELECT `time`, `actor_uuid`, `actor_name`, `type`, `acted_uuid`, `acted_name`, `action` FROM `{prefix}actions` ORDER BY `time` DESC, `id` DESC"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Action
	for rows.Next() {
		var ts int64
		var actorUUID, actorName, typ, actedUUID, actedName, desc string
		if err := rows.Scan(&ts, &actorUUID, &actorName, &typ, &actedUUID, &actedName, &desc); err != nil {
			return nil, err
		}
		srcID, _ := uuid.Parse(actorUUID)
		var tgtID *plugin.UUID
		if actedUUID != "" && actedUUID != "null" {
			if parsed, err := uuid.Parse(actedUUID); err == nil {
				tgtID = &parsed
			}
		}
		var targetType ActionTargetType
		switch strings.ToUpper(strings.TrimSpace(typ)) {
		case "G":
			targetType = TargetGroup
		case "T":
			targetType = TargetTrack
		default:
			targetType = TargetUser
		}
		out = append(out, Action{
			Timestamp:   time.Unix(ts, 0).UTC(),
			SourceUUID:  srcID,
			SourceName:  actorName,
			TargetType:  targetType,
			TargetUUID:  tgtID,
			TargetName:  actedName,
			Description: desc,
		})
	}
	slices.SortFunc(out, func(a, b Action) int {
		if c := cmp.Compare(b.Timestamp.Unix(), a.Timestamp.Unix()); c != 0 {
			return c
		}
		return cmp.Compare(a.TargetName, b.TargetName)
	})
	return out, rows.Err()
}

// --- BulkUpdate ---

func (s *SQLStorage) ApplyBulkUpdate(bu *BulkUpdate) error {
	if bu.DataType == BulkDataAll || bu.DataType == BulkDataGroups {
		if err := s.bulkUpdateTable("group_permissions", "name", HolderGroup, bu); err != nil {
			return err
		}
	}
	if bu.DataType == BulkDataAll || bu.DataType == BulkDataUsers {
		if err := s.bulkUpdateTable("user_permissions", "uuid", HolderUser, bu); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLStorage) bulkUpdateTable(table, idCol string, holderType HolderType, bu *BulkUpdate) error {
	rows, err := s.db.Query(s.q(fmt.Sprintf("SELECT `%s`, `permission`, `value`, `server`, `world`, `expiry`, `contexts` FROM `{prefix}%s`", idCol, table)))
	if err != nil {
		return err
	}
	byHolder := make(map[string][]Node)
	for rows.Next() {
		var holderID, perm, srv, world, ctxJSON string
		var val bool
		var exp int64
		if err := rows.Scan(&holderID, &perm, &val, &srv, &world, &exp, &ctxJSON); err != nil {
			rows.Close()
			return err
		}
		byHolder[holderID] = append(byHolder[holderID], decodeNodeSQL(perm, val, srv, world, exp, ctxJSON))
	}
	rows.Close()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for holderID, nodes := range byHolder {
		updated, changed := bu.Apply(nodes, holderType)
		if !changed {
			continue
		}
		if _, err := tx.Exec(s.q(fmt.Sprintf("DELETE FROM `{prefix}%s` WHERE `%s`=?", table, idCol)), holderID); err != nil {
			return err
		}
		for _, n := range updated {
			perm, val, srv, world, exp, ctxJSON := encodeNodeSQL(n)
			if _, err := tx.Exec(s.q(fmt.Sprintf("INSERT INTO `{prefix}%s` (`%s`, `permission`, `value`, `server`, `world`, `expiry`, `contexts`) VALUES(?, ?, ?, ?, ?, ?, ?)", table, idCol)),
				holderID, perm, val, srv, world, exp, ctxJSON); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func defaultHostPort(addr, defPort string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "localhost:" + defPort
	}
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(addr, defPort)
}
